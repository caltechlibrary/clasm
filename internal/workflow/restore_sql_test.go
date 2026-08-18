package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestBuildDownloadAndDecompressCommand_ExactShape(t *testing.T) {
	got := buildDownloadAndDecompressCommand("sql-backups.library.caltech.edu", "new-data/caltechdata-db-1-caltechdata-2026-08-18.sql.gz")
	for _, want := range []string{"set -e", "aws s3 cp --only-show-errors", "s3://sql-backups.library.caltech.edu/new-data/caltechdata-db-1-caltechdata-2026-08-18.sql.gz", "gunzip -c", "2>&1"} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

// TestBuildDownloadAndDecompressCommand_GzKeyIncludesGunzip confirms a
// ".gz"-suffixed key still gets decompressed.
func TestBuildDownloadAndDecompressCommand_GzKeyIncludesGunzip(t *testing.T) {
	got := buildDownloadAndDecompressCommand("my-bucket", "new-data/backup.sql.gz")
	if !strings.Contains(got, "gunzip -c") {
		t.Errorf("command = %q, want it to include gunzip", got)
	}
}

// TestBuildDownloadAndDecompressCommand_NonGzKeySkipsGunzip is a
// regression test for a real incident (2026-08-18, restoring
// CaltechDATA production's own real SQL backup): the archived object's
// key is "...2026-08-16.sql" -- no ".gz" -- and, once the stderr-
// visibility fix (above) actually surfaced the real failure, confirmed
// live to genuinely be plain, uncompressed SQL text ("gzip: ...: not in
// gzip format"), not gzip content under a misleading name as an earlier
// version of this function assumed. Every backup clasm itself ever
// produces is unconditionally gzip'd (`buildSQLDumpCommand`) and named
// accordingly (`....sql.gz`), so the key's own suffix is a reliable
// signal either way -- for anything not ending in ".gz", skip
// decompression entirely rather than running gunzip against a file that
// was never compressed to begin with.
func TestBuildDownloadAndDecompressCommand_NonGzKeySkipsGunzip(t *testing.T) {
	got := buildDownloadAndDecompressCommand("sql-backups.library.caltech.edu", "new-data/caltechdata-db-1-caltechdata-2026-08-16.sql")
	if strings.Contains(got, "gunzip") {
		t.Errorf("command = %q, did not expect a gunzip step for a non-.gz key", got)
	}
	if !strings.Contains(got, "aws s3 cp --only-show-errors") || !strings.Contains(got, "2>&1") {
		t.Errorf("command = %q, missing an expected element", got)
	}
}

func TestDownloadAndDecompressSQLBackup_Success(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	got, err := downloadAndDecompressSQLBackup(context.Background(), fake, "i-1", "my-bucket", "new-data/backup.sql.gz", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != remoteRestoreSQLPath {
		t.Errorf("got %q, want the decompressed scratch path %q", got, remoteRestoreSQLPath)
	}
}

// TestDownloadAndDecompressSQLBackup_NonGzKeyReturnsRawDownloadPath is
// the same regression as TestBuildDownloadAndDecompressCommand_NonGzKeySkipsGunzip,
// one level up: a key without ".gz" resolves to the raw downloaded
// path directly, not the (never-created, in this case) decompressed one.
func TestDownloadAndDecompressSQLBackup_NonGzKeyReturnsRawDownloadPath(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	got, err := downloadAndDecompressSQLBackup(context.Background(), fake, "i-1", "sql-backups.library.caltech.edu", "new-data/caltechdata-db-1-caltechdata-2026-08-16.sql", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != remoteRestoreDownloadPath {
		t.Errorf("got %q, want the raw download path %q", got, remoteRestoreDownloadPath)
	}
}

func TestDownloadAndDecompressSQLBackup_SSMFailurePropagatesBody(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "download failed: NoSuchKey"}
	_, err := downloadAndDecompressSQLBackup(context.Background(), fake, "i-1", "my-bucket", "new-data/backup.sql.gz", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "NoSuchKey") {
		t.Errorf("expected the error to include the remote failure body, got: %v", err)
	}
}

// TestDownloadAndDecompressSQLBackup_SurfacesStderr is a regression test
// for the real incident's *second* gap: gunzip's "unknown suffix" message
// went to stderr, which the pre-fix command never captured at all (only
// stdout), so the operator-facing error showed just "(status: Failed)"
// with no explanation. The command now redirects stderr into the same
// stream RunShellCommand captures ("{ ...; } 2>&1"), so a real remote
// failure's actual message reaches the reported error.
func TestDownloadAndDecompressSQLBackup_SurfacesStderr(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "gzip: /var/tmp/clasm-sql-restore.download: unknown suffix -- ignored"}
	_, err := downloadAndDecompressSQLBackup(context.Background(), fake, "i-1", "my-bucket", "new-data/backup.sql", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unknown suffix") {
		t.Errorf("expected the error to include gunzip's own message, got: %v", err)
	}
}

func TestQuoteSQLIdentifier_SimpleName(t *testing.T) {
	if got := quoteSQLIdentifier("caltechauthors"); got != `"caltechauthors"` {
		t.Errorf("got %q, want %q", got, `"caltechauthors"`)
	}
}

// TestQuoteSQLIdentifier_HyphenatedNameAndEmbeddedDoubleQuote is a
// regression test grounded in a real case: `caltechdata-restore-test`'s
// own Postgres database is named "rdm14-granian" (hyphenated) --  not a
// valid *unquoted* Postgres identifier at all (an unquoted hyphen parses
// as subtraction). Also covers the doubling rule for an embedded double
// quote, per standard SQL quoted-identifier syntax.
func TestQuoteSQLIdentifier_HyphenatedNameAndEmbeddedDoubleQuote(t *testing.T) {
	if got := quoteSQLIdentifier("rdm14-granian"); got != `"rdm14-granian"` {
		t.Errorf("got %q, want %q", got, `"rdm14-granian"`)
	}
	if got := quoteSQLIdentifier(`weird"name`); got != `"weird""name"` {
		t.Errorf("got %q, want %q", got, `"weird""name"`)
	}
}

func TestSQLStringLiteral_EmbeddedSingleQuote(t *testing.T) {
	if got := sqlStringLiteral("o'brien"); got != `'o''brien'` {
		t.Errorf("got %q, want %q", got, `'o''brien'`)
	}
}

// TestBuildRestoreSQLCommands_ExactShape also confirms each command
// captures stderr into the same stream RunShellCommand returns ("2>&1")
// -- found real, live (2026-08-18): psql/gunzip failures land on
// stderr, which RunShellCommand never captures at all (only
// StandardOutputContent), so an un-redirected failure reports only
// "(status: Failed)" with no explanation. None of these three commands
// parse their own stdout for meaningful content on success (unlike
// detectExistingSQLData/countRestoredTables, which do and are
// deliberately left un-redirected to avoid a stray stderr notice
// corrupting that parsing), so redirecting here is pure upside.
func TestBuildRestoreSQLCommands_ExactShape(t *testing.T) {
	dropCmd, createCmd, loadCmd := buildRestoreSQLCommands("caltechauthors-db-1", "caltechauthors", "caltechauthors", "/tmp/backup.sql")
	if !strings.Contains(dropCmd, "docker exec") || !strings.Contains(dropCmd, "caltechauthors-db-1") || !strings.Contains(dropCmd, `DROP DATABASE IF EXISTS "caltechauthors"`) || !strings.Contains(dropCmd, "2>&1") {
		t.Errorf("dropCmd = %q, missing an expected element", dropCmd)
	}
	if !strings.Contains(createCmd, "docker exec") || !strings.Contains(createCmd, `CREATE DATABASE "caltechauthors"`) || !strings.Contains(createCmd, "2>&1") {
		t.Errorf("createCmd = %q, missing an expected element", createCmd)
	}
	if !strings.Contains(loadCmd, "docker exec -i") || !strings.Contains(loadCmd, "caltechauthors-db-1") || !strings.Contains(loadCmd, "< '/tmp/backup.sql'") || !strings.Contains(loadCmd, "2>&1") {
		t.Errorf("loadCmd = %q, missing an expected element", loadCmd)
	}
}

// TestBuildRestoreSQLCommands_HyphenatedDBNameIsQuotedAsIdentifier
// reproduces the real caltechdata-restore-test case (dbName
// "rdm14-granian") directly: the DROP/CREATE statements must quote the
// identifier, or "DROP DATABASE IF EXISTS rdm14-granian" is a SQL syntax
// error (an unquoted hyphen parses as subtraction). The load command's
// own dbName argument is a plain psql connection-target string, not SQL
// syntax, so it stays unquoted-at-the-SQL-level (shell-quoted only).
func TestBuildRestoreSQLCommands_HyphenatedDBNameIsQuotedAsIdentifier(t *testing.T) {
	dropCmd, createCmd, loadCmd := buildRestoreSQLCommands("rdm14-granian-db-1", "rdm14-granian", "rdm14-granian", "/tmp/backup.sql")
	if !strings.Contains(dropCmd, `DROP DATABASE IF EXISTS "rdm14-granian"`) {
		t.Errorf("dropCmd = %q, want a quoted identifier", dropCmd)
	}
	if !strings.Contains(createCmd, `CREATE DATABASE "rdm14-granian"`) {
		t.Errorf("createCmd = %q, want a quoted identifier", createCmd)
	}
	if !strings.Contains(loadCmd, "rdm14-granian") {
		t.Errorf("loadCmd = %q, missing the database name", loadCmd)
	}
}

func TestDetectExistingSQLData_ExistingDatabase(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "1\n"}
	got, err := detectExistingSQLData(context.Background(), fake, "i-1", "caltechauthors-db-1", "caltechauthors", "caltechauthors", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected an existing database to be detected")
	}
}

