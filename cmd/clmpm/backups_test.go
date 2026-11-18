package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/mpreflight"
	"github.com/caltechlibrary/clasm/internal/multipass"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// world records, in order, everything the backup verbs do, across the fake
// fetcher, the fake host, the fake multipass and the fake restore functions, so
// a test can assert the order that matters (fetch before preflights before load).
type world struct {
	t      *testing.T
	events []string

	cache       string
	dumps       []rdmfetch.Dump
	snaps       []string
	dumpReport  rdmfetch.DumpReport
	snapReport  rdmfetch.SnapshotReport
	fetchErr    error
	findings    []mpreflight.Finding
	preflightOK bool
	lastOptions mpreflight.Options
	preflights  int

	restoreSQLParams workflow.SQLRestoreParams
	sqlAcquirer      workflow.SQLAcquirer
	osParams         workflow.OpenSearchRestoreParams
	osAcquirer       workflow.SnapshotAcquirer
	restoreErr       error

	rules      []config.RDMPostgresRule
	osRules    []config.BackupDirectoryRule
	saved      *config.Config
	discovered int
	plan       workflow.OpenSearchRestorePlan
	health     workflow.IndexHealthReport

	hostCommands []string
	hostStdout   string
	putCalls     []string
}

func (w *world) ev(s string) { w.events = append(w.events, s) }

func (w *world) indexOf(prefix string) int {
	for i, e := range w.events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

type fakeFetcherW struct{ w *world }

func (f fakeFetcherW) ListDumps(ctx context.Context, bucket, source string) ([]rdmfetch.Dump, error) {
	f.w.ev("list-dumps " + source)
	return f.w.dumps, f.w.fetchErr
}
func (f fakeFetcherW) ListSnapshots(ctx context.Context, bucket, source string) ([]string, error) {
	f.w.ev("list-snapshots " + source)
	return f.w.snaps, f.w.fetchErr
}
func (f fakeFetcherW) FetchDump(ctx context.Context, bucket, key string) (rdmfetch.DumpReport, error) {
	f.w.ev("fetch-dump " + key)
	if f.w.fetchErr != nil {
		return rdmfetch.DumpReport{}, f.w.fetchErr
	}
	r := f.w.dumpReport
	r.Key = key
	return r, nil
}
func (f fakeFetcherW) FetchSnapshot(ctx context.Context, bucket, source, name string) (rdmfetch.SnapshotReport, error) {
	f.w.ev("fetch-snapshot " + name)
	if f.w.fetchErr != nil {
		return rdmfetch.SnapshotReport{}, f.w.fetchErr
	}
	r := f.w.snapReport
	r.Name = name
	return r, nil
}

type hostExec struct{ w *world }

func (h hostExec) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, executor.CommandStatus, error) {
	h.w.hostCommands = append(h.w.hostCommands, command)
	h.w.ev("host " + command)
	return h.w.hostStdout, executor.StatusSuccess, nil
}
func (h hostExec) Put(ctx context.Context, local, remote string) error {
	h.w.putCalls = append(h.w.putCalls, local+" -> "+remote)
	return nil
}
func (h hostExec) Get(ctx context.Context, remote, local string) error { return nil }
func (h hostExec) Name() string                                        { return "vm" }

