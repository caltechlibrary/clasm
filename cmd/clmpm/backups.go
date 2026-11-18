package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/mpreflight"
	"github.com/caltechlibrary/clasm/internal/mprestore"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// backupFetcher is the part of rdmfetch.Fetcher the verbs use, so a test can
// supply a fake.
type backupFetcher interface {
	ListDumps(ctx context.Context, bucket, source string) ([]rdmfetch.Dump, error)
	ListSnapshots(ctx context.Context, bucket, source string) ([]string, error)
	FetchDump(ctx context.Context, bucket, key string) (rdmfetch.DumpReport, error)
	FetchSnapshot(ctx context.Context, bucket, source, name string) (rdmfetch.SnapshotReport, error)
}

// backupDeps are the things the backup and restore verbs need from outside.
type backupDeps struct {
	cacheDir          string
	newFetcher        func(ctx context.Context, region, cacheDir string) (backupFetcher, error)
	pack              func(dir, tarPath string) (string, error)
	preflight         func(ctx context.Context, ex executor.Executor, o mpreflight.Options) ([]mpreflight.Finding, error)
	discoverPostgres  func(ctx context.Context, ex executor.Executor) (string, error)
	existingSQLData   func(ctx context.Context, ex executor.Executor, container, db, user string) (bool, error)
	planOpenSearch    func(ctx context.Context, ex executor.Executor, prefix string) (workflow.OpenSearchRestorePlan, error)
	restoreSQL        func(ctx context.Context, w io.Writer, ex executor.Executor, p workflow.SQLRestoreParams, acquire workflow.SQLAcquirer) error
	restoreOpenSearch func(ctx context.Context, w io.Writer, ex executor.Executor, p workflow.OpenSearchRestoreParams, acquire workflow.SnapshotAcquirer) error
	indexHealth       func(ctx context.Context, ex executor.Executor, name string, wait, poll time.Duration) (workflow.IndexHealthReport, error)
	loadConfig        func() (config.Config, error)
	saveConfig        func(config.Config) error
	readyTimeout      time.Duration
}

var errNotConfigured = errors.New("this command needs the backup tools, which are not configured")

// negativeError is a failure whose class is "the answer is no" (exit 1).
type negativeError struct{ msg string }

func (e *negativeError) Error() string { return e.msg }

// unavailableError wraps a failure to reach a service this command needs (exit 69).
type unavailableError struct{ err error }

func (e *unavailableError) Error() string { return e.err.Error() }
func (e *unavailableError) Unwrap() error { return e.err }

func (b backupDeps) ready() error {
	if b.newFetcher == nil || b.preflight == nil || b.loadConfig == nil {
		return errNotConfigured
	}
	return nil
}

func backupFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.sqlBucket, "sql-bucket", "sql-backups.library.caltech.edu", "the SQL backup bucket")
	fs.StringVar(&o.osBucket, "os-bucket", "opensearch-backups.library.caltech.edu", "the OpenSearch backup bucket")
	fs.StringVar(&o.region, "region", "us-west-2", "the buckets' region")
	fs.StringVar(&o.dumpKey, "dump", "", "the SQL dump's S3 key; default the newest")
	fs.StringVar(&o.snapshotName, "snapshot", "", "the snapshot name; default the newest")
	fs.BoolVar(&o.skipSQL, "skip-sql", false, "do not fetch the SQL dump")
	fs.BoolVar(&o.skipOpenSearch, "skip-opensearch", false, "do not fetch the snapshot")
	fs.IntVar(&o.listN, "n", 10, "how many dumps to list")
	fs.StringVar(&o.cache, "cache", "", "the directory fetched backups are kept in; default ~/.cache/clasm/fetch")
}

func cacheFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.cache, "cache", "", "the directory fetched backups are kept in; default ~/.cache/clasm/fetch")
	confirmFlag(fs, o)
}

func preflightFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.setMail, "set-mail-suppression", false, "set INVENIO_MAIL_SUPPRESS_SEND=True when it is not")
	fs.BoolVar(&o.setDataCite, "set-datacite-safeguards", false, "set DataCite test mode and block its API hosts when they are not")
	fs.BoolVar(&o.requireStopped, "require-stopped", false, "also require the RDM services to be stopped")
	fs.Int64Var(&o.need, "need", 0, "bytes of free space the load needs; 0 skips the check")
}