func TestDetectExistingSQLData_NoDatabase(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: ""}
	got, err := detectExistingSQLData(context.Background(), fake, "i-1", "caltechauthors-db-1", "caltechauthors", "caltechauthors", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("did not expect an existing database to be detected")
	}
}

func TestDetectExistingSQLData_SSMFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "boom"}
	_, err := detectExistingSQLData(context.Background(), fake, "i-1", "caltechauthors-db-1", "caltechauthors", "caltechauthors", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestCountRestoredTables_ParsesCount(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "42\n"}
	got, err := countRestoredTables(context.Background(), fake, "i-1", "caltechauthors-db-1", "caltechauthors", "caltechauthors", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestCountRestoredTables_SSMFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	_, err := countRestoredTables(context.Background(), fake, "i-1", "caltechauthors-db-1", "caltechauthors", "caltechauthors", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// restoreSQLFake builds a fakeSSMClient that distinguishes
// restoreSQLBackup's sequence of remote commands by substring, mirroring
// sqlBackupFake's own shape (run_sql_backup_test.go).
func restoreSQLFake(discoveryStdout, detectStdout string, downloadStatus, loadStatus types.CommandInvocationStatus) *fakeSSMClient {
	return &fakeSSMClient{
		commandID:   "cmd-1",
		finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", stdout: "/usr/bin/aws", status: types.CommandInvocationStatusSuccess},
			{substring: "docker ps", stdout: discoveryStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "pg_database", stdout: detectStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "aws s3 cp", status: downloadStatus},
			{substring: "DROP DATABASE", status: loadStatus},
			{substring: "CREATE DATABASE", status: loadStatus},
			{substring: "docker exec -i", status: loadStatus},
			{substring: "information_schema.tables", stdout: "7\n", status: types.CommandInvocationStatusSuccess},
		},
	}
}

func TestRestoreSQLBackup_NoBackupsFoundUnderPrefix(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" // bucket, source instance name
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No SQL backups found") {
		t.Errorf("expected a no-backups message, got:\n%s", buf.String())
	}
}

func TestRestoreSQLBackup_HappyPathNoExistingData(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n" // bucket, source name, pick the (only) object
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored") || !strings.Contains(buf.String(), "7 table") {
		t.Errorf("expected a success report including the table count, got:\n%s", buf.String())
	}
}