// newWorld returns a deps wired to fakes. The cache directory is a temp dir.
func newWorld(t *testing.T) (*world, deps) {
	w := &world{t: t, cache: t.TempDir(), preflightOK: true}
	w.dumps = []rdmfetch.Dump{
		{Key: "src/db-2026-11-18.sql.gz", Size: 300 << 20, LastModified: time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)},
		{Key: "src/db-2026-11-17.sql.gz", Size: 290 << 20, LastModified: time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)},
	}
	w.snaps = []string{"rdm-20261118-133546", "rdm-20261117-133550"}
	w.dumpReport = rdmfetch.DumpReport{Path: filepath.Join(w.cache, "db-2026-11-18.sql.gz"), Size: 300 << 20, MD5: "abc", MD5Verified: true, GzipOK: true}
	w.snapReport = rdmfetch.SnapshotReport{Dir: filepath.Join(w.cache, "rdm-20261118-133546"), Objects: 7, Bytes: 2 << 30}
	w.plan = workflow.OpenSearchRestorePlan{Owner: workflow.ServiceOwner{UID: 1000, GID: 1001}, Indices: []string{"caltechauthors-rdmrecords-*"}, Existing: []string{"caltechauthors-rdmrecords-records-record-v7.0.0"}}
	w.health = workflow.IndexHealthReport{Healthy: true}

	mp := &mp{stdout: map[string]string{"list": listJSON, "info": infoJSON}, errs: map[string]error{}}
	client := multipass.NewWithRunner(func(ctx context.Context, args ...string) (string, error) {
		w.ev("mp " + strings.Join(args, " "))
		return mp.run(ctx, args...)
	})
	d := deps{
		client:      client,
		newExecutor: func(string) executor.Executor { return hostExec{w} },
		backups: backupDeps{
			cacheDir: w.cache,
			newFetcher: func(ctx context.Context, region, cacheDir string) (backupFetcher, error) {
				return fakeFetcherW{w}, nil
			},
			pack: func(dir, tarPath string) (string, error) {
				w.ev("pack " + filepath.Base(dir))
				return "sha", os.WriteFile(tarPath, []byte("tar"), 0o600)
			},
			preflight: func(ctx context.Context, ex executor.Executor, o mpreflight.Options) ([]mpreflight.Finding, error) {
				w.preflights++
				w.lastOptions = o
				w.ev("preflight")
				fs := w.findings
				if fs == nil {
					fs = []mpreflight.Finding{{Check: "mail suppression", OK: w.preflightOK, Detail: "detail"}}
				}
				if !w.preflightOK {
					return fs, &mpreflight.RefusedError{Findings: fs}
				}
				return fs, nil
			},
			discoverPostgres: func(ctx context.Context, ex executor.Executor) (string, error) {
				w.discovered++
				w.ev("discover-postgres")
				return "discovered-db", nil
			},
			existingSQLData: func(ctx context.Context, ex executor.Executor, c, db, u string) (bool, error) {
				return true, nil
			},
			planOpenSearch: func(ctx context.Context, ex executor.Executor, prefix string) (workflow.OpenSearchRestorePlan, error) {
				w.ev("plan-opensearch " + prefix)
				return w.plan, nil
			},
			restoreSQL: func(ctx context.Context, out io.Writer, ex executor.Executor, p workflow.SQLRestoreParams, acquire workflow.SQLAcquirer) error {
				w.ev("restore-sql")
				w.restoreSQLParams, w.sqlAcquirer = p, acquire
				return w.restoreErr
			},
			restoreOpenSearch: func(ctx context.Context, out io.Writer, ex executor.Executor, p workflow.OpenSearchRestoreParams, acquire workflow.SnapshotAcquirer) error {
				w.ev("restore-opensearch")
				w.osParams, w.osAcquirer = p, acquire
				return w.restoreErr
			},
			indexHealth: func(ctx context.Context, ex executor.Executor, name string, wait, poll time.Duration) (workflow.IndexHealthReport, error) {
				w.ev("index-health " + name)
				return w.health, nil
			},
			loadConfig: func() (config.Config, error) {
				return config.Config{RDMPostgresConfig: w.rules, OpenSearchBackupDirectories: w.osRules}, nil
			},
			saveConfig:   func(c config.Config) error { w.saved = &c; return nil },
			readyTimeout: 2 * time.Second,
		},
	}
	return w, d
}

func runW(t *testing.T, d deps, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e, d)
	return code, o.String(), e.String()
}

// ---- list-backups, fetch-backups ----

func TestListBackups_ShowsTheNewestDumpsAndAllSnapshotsReadOnly(t *testing.T) {
	w, d := newWorld(t)
	code, out, _ := runW(t, d, "list-backups", "src")
	if code != 0 || !strings.Contains(out, "db-2026-11-18.sql.gz") || !strings.Contains(out, "rdm-20261118-133546") {
		t.Errorf("code=%d out=%q", code, out)
	}
	for _, e := range w.events {
		if strings.HasPrefix(e, "fetch-") {
			t.Errorf("listing must not download: %v", w.events)
		}
	}
}