func restoreFlags(fs *flag.FlagSet, o *options) {
	backupFlags(fs, o)
	confirmFlag(fs, o)
	fs.BoolVar(&o.setMail, "set-mail-suppression", false, "set INVENIO_MAIL_SUPPRESS_SEND=True when it is not")
	fs.BoolVar(&o.setDataCite, "set-datacite-safeguards", false, "set DataCite test mode and block its API hosts when they are not")
	fs.BoolVar(&o.snapshotFirst, "snapshot-first", false, "stop the VM, snapshot it, and start it again before loading")
	fs.StringVar(&o.container, "container", "", "the Postgres container; default from the config or discovered")
	fs.StringVar(&o.dbName, "db-name", "", "the database name; default from the config or the project")
	fs.StringVar(&o.dbUser, "db-user", "", "the database user; default from the config or the project")
	fs.StringVar(&o.indexPrefix, "index-prefix", "", "the prefix the indices carry in the snapshot; default the project")
	fs.StringVar(&o.directory, "directory", "", "the snapshot repository directory on the VM")
}

func healthFlags(fs *flag.FlagSet, o *options) {
	fs.DurationVar(&o.wait, "wait", 5*time.Minute, "how long to wait for a healthy answer; 0 checks once")
}

func cacheDirOf(d deps, o *options) string {
	if o.cache != "" {
		return o.cache
	}
	return d.backups.cacheDir
}

func (d deps) fetcher(o *options) (backupFetcher, error) {
	if err := d.backups.ready(); err != nil {
		return nil, err
	}
	f, err := d.backups.newFetcher(d.ctx, o.region, cacheDirOf(d, o))
	if err != nil {
		return nil, &unavailableError{fmt.Errorf("reaching S3: %w", err)}
	}
	return f, nil
}

// printFindings writes the pre-flight findings, as text or JSON.
func printFindings(out cmdOut, findings []mpreflight.Finding) error {
	if out.json {
		if findings == nil {
			findings = []mpreflight.Finding{}
		}
		return out.printJSON(findings)
	}
	for _, f := range findings {
		mark := "ok   "
		if !f.OK {
			mark = "FAIL "
		}
		lines := strings.Split(strings.TrimRight(f.Detail, "\n"), "\n")
		fmt.Fprintf(out.w, "%s%s: %s\n", mark, f.Check, lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(out.w, "      %s\n", l)
		}
	}
	return nil
}

func doListBackups(d deps, o *options, args []string, out cmdOut) error {
	f, err := d.fetcher(o)
	if err != nil {
		return err
	}
	source := args[0]
	dumps, err := f.ListDumps(d.ctx, o.sqlBucket, source)
	if err != nil {
		return err
	}
	snaps, err := f.ListSnapshots(d.ctx, o.osBucket, source)
	if err != nil {
		return err
	}
	if o.listN > 0 && len(dumps) > o.listN {
		dumps = dumps[:o.listN]
	}
	if dumps == nil {
		dumps = []rdmfetch.Dump{}
	}
	if snaps == nil {
		snaps = []string{}
	}
	if out.json {
		return out.printJSON(map[string]any{"dumps": dumps, "snapshots": snaps})
	}
	fmt.Fprintf(out.w, "SQL dumps in s3://%s/%s/ (newest first):\n", o.sqlBucket, source)
	for _, dm := range dumps {
		fmt.Fprintf(out.w, "  %s  %s  %s\n", dm.LastModified.Format("2006-01-02 15:04"), gib(dm.Size), dm.Key)
	}
	fmt.Fprintf(out.w, "OpenSearch snapshots in s3://%s/%s/opensearch-snapshots/ (newest first):\n", o.osBucket, source)
	for _, s := range snaps {
		fmt.Fprintf(out.w, "  %s\n", s)
	}
	return nil
}

func chooseDump(ctx context.Context, f backupFetcher, o *options, source string) (rdmfetch.Dump, error) {
	dumps, err := f.ListDumps(ctx, o.sqlBucket, source)
	if err != nil {
		return rdmfetch.Dump{}, err
	}
	if o.dumpKey != "" {
		for _, dm := range dumps {
			if dm.Key == o.dumpKey {
				return dm, nil
			}
		}
		return rdmfetch.Dump{Key: o.dumpKey}, nil // size unknown; the fetch will say if it is missing
	}
	if len(dumps) == 0 {
		return rdmfetch.Dump{}, fmt.Errorf("no SQL dumps under s3://%s/%s/: %w", o.sqlBucket, source, rdmfetch.ErrNotFound)
	}
	return dumps[0], nil
}

