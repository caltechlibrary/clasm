package workflow

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// DefaultSQLRestoreTimeout bounds the download/decompress and load SSM
// commands -- both can legitimately take a while for a large dump,
// mirroring DefaultBackupUploadTimeout's 30-minute bound.
const DefaultSQLRestoreTimeout = 30 * time.Minute

// buildDownloadAndDecompressCommand downloads bucket/key to destPath via
// the target's own aws CLI/credentials, then decompresses it in place --
// one shell script, two steps joined by `set -e` (matches
// buildSQLDumpCommand's own established shape, run_sql_backup.go), so a
// failed download aborts before gunzip ever runs on a partial or missing
// file.
func buildDownloadAndDecompressCommand(bucket, key, destPath string) string {
	src := fmt.Sprintf("s3://%s/%s", bucket, key)
	return fmt.Sprintf("set -e; aws s3 cp --only-show-errors %s %s; gunzip -f %s", shellQuote(src), shellQuote(destPath), shellQuote(destPath))
}

// downloadAndDecompressSQLBackup runs buildDownloadAndDecompressCommand
// via SSM, downloading key from bucket to a fixed /tmp scratch path on
// instanceID (named after the object's own basename, with any source-
// instance S3 prefix stripped) and decompressing it there. Returns the
// resulting plain .sql file's path.
func downloadAndDecompressSQLBackup(ctx context.Context, client awsclient.SSMAPI, instanceID, bucket, key string, timeout, pollInterval time.Duration) (string, error) {
	destPath := "/tmp/" + path.Base(key)
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDownloadAndDecompressCommand(bucket, key, destPath), timeout, pollInterval)
	if err != nil {
		return "", err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return "", curlFailureError(fmt.Sprintf("downloading/decompressing s3://%s/%s on %s failed", bucket, key, instanceID), status, stdout)
	}
	return strings.TrimSuffix(destPath, ".gz"), nil
}

// quoteSQLIdentifier double-quotes name per Postgres quoted-identifier
// syntax (doubling any embedded double quote), so the DROP/CREATE
// DATABASE statements below work correctly for any dbName -- including
// one containing a hyphen (e.g. "rdm14-granian", a real case hit
// 2026-08-18 setting up a restore-test instance), which isn't a valid
// *unquoted* Postgres identifier at all (an unquoted hyphen parses as
// subtraction, not part of the name). invenio-sql-backup.bash/
// invenio-sql-restore.bash's own real DROP/CREATE statements are
// unquoted, since every production instance's dbName so far has been a
// plain lowercase word -- quoting here is a safe superset (changes
// nothing observable for a simple name) rather than a literal
// reproduction of that specific gap (DECISIONS.md, "Restore SQL Backup:
// quote the database name as a SQL identifier, not just shell-quote
// it").
func quoteSQLIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// sqlStringLiteral single-quotes s per SQL string-literal syntax
// (doubling any embedded single quote) -- used for
// detectExistingSQLData's datname comparison, a value position, not an
// identifier.
func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// buildDetectExistingSQLDataCommand builds the docker-exec-wrapped psql
// command that checks whether dbName already exists on containerName --
// matching invenio-sql-restore.bash's own DROP DATABASE IF EXISTS
// precondition (DESIGN.md, "Restore SQL Backup from S3").
func buildDetectExistingSQLDataCommand(containerName, dbName, dbUser string) string {
	sqlStmt := fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname=%s", sqlStringLiteral(dbName))
	return fmt.Sprintf("docker exec %s psql --username=%s --dbname postgres -tAc %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote(sqlStmt))
}

// detectExistingSQLData runs buildDetectExistingSQLDataCommand via SSM
// and reports whether dbName already exists on containerName -- any
// non-empty output (psql's -tAc prints a bare "1" per matching row, no
// header/formatting) means it does.
func detectExistingSQLData(ctx context.Context, client awsclient.SSMAPI, instanceID, containerName, dbName, dbUser string, timeout, pollInterval time.Duration) (bool, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDetectExistingSQLDataCommand(containerName, dbName, dbUser), timeout, pollInterval)
	if err != nil {
		return false, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return false, curlFailureError(fmt.Sprintf("checking for an existing database %q on %s failed", dbName, instanceID), status, stdout)
	}
	return strings.TrimSpace(stdout) != "", nil
}