func TestRestoreSQLBackup_ExistingDataRequiresConfirmDestructive_DeclinedCancelsBeforeDownload(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n" + "wrong-name\n" // bucket, source name, pick object, decline the type-to-confirm
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "1\n", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancellation message, got:\n%s", buf.String())
	}
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "aws s3 cp") {
			t.Errorf("did not expect a download attempt after a declined confirmation, sent commands: %v", ssmClient.sentCommands)
		}
	}
}

func TestRestoreSQLBackup_ExistingDataConfirmedProceeds(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n" + "i-1\n" // bucket, source name, pick object, confirm with the instance ID
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "1\n", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored") {
		t.Errorf("expected a success report, got:\n%s", buf.String())
	}
}

func TestRestoreSQLBackup_DownloadFailureAbortsBeforeLoad(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "", types.CommandInvocationStatusFailed, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err == nil {
		t.Fatal("expected a download-failure error")
	}
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "DROP DATABASE") || strings.Contains(sent, "CREATE DATABASE") {
			t.Errorf("did not expect any load attempt after a download failure, sent commands: %v", ssmClient.sentCommands)
		}
	}
}

// TestRestoreSQLBackup_DiscoveryFailureAbortsBeforeAnyS3Activity is a
// regression test for the corrected step order (DECISIONS.md, "Restore
// SQL Backup: resolve the Postgres target before any S3 prompt, not
// after"): Postgres-container discovery runs right after the AWS-CLI
// preflight, before the bucket/source-name/object-pick prompts, so a
// broken target fails fast without first making the operator pick a
// bucket and an object to restore. No S3 call of any kind, and no
// bucket/source-name input consumed.
func TestRestoreSQLBackup_DiscoveryFailureAbortsBeforeAnyS3Activity(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor("") // no bucket/source-name input available at all
	ssmClient := restoreSQLFake("", "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if err == nil {
		t.Fatal("expected a discovery-failure error")
	}
	if len(s3Client.listObjectsV2Calls) != 0 {
		t.Errorf("expected zero S3 calls before the discovery failure, got: %+v", s3Client.listObjectsV2Calls)
	}
}