func chooseSnapshot(ctx context.Context, f backupFetcher, o *options, source string) (string, error) {
	if o.snapshotName != "" {
		return o.snapshotName, nil
	}
	snaps, err := f.ListSnapshots(ctx, o.osBucket, source)
	if err != nil {
		return "", err
	}
	if len(snaps) == 0 {
		return "", fmt.Errorf("no OpenSearch snapshots under s3://%s/%s/opensearch-snapshots/: %w", o.osBucket, source, rdmfetch.ErrNotFound)
	}
	return snaps[0], nil
}

func printDumpReport(out cmdOut, r rdmfetch.DumpReport) {
	verified := "MD5 matches S3"
	if !r.MD5Verified {
		verified = "MD5 not comparable (multipart upload)"
	}
	gz := ""
	if r.GzipOK {
		gz = ", gzip tested end to end"
	}
	reused := ""
	if r.Reused {
		reused = " (already fetched)"
	}
	fmt.Fprintf(out.w, "dump %s: %s, %s%s%s\n  %s\n", r.Key, gib(r.Size), verified, gz, reused, r.Path)
}

func printSnapshotReport(out cmdOut, r rdmfetch.SnapshotReport) {
	reused := ""
	if r.Reused {
		reused = " (already fetched)"
	}
	fmt.Fprintf(out.w, "snapshot %s: %d objects, %s, every object's size checked%s\n  %s\n", r.Name, r.Objects, gib(r.Bytes), reused, r.Dir)
}

func doFetchBackups(d deps, o *options, args []string, out cmdOut) error {
	f, err := d.fetcher(o)
	if err != nil {
		return err
	}
	source := args[0]
	result := map[string]any{}
	if !o.skipSQL {
		dm, err := chooseDump(d.ctx, f, o, source)
		if err != nil {
			return err
		}
		rep, err := f.FetchDump(d.ctx, o.sqlBucket, dm.Key)
		if err != nil {
			return err
		}
		result["dump"] = rep
		if !out.json {
			printDumpReport(out, rep)
		}
	}
	if !o.skipOpenSearch {
		name, err := chooseSnapshot(d.ctx, f, o, source)
		if err != nil {
			return err
		}
		rep, err := f.FetchSnapshot(d.ctx, o.osBucket, source, name)
		if err != nil {
			return err
		}
		result["snapshot"] = rep
		if !out.json {
			printSnapshotReport(out, rep)
		}
	}
	if out.json {
		return out.printJSON(result)
	}
	return nil
}

func doRunPreflights(d deps, o *options, args []string, out cmdOut) error {
	if err := d.backups.ready(); err != nil {
		return err
	}
	findings, err := d.backups.preflight(d.ctx, d.newExecutor(args[0]), mpreflight.Options{
		Docker: true, SetMailSuppression: o.setMail, SetDataCiteSafeguards: o.setDataCite,
		RequireAppStopped: o.requireStopped, DiskNeed: o.need,
	})
	if perr := printFindings(out, findings); perr != nil {
		return perr
	}
	return err
}

func doApplication(action string) func(d deps, o *options, args []string, out cmdOut) error {
	return func(d deps, o *options, args []string, out cmdOut) error {
		vm := args[0]
		units := strings.Join(mpreflight.AppUnits(), " ")
		if !o.confirm {
			return out.dryRun(action+"-application", vm, fmt.Sprintf("would run `systemctl %s %s` on %s.", action, units, vm))
		}
		stdout, status, err := d.newExecutor(vm).Run(d.ctx, "systemctl "+action+" "+units+" 2>&1", 5*time.Minute, 5*time.Second)
		if err != nil {
			return err
		}
		if status != executor.StatusSuccess {
			return &negativeError{fmt.Sprintf("systemctl %s failed on %s (%s): %s", action, vm, status, strings.TrimSpace(stdout))}
		}
		return out.done(action+"-application", vm, fmt.Sprintf("%s: %s", vm, units))
	}
}

var (
	doStopApplication  = doApplication("stop")
	doStartApplication = doApplication("start")
)