// buildRestoreSQLCommands returns invenio-sql-restore.bash's own real
// three-step sequence -- drop, create, load -- each its own
// docker-exec-wrapped command (DECISIONS.md, "SQL restore load command:
// grounded in the real invenio-sql-backup.bash/invenio-sql-restore.bash,
// not guessed"). Run as three separate SSM round trips, not joined by
// "&&", matching this project's established pattern for other
// multi-step remote sequences (OpenSearch's register/create/poll/delete)
// -- a failure is then reported against the exact step it happened at.
// dbName is SQL-identifier-quoted in the DROP/CREATE statements
// (quoteSQLIdentifier); in the load command it's a plain psql
// connection-target argument, not SQL syntax, so it's only shell-quoted.
func buildRestoreSQLCommands(containerName, dbName, dbUser, sqlFilePath string) (dropCmd, createCmd, loadCmd string) {
	quotedDB := quoteSQLIdentifier(dbName)
	dropCmd = fmt.Sprintf("docker exec %s psql --username=%s --dbname postgres -c %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote("DROP DATABASE IF EXISTS "+quotedDB))
	createCmd = fmt.Sprintf("docker exec %s psql --username=%s --dbname postgres -c %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote("CREATE DATABASE "+quotedDB))
	loadCmd = fmt.Sprintf("docker exec -i %s psql --username=%s %s < %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote(dbName), shellQuote(sqlFilePath))
	return dropCmd, createCmd, loadCmd
}

// runRestoreSQLCommands issues buildRestoreSQLCommands' three commands
// in sequence via SSM, stopping at (and naming) the first one that
// fails.
func runRestoreSQLCommands(ctx context.Context, client awsclient.SSMAPI, instanceID, containerName, dbName, dbUser, sqlFilePath string, timeout, pollInterval time.Duration) error {
	dropCmd, createCmd, loadCmd := buildRestoreSQLCommands(containerName, dbName, dbUser, sqlFilePath)
	for _, step := range []struct{ name, command string }{
		{"dropping the existing database", dropCmd},
		{"creating the database", createCmd},
		{"loading the SQL backup", loadCmd},
	} {
		stdout, status, err := RunShellCommand(ctx, client, instanceID, step.command, timeout, pollInterval)
		if err != nil {
			return err
		}
		if status != ssmtypes.CommandInvocationStatusSuccess {
			return curlFailureError(fmt.Sprintf("%s on %s failed", step.name, instanceID), status, stdout)
		}
	}
	return nil
}

// buildRestoreVerificationCommand builds the docker-exec-wrapped psql
// command countRestoredTables uses to sanity-check a restore actually
// populated something, rather than silently declaring success against
// an empty database.
func buildRestoreVerificationCommand(containerName, dbUser, dbName string) string {
	sqlStmt := "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'"
	return fmt.Sprintf("docker exec %s psql --username=%s %s -tAc %s",
		shellQuote(containerName), shellQuote(dbUser), shellQuote(dbName), shellQuote(sqlStmt))
}

// countRestoredTables runs buildRestoreVerificationCommand via SSM and
// parses the resulting table count.
func countRestoredTables(ctx context.Context, client awsclient.SSMAPI, instanceID, containerName, dbUser, dbName string, timeout, pollInterval time.Duration) (int, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildRestoreVerificationCommand(containerName, dbUser, dbName), timeout, pollInterval)
	if err != nil {
		return 0, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return 0, curlFailureError(fmt.Sprintf("verifying the restored database on %s failed", instanceID), status, stdout)
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return 0, fmt.Errorf("parsing restored table count %q on %s: %w", stdout, instanceID, convErr)
	}
	return n, nil
}

// RestoreSQLBackup runs the full Restore SQL Backup from S3 workflow
// (DESIGN.md, "Restore SQL Backup from S3"; PLAN.md Phase 20.50): pick a
// target instance, then delegate to the testable core. No recall/
// default-cursor history, unlike Run SQL Backup/Archive SQL -- restoring
// is a rare, deliberate action, not a routine one worth pre-positioning.
func RestoreSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance, rdmPostgresRules []config.RDMPostgresRule, saveRDMPostgresRules func([]config.RDMPostgresRule) error) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select the target instance to restore into", "Connects to this instance via SSM to load a SQL backup into its Postgres container. This overwrites the target database if it already exists.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return restoreSQLBackup(ctx, w, ssmClients, s3Client, newS3Client, inst, rdmPostgresRules, saveRDMPostgresRules, nil, nil)
}

