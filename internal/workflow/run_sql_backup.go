package workflow

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// DefaultSQLDumpTimeout bounds the pg_dump SSM command -- long-running,
// mirrors DefaultBackupUploadTimeout's 30-minute bound.
const DefaultSQLDumpTimeout = 30 * time.Minute

// buildSQLDumpCommand builds the pg_dump command Run SQL Backup sends
// via SSM, matching invenio-sql-backup.bash's own command and filename
// convention exactly: pg_dump --column-inserts redirects to a plain
// <container>-<db>-<date>.sql file first, then `gzip -f` compresses it
// as a separate step second (producing the final ...sql.gz) -- NOT
// piped directly (`pg_dump | gzip`), which was a real bug (confirmed
// 2026-07-29 against CaltechAUTHORS production, DECISIONS.md, "Real
// bug: pg_dump | gzip masks pg_dump's own exit status"): a pipe's exit
// status is its last command's (gzip, which always succeeds even on
// empty/error input), silently hiding a failed pg_dump behind a
// falsely-reported Success. `set -e` (this project's own established
// pattern for a multi-step SSM command, ssm_grow.go's
// rootFilesystemGrowCommand) makes pg_dump's own failure abort before
// gzip ever runs. date is passed in (rather than computed here via a
// remote `$(date ...)` substitution, as the real script does) so this
// stays a pure, deterministic, directly testable function -- the
// resulting filename is identical either way.
func buildSQLDumpCommand(containerName, dbName, dbUser, directory, date string) string {
	rawFile := fmt.Sprintf("%s/%s-%s-%s.sql", directory, containerName, dbName, date)
	return fmt.Sprintf("set -e; docker exec %s pg_dump --username=%s --column-inserts %s > %s; gzip -f %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote(dbName), shellQuote(rawFile), shellQuote(rawFile))
}

// RunSQLBackup runs the full Run SQL Backup workflow (DESIGN.md, "Run
// SQL Backup"): pick an instance, CheckAWSCLIAvailable, prompt for the
// backup directory (same recall/pattern-match as Archive SQL Backup's
// own directory step, via the same hist), discover-and-reconcile the
// instance's live Postgres container/DB identity
// (resolveRDMPostgresConfig), run pg_dump directly via SSM, then offer
// to chain straight into archiveSQL (the same closure
// RDMBackupRestoreActions.ArchiveSQL already wraps BackupArchiveAndTrim
// in). archiveSQL may be nil (no chaining offered) for callers/tests
// that don't need it.
func RunSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, backupDirRules []config.BackupDirectoryRule, rdmPostgresRules []config.RDMPostgresRule, hist BackupHistory, saveRDMPostgresRules func([]config.RDMPostgresRule) error, archiveSQL func(ctx context.Context) error) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstanceDefaulted(ctx, "Select an instance", "Connects to this instance via SSM to run pg_dump directly -- no pre-installed backup script needed.", instances, hist.LastInstanceID)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return runSQLBackup(ctx, w, ssmClients, inst, backupDirRules, rdmPostgresRules, hist, saveRDMPostgresRules, archiveSQL, nil, nil)
}

// runSQLBackup is RunSQLBackup's testable core, once an instance is
// resolved -- input/output are nil in production and supplied by tests
// to drive every prompt/confirm in this function through its
// accessible-mode pipe path instead.
func runSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, inst inventory.Instance, backupDirRules []config.BackupDirectoryRule, rdmPostgresRules []config.RDMPostgresRule, hist BackupHistory, saveRDMPostgresRules func([]config.RDMPostgresRule) error, archiveSQL func(ctx context.Context) error, input io.Reader, output io.Writer) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	dirPromptOpts := []ui.PromptOption{ui.WithValidator(requireNonEmpty)}
	if def := hist.LastDirectoryByInstance[inst.InstanceID]; def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	} else if def := config.BackupDirectoryFor(backupDirRules, inst.Name); def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	}
	dirPromptOpts = append(dirPromptOpts, ui.WithIO(input, output))
	directory, err := ui.Prompt("Backup directory (e.g. /opt/rdm_sql_backups)", dirPromptOpts...)
	if err != nil {
		return err
	}
	if hist.Save != nil {
		if err := hist.Save(inst.InstanceID, directory); err != nil {
			fmt.Fprintf(w, "warning: could not save backup history: %v\n", err)
		}
	}

	// fallbackIdentifier prefers the instance's Project tag over its Name
	// tag for defaulting dbName/dbUser -- confirmed via a real incident
	// (2026-07-29) that an instance's Name tag can be a legacy label
	// unrelated to its actual RDM project shortname, while Project holds
	// the reliable value (DECISIONS.md, "Default db_name/db_user to the
	// instance's Project tag, not its Name tag"). Pattern matching itself
	// still uses inst.Name, same convention as config.BackupDirectoryFor.
	fallbackIdentifier := cmp.Or(inst.Project, inst.Name)
	containerName, dbName, dbUser, updatedRules, err := resolveRDMPostgresConfig(ctx, w, ssmClient, inst.InstanceID, inst.Name, fallbackIdentifier, rdmPostgresRules, DefaultRDMPostgresDiscoveryTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if saveRDMPostgresRules != nil && !slices.Equal(rdmPostgresRules, updatedRules) {
		if err := saveRDMPostgresRules(updatedRules); err != nil {
			fmt.Fprintf(w, "warning: could not save RDM Postgres config: %v\n", err)
		}
	}

	// Reported before running the dump, not just after -- so an operator
	// can catch a wrong resolution (e.g. the wrong database name) by eye
	// immediately, rather than only discovering it later by inspecting
	// the resulting file on disk (exactly how the 2026-07-29 incident was
	// first noticed).
	fmt.Fprintf(w, "Using Postgres container %q, database %q, user %q.\n", containerName, dbName, dbUser)

	command := buildSQLDumpCommand(containerName, dbName, dbUser, directory, time.Now().Format("2006-01-02"))
	_, status, err := RunShellCommand(ctx, ssmClient, inst.InstanceID, command, DefaultSQLDumpTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("SQL dump failed on %s (status: %s)", inst.InstanceID, status)
	}
	fmt.Fprintf(w, "SQL backup created in %s on %s.\n", directory, inst.InstanceID)

	if archiveSQL == nil {
		return nil
	}
	ok, err := Confirm("Continue to Archive SQL Backup to S3 now?", WithConfirmIO(input, output))
	if err != nil {
		return cancelledIsNil(w, err)
	}
	if !ok {
		return nil
	}
	return archiveSQL(ctx)
}