func doShowUserData(d deps, o *options, args []string, out cmdOut) error {
	local, err := mpreflight.LocalUserData(cacheDirOf(d, o))
	if err != nil {
		return err
	}
	vmData := mpreflight.VMUserData()
	if out.json {
		return out.printJSON(map[string]any{"local": local, "vm": vmData})
	}
	fmt.Fprintf(out.w, "On this machine, in %s:\n", cacheDirOf(d, o))
	var total int64
	for _, f := range local {
		total += f.Bytes
		fmt.Fprintf(out.w, "  %s  %s\n", gib(f.Bytes), f.Path)
	}
	if len(local) == 0 {
		fmt.Fprintln(out.w, "  nothing")
	} else {
		fmt.Fprintf(out.w, "  %d files, %s; `clmpm remove-fetched-backups -confirm` deletes them\n", len(local), gib(total))
	}
	fmt.Fprintf(out.w, "On %s:\n", args[0])
	for _, l := range vmData {
		fmt.Fprintf(out.w, "  %s\n", l)
	}
	return nil
}

func doRemoveFetchedBackups(d deps, o *options, args []string, out cmdOut) error {
	dir := cacheDirOf(d, o)
	local, err := mpreflight.LocalUserData(dir)
	if err != nil {
		return err
	}
	if !o.confirm {
		var b strings.Builder
		for _, f := range local {
			b.WriteString("\n  " + f.Path)
		}
		return out.dryRun("remove-fetched-backups", dir, fmt.Sprintf("would delete %d file(s) under %s:%s", len(local), dir, b.String()))
	}
	if err := mpreflight.RemoveLocalUserData(dir); err != nil {
		return err
	}
	return out.done("remove-fetched-backups", dir, fmt.Sprintf("removed %d file(s) from %s", len(local), dir))
}

func doCheckIndexHealth(d deps, o *options, args []string, out cmdOut) error {
	if d.backups.indexHealth == nil {
		return errNotConfigured
	}
	rep, err := d.backups.indexHealth(d.ctx, d.newExecutor(args[0]), args[0], o.wait, 5*time.Second)
	if err != nil {
		return err
	}
	if out.json {
		if perr := out.printJSON(rep); perr != nil {
			return perr
		}
	} else if rep.Healthy {
		fmt.Fprintf(out.w, "%s: the RDM log indices are healthy\n", args[0])
	} else {
		fmt.Fprintf(out.w, "%s: the RDM log indices have problems:\n", args[0])
		for _, p := range rep.Problems {
			fmt.Fprintf(out.w, "  %s\n", p)
		}
	}
	if !rep.Healthy {
		return &negativeError{"the index health check found problems"}
	}
	return nil
}

// safetySnapshot stops the VM, snapshots it, starts it again and waits until it
// answers, so a failed load costs seconds to undo (multipass restore). The
// services may start again with the VM; the pre-flights that follow say so.
func safetySnapshot(d deps, vm, verb string, out cmdOut) error {
	name := "before-" + verb + "-" + time.Now().UTC().Format("20060102T150405Z")
	if err := d.client.Stop(d.ctx, vm); err != nil {
		return fmt.Errorf("stopping %s for its safety snapshot: %w", vm, err)
	}
	if err := d.client.Snapshot(d.ctx, vm, name, "taken by clmpm "+verb); err != nil {
		return fmt.Errorf("snapshotting %s: %w", vm, err)
	}
	if err := d.client.Start(d.ctx, vm); err != nil {
		return fmt.Errorf("starting %s after its snapshot (the snapshot %s was taken): %w", vm, name, err)
	}
	ex := d.newExecutor(vm)
	timeout := d.backups.readyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, status, err := ex.Run(d.ctx, "true", 30*time.Second, time.Second); err == nil && status == executor.StatusSuccess {
			break
		}
		if time.Now().After(deadline) || d.ctx.Err() != nil {
			return fmt.Errorf("%s did not answer within %s after its snapshot %s", vm, timeout, name)
		}
		time.Sleep(2 * time.Second)
	}
	if !out.json {
		fmt.Fprintf(out.w, "snapshot %s taken; `multipass restore --destructive %s.%s` returns the VM to it\n", name, vm, name)
	}
	return nil
}

var dumpProjectRE = regexp.MustCompile(`^.+?-db-\d+-(.+?)-\d{4}-\d{2}-\d{2}`)
var versionSuffixRE = regexp.MustCompile(`-v\d+(\.\d+)*$`)