func TestListBackups_JSONHasDumpsAndSnapshotsAndHonoursN(t *testing.T) {
	_, d := newWorld(t)
	code, out, _ := runW(t, d, "list-backups", "-json", "-n", "1", "src")
	var got struct {
		Dumps     []rdmfetch.Dump `json:"dumps"`
		Snapshots []string        `json:"snapshots"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Dumps) != 1 || len(got.Snapshots) != 2 {
		t.Errorf("code=%d out=%q got=%+v", code, out, got)
	}
}

func TestListBackups_AnS3ErrorIsNotASuccess(t *testing.T) {
	w, d := newWorld(t)
	w.fetchErr = errors.New("ExpiredToken")
	code, _, errOut := runW(t, d, "list-backups", "src")
	if code == 0 || !strings.Contains(errOut, "ExpiredToken") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestFetchBackups_DownloadsTheNewestOfEachByDefaultAndReportsWhatItVerified(t *testing.T) {
	w, d := newWorld(t)
	code, out, _ := runW(t, d, "fetch-backups", "src")
	if code != 0 || w.indexOf("fetch-dump src/db-2026-11-18.sql.gz") < 0 || w.indexOf("fetch-snapshot rdm-20261118-133546") < 0 {
		t.Fatalf("code=%d events=%v", code, w.events)
	}
	for _, want := range []string{"db-2026-11-18.sql.gz", "md5", "gzip", "rdm-20261118-133546", "7 objects"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFetchBackups_NamedBackupsAndSkipFlags(t *testing.T) {
	w, d := newWorld(t)
	code, _, _ := runW(t, d, "fetch-backups", "-dump", "src/db-2026-11-17.sql.gz", "-skip-opensearch", "src")
	if code != 0 || w.indexOf("fetch-dump src/db-2026-11-17.sql.gz") < 0 || w.indexOf("fetch-snapshot") >= 0 {
		t.Errorf("code=%d events=%v", code, w.events)
	}
	w2, d2 := newWorld(t)
	code, _, _ = runW(t, d2, "fetch-backups", "-snapshot", "rdm-20261117-133550", "-skip-sql", "src")
	if code != 0 || w2.indexOf("fetch-snapshot rdm-20261117-133550") < 0 || w2.indexOf("fetch-dump") >= 0 {
		t.Errorf("code=%d events=%v", code, w2.events)
	}
}

func TestFetchBackups_NothingToFetchIsExit1(t *testing.T) {
	w, d := newWorld(t)
	w.dumps, w.snaps = nil, nil
	code, _, errOut := runW(t, d, "fetch-backups", "src")
	if code != 1 || !strings.Contains(errOut, "src") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestFetchBackups_ACorruptDownloadIsExit65(t *testing.T) {
	w, d := newWorld(t)
	w.fetchErr = rdmfetch.ErrCorrupt
	code, _, _ := runW(t, d, "fetch-backups", "src")
	if code != 65 {
		t.Errorf("code = %d, want 65 (data)", code)
	}
}

// ---- run-preflights ----

func TestRunPreflights_PassesTheFlagsThroughAndReportsEachFinding(t *testing.T) {
	w, d := newWorld(t)
	w.findings = []mpreflight.Finding{{Check: "mail suppression", OK: true, Detail: "fine"}, {Check: "disk space", OK: true, Detail: "12 GiB free"}}
	code, out, _ := runW(t, d, "run-preflights", "-set-mail-suppression", "-set-datacite-safeguards", "-require-stopped", "-need", "5000", "vm")
	o := w.lastOptions
	if code != 0 || !o.SetMailSuppression || !o.SetDataCiteSafeguards || !o.RequireAppStopped || o.DiskNeed != 5000 || !o.Docker {
		t.Errorf("code=%d options=%+v", code, o)
	}
	if !strings.Contains(out, "mail suppression") || !strings.Contains(out, "12 GiB free") {
		t.Errorf("out=%s", out)
	}
}

func TestRunPreflights_ChangesNothingUnlessAskedAndARefusalIsExit1WithTheFindings(t *testing.T) {
	w, d := newWorld(t)
	w.preflightOK = false
	w.findings = []mpreflight.Finding{{Check: "no production credentials", OK: false, Detail: "x: INVENIO_S3_ACCESS_KEY_ID"}}
	code, out, _ := runW(t, d, "run-preflights", "vm")
	if code != 1 || w.lastOptions.SetMailSuppression || w.lastOptions.SetDataCiteSafeguards {
		t.Errorf("code=%d options=%+v", code, w.lastOptions)
	}
	if !strings.Contains(strings.ToUpper(out), "FAIL") || !strings.Contains(out, "INVENIO_S3_ACCESS_KEY_ID") {
		t.Errorf("the refusal should be on stdout where a person reads it:\n%s", out)
	}
}

func TestRunPreflights_JSONIsTheFindingList(t *testing.T) {
	_, d := newWorld(t)
	code, out, _ := runW(t, d, "run-preflights", "-json", "vm")
	var got []mpreflight.Finding
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || got[0].Check != "mail suppression" {
		t.Errorf("code=%d out=%q", code, out)
	}
}

// ---- stop and start the application ----

func TestStopAndStartApplication_AreDryRunsUntilConfirmed(t *testing.T) {
	for _, verb := range []string{"stop-application", "start-application"} {
		w, d := newWorld(t)
		code, out, _ := runW(t, d, verb, "vm")
		if code != 0 || len(w.hostCommands) != 0 || !strings.Contains(strings.ToLower(out), "dry run") {
			t.Errorf("%s: code=%d commands=%v out=%q", verb, code, w.hostCommands, out)
		}
		w, d = newWorld(t)
		code, _, _ = runW(t, d, verb, "-confirm", "vm")
		action := strings.TrimSuffix(verb, "-application")
		if code != 0 || len(w.hostCommands) != 1 || !strings.Contains(w.hostCommands[0], "systemctl "+action) || !strings.Contains(w.hostCommands[0], "rdm.service") || !strings.Contains(w.hostCommands[0], "rdm_celery.service") {
			t.Errorf("%s -confirm: code=%d commands=%v", verb, code, w.hostCommands)
		}
	}
}

// ---- restore-sql ----

func TestRestoreSQL_IsADryRunThatDownloadsAndChangesNothingButStillRunsThePreflightsReadOnly(t *testing.T) {
	w, d := newWorld(t)
	code, out, _ := runW(t, d, "restore-sql", "vm", "src")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if w.indexOf("fetch-") >= 0 || w.indexOf("restore-sql") >= 0 || w.indexOf("mp ") >= 0 {
		t.Errorf("a dry run downloads and changes nothing: %v", w.events)
	}
	if w.preflights != 1 || w.lastOptions.SetMailSuppression || w.lastOptions.SetDataCiteSafeguards || !w.lastOptions.RequireAppStopped {
		t.Errorf("the preflights run read-only in a dry run: %d %+v", w.preflights, w.lastOptions)
	}
	if want := mpreflight.DumpNeed(300<<20, true); w.lastOptions.DiskNeed != want {
		t.Errorf("DiskNeed = %d, want %d from the listing's size", w.lastOptions.DiskNeed, want)
	}
	for _, want := range []string{"db-2026-11-18.sql.gz", "-confirm", "dry run"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("plan missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreSQL_ADryRunThatThePreflightsWouldRefuseIsExit1SoYouLearnItNow(t *testing.T) {
	w, d := newWorld(t)
	w.preflightOK = false
	code, out, _ := runW(t, d, "restore-sql", "vm", "src")
	if code != 1 || !strings.Contains(strings.ToUpper(out), "FAIL") {
		t.Errorf("code=%d out=%s", code, out)
	}
}

func TestRestoreSQL_ConfirmedRunsFetchThenPreflightsThenTheLoadInThatOrder(t *testing.T) {
	w, d := newWorld(t)
	code, out, errOut := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	fetch, pre, load := w.indexOf("fetch-dump src/db-2026-11-18.sql.gz"), w.indexOf("preflight"), w.indexOf("restore-sql")
	if !(fetch >= 0 && fetch < pre && pre < load) {
		t.Errorf("order wrong: %v", w.events)
	}
	p := w.restoreSQLParams
	if p.Bucket != "sql-backups.library.caltech.edu" || p.Key != "src/db-2026-11-18.sql.gz" {
		t.Errorf("params = %+v", p)
	}
	// the acquirer sends the file that was fetched
	if err := os.WriteFile(w.dumpReport.Path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.sqlAcquirer(context.Background(), hostExec{w}); err != nil {
		t.Fatal(err)
	}
	if len(w.putCalls) == 0 || !strings.HasPrefix(w.putCalls[0], w.dumpReport.Path+" -> ") {
		t.Errorf("puts = %v, want the fetched dump %s", w.putCalls, w.dumpReport.Path)
	}
}

func TestRestoreSQL_ARefusedPreflightStopsBeforeTheLoad(t *testing.T) {
	w, d := newWorld(t)
	w.preflightOK = false
	code, out, _ := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if code != 1 || w.indexOf("restore-sql") >= 0 || !strings.Contains(strings.ToUpper(out), "FAIL") {
		t.Errorf("code=%d events=%v out=%s", code, w.events, out)
	}
}

func TestRestoreSQL_AFailedDownloadStopsEverythingAfterIt(t *testing.T) {
	w, d := newWorld(t)
	w.fetchErr = rdmfetch.ErrCorrupt
	code, _, _ := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if code != 65 || w.indexOf("preflight") >= 0 || w.indexOf("restore-sql") >= 0 {
		t.Errorf("code=%d events=%v", code, w.events)
	}
}

func TestRestoreSQL_SetFlagsReachThePreflightsOnlyWhenConfirmed(t *testing.T) {
	w, d := newWorld(t)
	runW(t, d, "restore-sql", "-confirm", "-set-mail-suppression", "-set-datacite-safeguards", "vm", "src")
	if !w.lastOptions.SetMailSuppression || !w.lastOptions.SetDataCiteSafeguards || !w.lastOptions.RequireAppStopped {
		t.Errorf("options = %+v", w.lastOptions)
	}
}

func TestRestoreSQL_SnapshotFirstStopsSnapshotsAndStartsTheVMBeforeThePreflights(t *testing.T) {
	w, d := newWorld(t)
	code, _, errOut := runW(t, d, "restore-sql", "-confirm", "-snapshot-first", "vm", "src")
	if code != 0 {
		t.Fatalf("code=%d err=%s events=%v", code, errOut, w.events)
	}
	stop, snap, start, pre, load := w.indexOf("mp stop vm"), w.indexOf("mp snapshot vm --name before-restore-sql"), w.indexOf("mp start vm"), w.indexOf("preflight"), w.indexOf("restore-sql")
	if !(stop >= 0 && stop < snap && snap < start && start < pre && pre < load) {
		t.Errorf("order wrong: %v", w.events)
	}
}

func TestRestoreSQL_ADryRunWithSnapshotFirstDoesNotTouchTheVM(t *testing.T) {
	w, d := newWorld(t)
	runW(t, d, "restore-sql", "-snapshot-first", "vm", "src")
	if w.indexOf("mp ") >= 0 {
		t.Errorf("events = %v", w.events)
	}
}

func TestRestoreSQL_ADiscoveredContainerIsSavedAsAMultipassRuleAndAKnownOneIsNotDiscovered(t *testing.T) {
	w, d := newWorld(t)
	runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if w.discovered != 1 || w.restoreSQLParams.ContainerName != "discovered-db" {
		t.Errorf("discovered=%d params=%+v", w.discovered, w.restoreSQLParams)
	}
	if w.saved == nil || len(w.saved.RDMPostgresConfig) != 1 || w.saved.RDMPostgresConfig[0].Host != config.HostMultipass || w.saved.RDMPostgresConfig[0].Pattern != "vm" || w.saved.RDMPostgresConfig[0].ContainerName != "discovered-db" {
		t.Errorf("saved = %+v", w.saved)
	}

	w2, d2 := newWorld(t)
	w2.rules = []config.RDMPostgresRule{{Pattern: "vm", Host: config.HostMultipass, ContainerName: "known-db", DBName: "mydb", DBUser: "myuser"}}
	runW(t, d2, "restore-sql", "-confirm", "vm", "src")
	p := w2.restoreSQLParams
	if w2.discovered != 0 || p.ContainerName != "known-db" || p.DBName != "mydb" || p.DBUser != "myuser" {
		t.Errorf("discovered=%d params=%+v", w2.discovered, p)
	}
}

func TestRestoreSQL_AnAWSRuleWithTheSamePatternIsNotUsedForAVM(t *testing.T) {
	w, d := newWorld(t)
	w.rules = []config.RDMPostgresRule{{Pattern: "vm", ContainerName: "aws-db", DBName: "awsdb"}}
	runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if w.restoreSQLParams.ContainerName == "aws-db" || w.restoreSQLParams.DBName == "awsdb" {
		t.Errorf("a Multipass restore used an AWS rule: %+v", w.restoreSQLParams)
	}
}

func TestRestoreSQL_FlagsOverrideTheRuleAndTheDefaultsComeFromTheDumpName(t *testing.T) {
	w, d := newWorld(t)
	w.dumps = []rdmfetch.Dump{{Key: "caltechauthors-v13/caltechauthors-db-1-caltechauthors-2026-11-18.sql.gz", Size: 1 << 20, LastModified: time.Now()}}
	runW(t, d, "restore-sql", "-confirm", "vm", "caltechauthors-v13")
	if p := w.restoreSQLParams; p.DBName != "caltechauthors" || p.DBUser != "caltechauthors" {
		t.Errorf("defaults should come from the project in the dump name: %+v", p)
	}
	w2, d2 := newWorld(t)
	runW(t, d2, "restore-sql", "-confirm", "-container", "c1", "-db-name", "n1", "-db-user", "u1", "vm", "src")
	if p := w2.restoreSQLParams; p.ContainerName != "c1" || p.DBName != "n1" || p.DBUser != "u1" || w2.discovered != 0 {
		t.Errorf("params = %+v discovered=%d", p, w2.discovered)
	}
}

func TestRestoreSQL_ALoadFailureIsReportedAndExitsNonZero(t *testing.T) {
	w, d := newWorld(t)
	w.restoreErr = errors.New("psql failed")
	code, _, errOut := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if code == 0 || !strings.Contains(errOut, "psql failed") {
		t.Errorf("code=%d err=%s", code, errOut)
	}
}

func TestRestoreSQL_SaysWhereTheUserDataIsAfterwards(t *testing.T) {
	_, d := newWorld(t)
	_, out, _ := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if !strings.Contains(out, "show-user-data") || !strings.Contains(out, "remove-fetched-backups") {
		t.Errorf("out = %s", out)
	}
}

// ---- restore-opensearch ----

func TestRestoreOpenSearch_IsADryRunThatShowsWhatWouldBeReplaced(t *testing.T) {
	w, d := newWorld(t)
	code, out, _ := runW(t, d, "restore-opensearch", "vm", "src")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if w.indexOf("fetch-") >= 0 || w.indexOf("pack") >= 0 || w.indexOf("restore-opensearch") >= 0 || w.indexOf("mp ") >= 0 {
		t.Errorf("a dry run downloads and changes nothing: %v", w.events)
	}
	if w.indexOf("plan-opensearch src") < 0 && w.indexOf("plan-opensearch") < 0 {
		t.Errorf("the plan should be read: %v", w.events)
	}
	for _, want := range []string{"rdm-20261118-133546", "caltechauthors-rdmrecords-records-record-v7.0.0", "-confirm"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreOpenSearch_ConfirmedRunsFetchPackPreflightsThenTheLoad(t *testing.T) {
	w, d := newWorld(t)
	code, out, errOut := runW(t, d, "restore-opensearch", "-confirm", "-index-prefix", "caltechauthors", "vm", "src")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	fetch, pack, pre, load := w.indexOf("fetch-snapshot rdm-20261118-133546"), w.indexOf("pack rdm-20261118-133546"), w.indexOf("preflight"), w.indexOf("restore-opensearch")
	if !(fetch >= 0 && fetch < pack && pack < pre && pre < load) {
		t.Errorf("order wrong: %v", w.events)
	}
	if want := mpreflight.SnapshotNeed(2 << 30); w.lastOptions.DiskNeed != want || !w.lastOptions.RequireAppStopped {
		t.Errorf("options = %+v, want need %d", w.lastOptions, want)
	}
	p := w.osParams
	if p.HostName != "vm" || p.Directory != "/opt/rdm_opensearch_backups" || p.Snapshot.Name != "rdm-20261118-133546" || p.SourceName != "src" || p.IndexPrefix != "caltechauthors" || p.Owner.UID != 1000 || len(p.Existing) != 1 || p.Bucket != "opensearch-backups.library.caltech.edu" {
		t.Errorf("params = %+v", p)
	}
	if err := w.osAcquirer(context.Background(), hostExec{w}, p.Directory); err == nil {
		t.Log("the fake host returns no file count, so the acquirer is expected to refuse; the Put below is what matters")
	}
	if len(w.putCalls) == 0 || !strings.Contains(w.putCalls[0], ".tar -> ") {
		t.Errorf("puts = %v, want the packed tar", w.putCalls)
	}
}

func TestRestoreOpenSearch_TheDirectoryComesFromAMultipassRuleOrTheFlag(t *testing.T) {
	w, d := newWorld(t)
	w.osRules = []config.BackupDirectoryRule{{Pattern: "vm", Host: config.HostMultipass, Directory: "/data/os"}, {Pattern: "vm", Directory: "/aws/dir"}}
	runW(t, d, "restore-opensearch", "-confirm", "vm", "src")
	if w.osParams.Directory != "/data/os" {
		t.Errorf("directory = %q", w.osParams.Directory)
	}
	w2, d2 := newWorld(t)
	runW(t, d2, "restore-opensearch", "-confirm", "-directory", "/x/y", "vm", "src")
	if w2.osParams.Directory != "/x/y" {
		t.Errorf("directory = %q", w2.osParams.Directory)
	}
}

func TestRestoreOpenSearch_ARefusedPreflightStopsBeforeTheLoadAndTheSnapshotFirstOrderHolds(t *testing.T) {
	w, d := newWorld(t)
	w.preflightOK = false
	code, _, _ := runW(t, d, "restore-opensearch", "-confirm", "vm", "src")
	if code != 1 || w.indexOf("restore-opensearch") >= 0 {
		t.Errorf("code=%d events=%v", code, w.events)
	}
	w2, d2 := newWorld(t)
	runW(t, d2, "restore-opensearch", "-confirm", "-snapshot-first", "vm", "src")
	stop, snap, start, pre, load := w2.indexOf("mp stop vm"), w2.indexOf("mp snapshot vm --name before-restore-opensearch"), w2.indexOf("mp start vm"), w2.indexOf("preflight"), w2.indexOf("restore-opensearch")
	if !(stop >= 0 && stop < snap && snap < start && start < pre && pre < load) {
		t.Errorf("order wrong: %v", w2.events)
	}
}

func TestRestoreOpenSearch_APlanFailureStopsEverythingBeforeADownload(t *testing.T) {
	w, d := newWorld(t)
	d.backups.planOpenSearch = func(ctx context.Context, ex executor.Executor, prefix string) (workflow.OpenSearchRestorePlan, error) {
		return workflow.OpenSearchRestorePlan{}, errors.New("the service user cannot own the repository")
	}
	code, _, errOut := runW(t, d, "restore-opensearch", "-confirm", "vm", "src")
	if code == 0 || w.indexOf("fetch-") >= 0 || !strings.Contains(errOut, "cannot own") {
		t.Errorf("code=%d events=%v err=%s", code, w.events, errOut)
	}
}

// ---- after a load ----

func TestShowUserData_ListsTheLocalFilesAndWhatOnTheVMHoldsData(t *testing.T) {
	w, d := newWorld(t)
	if err := os.WriteFile(filepath.Join(w.cache, "d.sql.gz"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runW(t, d, "show-user-data", "vm")
	if code != 0 || !strings.Contains(out, "d.sql.gz") || !strings.Contains(strings.ToLower(out), "database") || !strings.Contains(out, "snapshots") {
		t.Errorf("code=%d out=%s", code, out)
	}
	code, out, _ = runW(t, d, "show-user-data", "-json", "vm")
	var got struct {
		Local []mpreflight.LocalFile `json:"local"`
		VM    []string               `json:"vm"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Local) != 1 || len(got.VM) == 0 {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestRemoveFetchedBackups_IsADryRunUntilConfirmed(t *testing.T) {
	w, d := newWorld(t)
	p := filepath.Join(w.cache, "d.sql.gz")
	os.WriteFile(p, []byte("x"), 0o600)
	code, out, _ := runW(t, d, "remove-fetched-backups")
	if _, err := os.Stat(p); code != 0 || err != nil || !strings.Contains(strings.ToLower(out), "dry run") || !strings.Contains(out, "d.sql.gz") {
		t.Errorf("code=%d err=%v out=%s", code, err, out)
	}
	code, _, _ = runW(t, d, "remove-fetched-backups", "-confirm")
	if _, err := os.Stat(p); code != 0 || err == nil {
		t.Errorf("code=%d err=%v: the file should be gone", code, err)
	}
	if _, err := os.Stat(w.cache); err != nil {
		t.Errorf("the cache directory itself stays: %v", err)
	}
}

// ---- check-index-health ----

func TestCheckIndexHealth_HealthyIsExit0AndAProblemIsExit1(t *testing.T) {
	w, d := newWorld(t)
	if code, _, _ := runW(t, d, "check-index-health", "vm"); code != 0 || w.indexOf("index-health vm") < 0 {
		t.Errorf("healthy: code=%d events=%v", code, w.events)
	}
	w.health = workflow.IndexHealthReport{Healthy: false, Problems: []string{"the audit log index is plain"}}
	code, out, _ := runW(t, d, "check-index-health", "vm")
	if code != 1 || !strings.Contains(out, "the audit log index is plain") {
		t.Errorf("unhealthy: code=%d out=%s", code, out)
	}
}

func TestBackupVerbsWithoutTheirToolsConfiguredSayWhat(t *testing.T) {
	var o, e bytes.Buffer
	code := run([]string{"list-backups", "src"}, &o, &e, deps{client: multipass.NewWithRunner(func(ctx context.Context, a ...string) (string, error) { return "", nil })})
	if code == 0 || !strings.Contains(e.String(), "not configured") {
		t.Errorf("code=%d err=%s", code, e.String())
	}
}

// ---- gaps the mutation pass found ----

func TestRestores_SetFlagsDoNothingInADryRunAndThePreflightsLookInsideContainers(t *testing.T) {
	for _, verb := range []string{"restore-sql", "restore-opensearch"} {
		w, d := newWorld(t)
		runW(t, d, verb, "-set-mail-suppression", "-set-datacite-safeguards", "vm", "src")
		if w.lastOptions.SetMailSuppression || w.lastOptions.SetDataCiteSafeguards {
			t.Errorf("%s: a dry run must not change the VM: %+v", verb, w.lastOptions)
		}
		if !w.lastOptions.Docker {
			t.Errorf("%s: containers' environments must be checked: %+v", verb, w.lastOptions)
		}
	}
	w, d := newWorld(t)
	runW(t, d, "restore-opensearch", "-confirm", "-set-mail-suppression", "-set-datacite-safeguards", "vm", "src")
	if !w.lastOptions.SetMailSuppression || !w.lastOptions.SetDataCiteSafeguards {
		t.Errorf("confirmed: %+v", w.lastOptions)
	}
}

func TestRestoreSQL_TheDiskNeedOnAConfirmedRunComesFromTheFetchedFile(t *testing.T) {
	w, d := newWorld(t)
	w.dumpReport.Size = 100 << 20
	runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if want := mpreflight.DumpNeed(100<<20, true); w.lastOptions.DiskNeed != want {
		t.Errorf("DiskNeed = %d, want %d", w.lastOptions.DiskNeed, want)
	}
}

func TestRestores_TheProjectDefaultsDropTheVersionSuffixOfTheSource(t *testing.T) {
	w, d := newWorld(t)
	w.dumps = []rdmfetch.Dump{{Key: "caltechauthors-v13/some-other-name.sql.gz", Size: 1 << 20, LastModified: time.Now()}}
	runW(t, d, "restore-sql", "-confirm", "vm", "caltechauthors-v13")
	if p := w.restoreSQLParams; p.DBName != "caltechauthors" || p.DBUser != "caltechauthors" {
		t.Errorf("params = %+v", p)
	}
	w2, d2 := newWorld(t)
	runW(t, d2, "restore-opensearch", "-confirm", "vm", "caltechauthors-v13")
	if w2.osParams.IndexPrefix != "caltechauthors" {
		t.Errorf("index prefix = %q", w2.osParams.IndexPrefix)
	}
}

func TestRestoreSQL_AContainerThatCannotBeFoundIsFineInADryRunAndAnErrorWhenConfirmed(t *testing.T) {
	w, d := newWorld(t)
	d.backups.discoverPostgres = func(ctx context.Context, ex executor.Executor) (string, error) {
		return "", errors.New("no running Postgres container")
	}
	if code, _, _ := runW(t, d, "restore-sql", "vm", "src"); code != 0 {
		t.Errorf("dry run: code=%d", code)
	}
	code, _, errOut := runW(t, d, "restore-sql", "-confirm", "vm", "src")
	if code == 0 || !strings.Contains(errOut, "-container") || w.indexOf("restore-sql") >= 0 {
		t.Errorf("confirmed: code=%d err=%s events=%v", code, errOut, w.events)
	}
}

func TestRestoreOpenSearch_TheAcquirerChecksTheFileCountOfTheFetchedSnapshot(t *testing.T) {
	w, d := newWorld(t)
	w.snapReport.Objects = 7
	runW(t, d, "restore-opensearch", "-confirm", "vm", "src")
	w.hostStdout = "clasm-extracted 7\n"
	if err := w.osAcquirer(context.Background(), hostExec{w}, w.osParams.Directory); err != nil {
		t.Errorf("the count S3 listed is what the acquirer expects: %v", err)
	}
	w.hostStdout = "clasm-extracted 6\n"
	if err := w.osAcquirer(context.Background(), hostExec{w}, w.osParams.Directory); err == nil {
		t.Error("a different count must be refused")
	}
}

func TestRestoreOpenSearch_AHostWhoseOpenSearchCannotBeReachedIsExit69(t *testing.T) {
	_, d := newWorld(t)
	d.backups.planOpenSearch = func(ctx context.Context, ex executor.Executor, prefix string) (workflow.OpenSearchRestorePlan, error) {
		return workflow.OpenSearchRestorePlan{}, errors.New("listing existing indices on vm failed (status: Failed)")
	}
	code, _, errOut := runW(t, d, "restore-opensearch", "vm", "src")
	if code != 69 || !strings.Contains(errOut, "listing existing indices") {
		t.Errorf("code=%d err=%s", code, errOut)
	}
}

func TestARefusalMessageNamesTheFailedChecksAndLeavesTheDetailToTheFindings(t *testing.T) {
	w, d := newWorld(t)
	w.preflightOK = false
	w.findings = []mpreflight.Finding{
		{Check: "mail suppression", OK: false, Detail: "no .env file found\nsecond line of detail"},
		{Check: "disk space", OK: true, Detail: "fine"},
		{Check: "no production credentials", OK: false, Detail: "x: INVENIO_S3_ACCESS_KEY_ID"},
	}
	code, out, errOut := runW(t, d, "run-preflights", "vm")
	if code != 1 || !strings.Contains(errOut, "mail suppression") || !strings.Contains(errOut, "no production credentials") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if strings.Contains(errOut, "second line of detail") || strings.Contains(errOut, "disk space") || strings.Contains(errOut, "\n"+"second") {
		t.Errorf("stderr should name the failed checks only, not repeat the detail:\n%s", errOut)
	}
	if !strings.Contains(out, "second line of detail") {
		t.Errorf("the detail belongs on stdout:\n%s", out)
	}
}
