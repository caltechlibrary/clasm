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

// sqlBackupDirMode is the mode of the SQL backup directory (DR-0176 decision
// 2): the service user reads and writes, its group reads, nobody else does.
// Nothing needs group write -- the dump runs inside the Postgres container
// via `docker exec` and the file's owner is whoever runs it -- and the old
// root:www-data 0770 was historical residue, not a requirement.
const sqlBackupDirMode = "0750"

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
	// `exec 2>&1` folds stderr into stdout, the only stream SSM returns, so a
	// failing docker exec or pg_dump says why (DR-0178). It does not reach the
	// dump: only docker's stdout is redirected to the file below, and stderr
	// was already pointed at the command's own output before that redirect.
	return fmt.Sprintf("set -e; exec 2>&1; docker exec %s pg_dump --username=%s --column-inserts %s > %s; gzip -f %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote(dbName), shellQuote(rawFile), shellQuote(rawFile))
}

// RunSQLBackup runs the full Generate SQL Backup workflow (DESIGN.md,
// "Run SQL Backup"): pick an instance, CheckAWSCLIAvailable, prompt for
// the backup directory (same recall/pattern-match as Archive SQL
// Backup's own directory step, via the same hist), discover-and-reconcile
// the instance's live Postgres container/DB identity
// (resolveRDMPostgresConfig), run pg_dump directly via SSM, then return
// -- it generates the local dump and nothing more (DECISIONS.md, "Run
// SQL Backup: drop the Archive-SQL auto-chain, rename to 'Generate SQL
// Backup'"; PLAN.md Phase 20.54). Archive SQL Backup to S3 remains a
// separate menu entry for an operator who wants to archive right after.
func RunSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, backupDirRules []config.BackupDirectoryRule, rdmPostgresRules []config.RDMPostgresRule, hist BackupHistory, saveRDMPostgresRules func([]config.RDMPostgresRule) error, report ...func(SQLBackupParams)) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstanceDefaulted(ctx, "Select an instance", "Connects to this instance via SSM to run pg_dump directly -- no pre-installed backup script needed.", instances, hist.LastInstanceID)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return runSQLBackup(ctx, w, ssmClients, inst, backupDirRules, rdmPostgresRules, hist, saveRDMPostgresRules, nil, nil, report...)
}

// runSQLBackup is RunSQLBackup's testable core, once an instance is
// resolved -- input/output are nil in production and supplied by tests
// to drive every prompt through its accessible-mode pipe path instead.
func runSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, inst inventory.Instance, backupDirRules []config.BackupDirectoryRule, rdmPostgresRules []config.RDMPostgresRule, hist BackupHistory, saveRDMPostgresRules func([]config.RDMPostgresRule) error, input io.Reader, output io.Writer, report ...func(SQLBackupParams)) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	// Docker, not the AWS CLI: nothing this workflow does touches S3 (DR-0178).
	// Still the first SSM call, before any prompt.
	if err := CheckDockerAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
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

	// Reported once the directory is known, whatever happens afterward, so main
	// can print the pastable command (PLAN.md Phase 20.64 item 5). A run that
	// failed before this point has nothing worth reproducing.
	for _, r := range report {
		r(SQLBackupParams{InstanceID: inst.InstanceID, Directory: directory})
	}
	return executeSQLBackup(ctx, w, ssmClient, inst, directory, rdmPostgresRules, saveRDMPostgresRules)
}

// SQLBackupParams are the resolved arguments of a Generate SQL Backup run: the
// whole of what the interactive form collects.
type SQLBackupParams struct {
	InstanceID string
	Directory  string
}

// RunSQLBackupAuto is the non-interactive Generate SQL Backup form (DR-0177):
// the interactive run minus the directory prompt and the recall history. The
// Docker check still comes first, then the same steps in the same order as
// runSQLBackup. There is no confirmation, like the archive forms: it writes a
// dump on the instance and changes nothing else, and is meant for cron.
func RunSQLBackupAuto(ctx context.Context, w io.Writer, ssmClient awsclient.SSMAPI, inst inventory.Instance, p SQLBackupParams, rdmPostgresRules []config.RDMPostgresRule, saveRDMPostgresRules func([]config.RDMPostgresRule) error) error {
	if err := CheckDockerAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	return executeSQLBackup(ctx, w, ssmClient, inst, p.Directory, rdmPostgresRules, saveRDMPostgresRules)
}

// executeSQLBackup is everything after the directory is known, shared by the
// interactive and non-interactive forms: discover the Postgres identity, make
// the directory the service user's, dump, and hand the dump over.
func executeSQLBackup(ctx context.Context, w io.Writer, ssmClient awsclient.SSMAPI, inst inventory.Instance, directory string, rdmPostgresRules []config.RDMPostgresRule, saveRDMPostgresRules func([]config.RDMPostgresRule) error) error {
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

	// Ownership (DR-0176). SSM runs as root, so left alone the dump is a
	// root-owned file in a directory somebody made by hand: the ubuntu cron
	// script's own same-day redirect onto it is then refused. So the
	// directory is made or repaired for the service user first -- after the
	// prompts and discovery above, so a lookup failure costs nothing -- and
	// handed over again once the dump exists, which also repairs any
	// root-owned dumps an older clasm left. A failed dump chowns nothing.
	owner, err := ResolveServiceOwner(ctx, ssmClient, inst.InstanceID, DefaultOwnershipTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if err := EnsureBackupDirectory(ctx, ssmClient, inst.InstanceID, directory, owner, sqlBackupDirMode, DefaultOwnershipTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	command := buildSQLDumpCommand(containerName, dbName, dbUser, directory, time.Now().Format("2006-01-02"))
	dumpOut, status, err := RunShellCommand(ctx, ssmClient, inst.InstanceID, command, DefaultSQLDumpTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("SQL dump failed on %s", inst.InstanceID), status, dumpOut)
	}
	if err := ChownBackupDirectory(ctx, ssmClient, inst.InstanceID, directory, owner, DefaultOwnershipTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	fmt.Fprintf(w, "SQL backup created in %s on %s.\n", directory, inst.InstanceID)
	return nil
}