// projectOf names the RDM project a backup belongs to: the project inside a
// dump's file name (caltechauthors-db-1-caltechauthors-2026-11-18.sql.gz), else
// the source prefix without its version suffix (caltechauthors-v13).
func projectOf(dumpKey, source string) string {
	if m := dumpProjectRE.FindStringSubmatch(path.Base(dumpKey)); m != nil {
		return m[1]
	}
	return versionSuffixRE.ReplaceAllString(source, "")
}

func sourceProject(source string) string { return versionSuffixRE.ReplaceAllString(source, "") }

func printPlanHeader(out cmdOut, what string) {
	fmt.Fprintf(out.w, "Dry run: %s\n", what)
}

func finishRestore(d deps, out cmdOut, action, vm string) error {
	if out.json {
		return out.done(action, vm, "")
	}
	fmt.Fprintf(out.w, "\nNext: start the application (`clmpm start-application -confirm %s`), then `clmpm check-index-health %s`.\n", vm, vm)
	fmt.Fprintf(out.w, "This VM and this machine now hold production user data: `clmpm show-user-data %s` lists where, and `clmpm remove-fetched-backups -confirm` deletes the downloads.\n", vm)
	return nil
}

func preflightOptions(o *options, confirm bool, need int64) mpreflight.Options {
	return mpreflight.Options{
		Docker: true, RequireAppStopped: true, DiskNeed: need,
		SetMailSuppression: confirm && o.setMail, SetDataCiteSafeguards: confirm && o.setDataCite,
	}
}