// restoreSQLBackup is RestoreSQLBackup's testable core, once a target
// instance is resolved -- input/output are nil in production and
// supplied by tests to drive every prompt/confirm in this function
// through its accessible-mode pipe path instead.
//
// Step order is deliberately not the same order PLAN.md Phase 20.50's
// work-item list originally sketched: Postgres-target discovery
// (resolveRDMPostgresConfig) runs right after the AWS-CLI preflight,
// before any bucket/source-name/object-pick prompt -- not after, as the
// work items enumerated it. A broken target (no running Postgres
// container, or more than one) is fatal to the whole restore regardless
// of which S3 object gets picked, so failing fast here avoids making the
// operator pick a bucket and a multi-gigabyte object first only to hit
// the same failure right before the load step (DECISIONS.md, "Restore
// SQL Backup: resolve the Postgres target before any S3 prompt, not
// after").
func restoreSQLBackup(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, rdmPostgresRules []config.RDMPostgresRule, saveRDMPostgresRules func([]config.RDMPostgresRule) error, input io.Reader, output io.Writer) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	// fallbackIdentifier prefers the instance's Project tag over its Name
	// tag, same reasoning as Run SQL Backup (DECISIONS.md, "Default
	// db_name/db_user to the instance's Project tag, not its Name tag").
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
	fmt.Fprintf(w, "Restoring into Postgres container %q, database %q, user %q.\n", containerName, dbName, dbUser)

	bucket, err := promptBackupBucketFunc(ctx, w, s3Client, newS3Client, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	bucketRegion, err := BucketRegion(ctx, s3Client, bucket)
	if err != nil {
		return err
	}
	bucketClient, err := newS3Client(ctx, bucketRegion)
	if err != nil {
		return err
	}
	if err := CheckS3BucketAccess(ctx, bucketClient, bucket); err != nil {
		return err
	}

	// Defaults to the target's own Name -- the common case is restoring
	// an instance's own most recent backup -- but stays editable, since
	// a cross-instance restore (e.g. onto a fresh clone under a
	// different name) needs a different source prefix.
	sourceName, err := ui.Prompt("Source instance name (the S3 prefix to restore from)", ui.WithDefault(inst.Name), ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}

	objects, err := ListObjectsByPrefix(ctx, bucketClient, bucket, sourceName+"/")
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		fmt.Fprintf(w, "No SQL backups found under s3://%s/%s/.\n", bucket, sourceName)
		return nil
	}
	object, err := pickS3Object(w, "Select a SQL backup to restore", "Most recent first.", objects, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	exists, err := detectExistingSQLData(ctx, ssmClient, inst.InstanceID, containerName, dbName, dbUser, DefaultRDMPostgresDiscoveryTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if exists {
		ok, err := ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Cancelled.")
			return nil
		}
	}

	sqlFilePath, err := downloadAndDecompressSQLBackup(ctx, ssmClient, inst.InstanceID, bucket, object.Key, DefaultSQLRestoreTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	if err := runRestoreSQLCommands(ctx, ssmClient, inst.InstanceID, containerName, dbName, dbUser, sqlFilePath, DefaultSQLRestoreTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	tableCount, err := countRestoredTables(ctx, ssmClient, inst.InstanceID, containerName, dbUser, dbName, DefaultRDMPostgresDiscoveryTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\nRestored %q from s3://%s/%s into database %q on %s -- %d table(s) present.\n", object.Key, bucket, object.Key, dbName, inst.InstanceID, tableCount)
	return nil
}
