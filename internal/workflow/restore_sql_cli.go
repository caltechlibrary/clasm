package workflow

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path"
	"slices"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Restore SQL Backup form (DR-0177, DR-0180), the second
// destructive leaf on the shared gate (cli_gate.go):
//
//	restore-sql-backup-from-s3 [--confirm <instance-id-or-name>]
//	    <instance> <bucket> <source> <backup-key-or-name-or-latest>
//
// The arguments are the interactive prompts in order, all explicit. It is a dry
// run unless --confirm names the target (or a terminal answers the prompt): it
// makes only read-only calls -- the Postgres discovery, whether the database
// exists, and the S3 listing -- prints a plan, and changes nothing.

// RestoreSQLParams are the resolved arguments of the form.
type RestoreSQLParams struct {
	Bucket string
	Source string
	Backup string // a full key, a file name, or "latest"
}

const restoreSQLUsage = "usage: " + RestoreSQLBackupCLISlug +
	" [--confirm <instance-id-or-name>] <instance> <bucket> <source> <backup-key-or-name-or-latest>"

// ParseSQLRestoreArgs parses the form's options and four positional arguments.
// Every problem is a *UsageError (exit 2) raised before AWS is touched; --help is
// a *HelpRequested. The instance is matched by Name tag first, then by ID.
func ParseSQLRestoreArgs(args []string, instances []inventory.Instance) (inventory.Instance, RestoreSQLParams, DestructiveOptions, error) {
	const leaf = RestoreSQLBackupCLISlug
	opts, pos, err := ParseDestructiveArgs(leaf, restoreSQLUsage, args)
	if err != nil {
		return inventory.Instance{}, RestoreSQLParams{}, DestructiveOptions{}, err
	}
	if len(pos) != 4 {
		return inventory.Instance{}, RestoreSQLParams{}, DestructiveOptions{},
			&UsageError{Msg: fmt.Sprintf("%s: want 4 arguments, got %d\n%s", leaf, len(pos), restoreSQLUsage)}
	}
	inst, err := resolveInstanceArg(pos[0], instances)
	if err != nil {
		return inventory.Instance{}, RestoreSQLParams{}, DestructiveOptions{}, &UsageError{Msg: fmt.Sprintf("%s: %v", leaf, err)}
	}
	p := RestoreSQLParams{Bucket: pos[1], Source: pos[2], Backup: pos[3]}
	for _, f := range []struct{ name, value string }{{"bucket", p.Bucket}, {"source", p.Source}, {"backup", p.Backup}} {
		if f.value == "" {
			return inventory.Instance{}, RestoreSQLParams{}, DestructiveOptions{},
				&UsageError{Msg: fmt.Sprintf("%s: %s must not be empty\n%s", leaf, f.name, restoreSQLUsage)}
		}
	}
	return inst, p, opts, nil
}

// RunRestoreSQLBackupAuto runs the non-interactive form. The caller has already
// done the preflight the interactive form does (the AWS CLI check and the
// bucket's region and access), so bucketClient is scoped to the bucket.
//
// Order: the --confirm value is checked first, before any call; then the
// read-only work that builds the plan; then the gate decides. Only a confirmed
// run goes on to executeSQLRestore -- the same steps in the same order as the
// interactive form, which downloads and decompresses the backup before it drops
// anything. The Postgres settings are saved to ~/.clasm only after the gate says
// proceed, so a dry run writes nothing anywhere, and the discovery's own "saved"
// message is not shown.
func RunRestoreSQLBackupAuto(ctx context.Context, w io.Writer, ssmClient awsclient.SSMAPI, bucketClient awsclient.S3API, inst inventory.Instance, rules []config.RDMPostgresRule, saveRules func([]config.RDMPostgresRule) error, p RestoreSQLParams, confirm string, interactive bool, input io.Reader, output io.Writer) error {
	gate := Gate{Target: inst, Confirm: confirm, Interactive: interactive}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}

	fallbackIdentifier := cmp.Or(inst.Project, inst.Name)
	containerName, dbName, dbUser, updatedRules, err := resolveRDMPostgresConfig(ctx, io.Discard, ssmClient, inst.InstanceID, inst.Name, fallbackIdentifier, rules, DefaultRDMPostgresDiscoveryTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	objects, err := ListObjectsByPrefix(ctx, bucketClient, p.Bucket, p.Source+"/")
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		return noSQLBackupsError(ctx, bucketClient, p.Bucket, p.Source, inst.Name)
	}
	object, err := chooseSQLBackup(objects, p)
	if err != nil {
		return err
	}

	exists, err := detectExistingSQLData(ctx, ssmClient, inst.InstanceID, containerName, dbName, dbUser, DefaultRDMPostgresDiscoveryTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	replace := fmt.Sprintf("no existing database %q in Postgres container %s (nothing to replace); it is created and loaded", dbName, containerName)
	action := fmt.Sprintf("create database %q", dbName)
	if exists {
		replace = fmt.Sprintf("replace the existing database %q in Postgres container %s: it is dropped, recreated and loaded", dbName, containerName)
		action = fmt.Sprintf("replace database %q", dbName)
	}
	gate.Summary = fmt.Sprintf("restore %s from s3://%s/%s onto %s (%s): %s",
		object.Key, p.Bucket, object.Key, inst.Name, inst.InstanceID, action)
	gate.Plan = []string{
		fmt.Sprintf("restore %s (%s, modified %s) from s3://%s/ onto %s (%s)",
			object.Key, humanBytes(object.SizeBytes), object.LastModified.Format("2006-01-02 15:04:05"), p.Bucket, inst.Name, inst.InstanceID),
		replace,
		"download and decompress the backup on the instance first; nothing is dropped until that has succeeded",
		"load it, then count the tables in the restored database",
	}
	decision, err := gate.Decide(w, input, output)
	if err != nil {
		return err
	}
	if decision != GateProceed {
		return nil
	}

	if saveRules != nil && !slices.Equal(rules, updatedRules) {
		if err := saveRules(updatedRules); err != nil {
			fmt.Fprintf(w, "warning: could not save RDM Postgres config: %v\n", err)
		}
	}
	return executeSQLRestore(ctx, w, ssmClient, sqlRestoreRun{
		inst: inst, containerName: containerName, dbName: dbName, dbUser: dbUser, bucket: p.Bucket, object: object,
	})
}

// chooseSQLBackup resolves the backup argument against objects (newest first):
// "latest" is the newest; otherwise the argument is matched against the full
// key, then against the key under the source prefix, then against the file name.
// An unknown name is an action failure that lists what exists, most recent first.
func chooseSQLBackup(objects []S3Object, p RestoreSQLParams) (S3Object, error) {
	if p.Backup == "latest" {
		return objects[0], nil
	}
	names := make([]string, 0, len(objects))
	for _, o := range objects {
		if o.Key == p.Backup || o.Key == p.Source+"/"+p.Backup || path.Base(o.Key) == p.Backup {
			return o, nil
		}
		names = append(names, path.Base(o.Key))
	}
	return S3Object{}, fmt.Errorf("backup %q not found under s3://%s/%s/; available, most recent first: %s",
		p.Backup, p.Bucket, p.Source, summarizeNames(names, 10))
}