func doRestoreSQL(d deps, o *options, args []string, out cmdOut) error {
	f, err := d.fetcher(o)
	if err != nil {
		return err
	}
	vm, source := args[0], args[1]
	ex := d.newExecutor(vm)
	dump, err := chooseDump(d.ctx, f, o, source)
	if err != nil {
		return err
	}
	cfg, err := d.backups.loadConfig()
	if err != nil {
		return err
	}
	project := projectOf(dump.Key, source)
	container, dbName, dbUser := config.RDMPostgresConfigForHost(cfg.RDMPostgresConfig, config.HostMultipass, vm, project)
	if o.container != "" {
		container = o.container
	}
	if o.dbName != "" {
		dbName = o.dbName
	}
	if o.dbUser != "" {
		dbUser = o.dbUser
	}
	discovered := false
	if container == "" && d.backups.discoverPostgres != nil {
		c, derr := d.backups.discoverPostgres(d.ctx, ex)
		if derr != nil && o.confirm {
			return fmt.Errorf("finding the Postgres container on %s (use -container to name it): %w", vm, derr)
		}
		container, discovered = c, derr == nil
	}
	need := mpreflight.DumpNeed(dump.Size, workflow.NeedsDecompression(dump.Key))

	if !o.confirm {
		printPlanHeader(out, "nothing was downloaded or changed.")
		fmt.Fprintf(out.w, "  load s3://%s/%s (%s) into database %q (user %q, container %q) on %s\n", o.sqlBucket, dump.Key, gib(dump.Size), dbName, dbUser, valueOr(container, "unknown"), vm)
		if container != "" && d.backups.existingSQLData != nil {
			if has, derr := d.backups.existingSQLData(d.ctx, ex, container, dbName, dbUser); derr == nil && has {
				fmt.Fprintf(out.w, "  the database %q holds tables now and they will be dropped\n", dbName)
			}
		}
		findings, perr := d.backups.preflight(d.ctx, ex, preflightOptions(o, false, need))
		if err := printFindings(out, findings); err != nil {
			return err
		}
		if perr != nil {
			return perr
		}
		fmt.Fprintln(out.w, "Add -confirm to download, check, and load it.")
		return nil
	}

	rep, err := f.FetchDump(d.ctx, o.sqlBucket, dump.Key)
	if err != nil {
		return err
	}
	if !out.json {
		printDumpReport(out, rep)
	}
	if o.snapshotFirst {
		if err := safetySnapshot(d, vm, "restore-sql", out); err != nil {
			return err
		}
	}
	findings, perr := d.backups.preflight(d.ctx, ex, preflightOptions(o, true, mpreflight.DumpNeed(rep.Size, workflow.NeedsDecompression(rep.Key))))
	if err := printFindings(out, findings); err != nil {
		return err
	}
	if perr != nil {
		return perr
	}
	if discovered && d.backups.saveConfig != nil {
		cfg.RDMPostgresConfig = config.UpsertRDMPostgresRuleForHost(cfg.RDMPostgresConfig, config.HostMultipass, vm, container)
		if err := d.backups.saveConfig(cfg); err != nil {
			return fmt.Errorf("saving the Postgres container name to the config: %w", err)
		}
	}
	w := out.w
	if out.json {
		w = io.Discard
	}
	if err := d.backups.restoreSQL(d.ctx, w, ex, workflow.SQLRestoreParams{ContainerName: container, DBName: dbName, DBUser: dbUser, Bucket: o.sqlBucket, Key: dump.Key}, mprestore.SQLAcquirer(rep)); err != nil {
		return err
	}
	return finishRestore(d, out, "restore-sql", vm)
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func doRestoreOpenSearch(d deps, o *options, args []string, out cmdOut) error {
	f, err := d.fetcher(o)
	if err != nil {
		return err
	}
	if d.backups.planOpenSearch == nil || d.backups.restoreOpenSearch == nil || d.backups.pack == nil {
		return errNotConfigured
	}
	vm, source := args[0], args[1]
	ex := d.newExecutor(vm)
	name, err := chooseSnapshot(d.ctx, f, o, source)
	if err != nil {
		return err
	}
	prefix := o.indexPrefix
	if prefix == "" {
		prefix = sourceProject(source)
	}
	cfg, err := d.backups.loadConfig()
	if err != nil {
		return err
	}
	directory := o.directory
	if directory == "" {
		directory = config.BackupDirectoryForHost(cfg.OpenSearchBackupDirectories, config.HostMultipass, vm)
	}
	if directory == "" {
		directory = "/opt/rdm_opensearch_backups"
	}
	plan, err := d.backups.planOpenSearch(d.ctx, ex, prefix)
	if err != nil {
		return &unavailableError{err}
	}

	if !o.confirm {
		printPlanHeader(out, "nothing was downloaded or changed.")
		fmt.Fprintf(out.w, "  restore snapshot %s from s3://%s/%s/opensearch-snapshots/ onto %s, index prefix %q, repository directory %s\n", name, o.osBucket, source, vm, prefix, directory)
		if len(plan.Existing) > 0 {
			fmt.Fprintf(out.w, "  these %d indices are on %s now and will be deleted and replaced:\n", len(plan.Existing), vm)
			for _, e := range plan.Existing {
				fmt.Fprintf(out.w, "    %s\n", e)
			}
		}
		findings, perr := d.backups.preflight(d.ctx, ex, preflightOptions(o, false, 0))
		if err := printFindings(out, findings); err != nil {
			return err
		}
		if perr != nil {
			return perr
		}
		fmt.Fprintln(out.w, "Add -confirm to download, check, and load it.")
		return nil
	}

	rep, err := f.FetchSnapshot(d.ctx, o.osBucket, source, name)
	if err != nil {
		return err
	}
	if !out.json {
		printSnapshotReport(out, rep)
	}
	tarPath := path.Join(cacheDirOf(d, o), name+".tar")
	if _, err := d.backups.pack(rep.Dir, tarPath); err != nil {
		return fmt.Errorf("packing the snapshot: %w", err)
	}
	if o.snapshotFirst {
		if err := safetySnapshot(d, vm, "restore-opensearch", out); err != nil {
			return err
		}
	}
	findings, perr := d.backups.preflight(d.ctx, ex, preflightOptions(o, true, mpreflight.SnapshotNeed(rep.Bytes)))
	if err := printFindings(out, findings); err != nil {
		return err
	}
	if perr != nil {
		return perr
	}
	w := out.w
	if out.json {
		w = io.Discard
	}
	params := workflow.OpenSearchRestoreParams{
		HostName: vm, Owner: plan.Owner, Directory: directory, Bucket: o.osBucket, SourceName: source, IndexPrefix: prefix,
		Snapshot: workflow.SnapshotPrefixInfo{Name: name}, Indices: plan.Indices, Existing: plan.Existing,
	}
	if err := d.backups.restoreOpenSearch(d.ctx, w, ex, params, mprestore.SnapshotAcquirer(mprestore.Snapshot{TarPath: tarPath, Objects: rep.Objects})); err != nil {
		return err
	}
	return finishRestore(d, out, "restore-opensearch", vm)
}

func cacheOnlyFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.cache, "cache", "", "the directory fetched backups are kept in; default ~/.cache/clasm/fetch")
}