func TestRestoreSQLBackup_SavesRDMPostgresRulesWhenChanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors_db_1"}}

	var savedRules []config.RDMPostgresRule
	saveFn := func(rules []config.RDMPostgresRule) error { savedRules = rules; return nil }

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, existing, saveFn, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(savedRules) != 1 || savedRules[0].ContainerName != "caltechauthors-db-1" {
		t.Errorf("savedRules = %v, want ContainerName updated to %q", savedRules, "caltechauthors-db-1")
	}
}

func TestRestoreSQLBackup_DoesNotSaveRDMPostgresRulesWhenUnchanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "my-bucket\n" + "caltechauthors\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreSQLFake("postgres:14.13\tcaltechauthors-db-1\n", "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneSQLBackupObject("caltechauthors")}
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"}}

	saveCalls := 0
	saveFn := func(rules []config.RDMPostgresRule) error { saveCalls++; return nil }

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, existing, saveFn, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saveCalls != 0 {
		t.Errorf("saveCalls = %d, want 0 (nothing changed)", saveCalls)
	}
}

func TestRestoreSQLBackup_CLIUnavailableAbortsBeforeAnyPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor("")
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, sendCommandErr: errUnavailable}
	s3Client := &fakeS3Client{}

	err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, nil, le, buf)
	if !errors.Is(err, errUnavailable) {
		t.Fatalf("expected errUnavailable to propagate, got: %v", err)
	}
}

// oneSQLBackupObject returns a single fixture S3 object under
// sourceName's own prefix, matching UploadBackupFiles' own key
// convention (uploadKey: "<prefix>/<basename>").
func oneSQLBackupObject(sourceName string) []s3types.Object {
	key := sourceName + "/" + sourceName + "-db-1-" + sourceName + "-2026-08-18.sql.gz"
	return []s3types.Object{{Key: aws.String(key), Size: aws.Int64(1024), LastModified: aws.Time(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))}}
}
