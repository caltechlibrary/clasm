package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Restore SQL Backup form (DR-0177, DR-0180), the second
// destructive leaf on the shared gate. Arguments, in the order of the
// interactive prompts:
//
//	restore-sql-backup-from-s3 [--confirm <instance-id-or-name>]
//	    <instance> <bucket> <source> <backup-key-or-name-or-latest>
//
// A dry run by default: it reads (the Postgres target, whether the database
// exists, the S3 listing) and prints a plan. Only --confirm, or a terminal's
// type-to-confirm, downloads the backup and replaces the database.

var restoreSQLInst = inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}

const restoreSQLDiscovery = "postgres:14.13\tcaltechdata-db-1\n"

func restoreSQLParams() RestoreSQLParams {
	return RestoreSQLParams{Bucket: "my-bucket", Source: "caltechdata", Backup: "latest"}
}

func sqlBackupObjects(source string, size int64, names ...string) []s3types.Object {
	var out []s3types.Object
	for i, n := range names {
		// later names are newer, so names are given oldest first
		out = append(out, s3types.Object{
			Key:          aws.String(source + "/" + n),
			Size:         aws.Int64(size),
			LastModified: aws.Time(time.Date(2026, 9, 1+i, 3, 0, 0, 0, time.UTC)),
		})
	}
	return out
}

func runRestoreSQLCLI(t *testing.T, detect string, objects []s3types.Object, p RestoreSQLParams, confirm string, interactive bool, typed string) (*fakeSSMClient, *fakeS3Client, string, error) {
	t.Helper()
	ssmClient := restoreSQLFake(restoreSQLDiscovery, detect, types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: objects}
	var w io.Writer
	var in io.Reader
	var buf *bytes.Buffer
	if interactive {
		w, in, buf = newPipeEditor(typed)
	} else {
		buf = &bytes.Buffer{}
		w, in = buf, failingReader{t}
	}
	err := RunRestoreSQLBackupAuto(context.Background(), w, ssmClient, s3Client, restoreSQLInst, nil, nil, p, confirm, interactive, in, buf)
	return ssmClient, s3Client, buf.String(), err
}

// --- argument parsing ---

func TestParseSQLRestoreArgs_FourArgumentsAndConfirm(t *testing.T) {
	inst, p, opts, err := ParseSQLRestoreArgs([]string{"--confirm", "i-1", "caltechdata", "my-bucket", "src", "latest"}, []inventory.Instance{restoreSQLInst})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.InstanceID != "i-1" || opts.Confirm != "i-1" {
		t.Errorf("got instance %q confirm %q", inst.InstanceID, opts.Confirm)
	}
	if want := (RestoreSQLParams{Bucket: "my-bucket", Source: "src", Backup: "latest"}); p != want {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestParseSQLRestoreArgs_UsageErrors(t *testing.T) {
	insts := []inventory.Instance{restoreSQLInst}
	cases := map[string][]string{
		"no arguments":     nil,
		"too few":          {"caltechdata", "b", "s"},
		"too many":         {"caltechdata", "b", "s", "latest", "x"},
		"unknown instance": {"nope", "b", "s", "latest"},
		"empty bucket":     {"caltechdata", "", "s", "latest"},
		"empty source":     {"caltechdata", "b", "", "latest"},
		"empty backup":     {"caltechdata", "b", "s", ""},
		"unknown option":   {"--force", "caltechdata", "b", "s", "latest"},
	}
	for name, args := range cases {
		_, _, _, err := ParseSQLRestoreArgs(args, insts)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: want a *UsageError, got: %v", name, err)
			continue
		}
		if !strings.Contains(err.Error(), RestoreSQLBackupCLISlug) {
			t.Errorf("%s: the error should name the leaf, got: %v", name, err)
		}
	}
	_, _, _, err := ParseSQLRestoreArgs([]string{"--help"}, insts)
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "--confirm") {
		t.Errorf("--help: want a *HelpRequested with the usage, got: %v", err)
	}
}

// --- the dry run ---

func TestRestoreSQLCLI_DryRunChangesNothingAndPrintsARealPlan(t *testing.T) {
	ssmClient, _, out, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 3<<20, "b-2026-09-01.sql.gz", "b-2026-09-02.sql.gz"), restoreSQLParams(), "", false, "")
	if err != nil {
		t.Fatalf("a dry run is not an error: %v", err)
	}
	for _, want := range []string{
		"caltechdata/b-2026-09-02.sql.gz", "3.0 MiB", "s3://my-bucket/",
		`database "caltechdata"`, "caltechdata-db-1", "replace",
		"Nothing was changed", "--confirm caltechdata",
		// the two scratch files are removed after a verified restore (item 1), so the plan says so
		remoteRestoreDownloadPath, remoteRestoreSQLPath, "once the table count",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan should mention %q, got:\n%s", want, out)
		}
	}
	for _, f := range []string{"aws s3 cp", "DROP DATABASE", "CREATE DATABASE", "docker exec -i"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("a dry run sent a command containing %q: %v", f, ssmClient.sentCommands)
		}
	}
}

func TestRestoreSQLCLI_DryRunSaysSoWhenThereIsNoExistingDatabase(t *testing.T) {
	_, _, out, err := runRestoreSQLCLI(t, "", sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz"), restoreSQLParams(), "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no existing database") {
		t.Errorf("the plan should say nothing will be replaced, got:\n%s", out)
	}
}

// --- --confirm ---

func TestRestoreSQLCLI_ConfirmDownloadsBeforeItDropsAnything(t *testing.T) {
	ssmClient, _, out, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz"), restoreSQLParams(), "caltechdata", false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	discover := commandIndex(t, sent, "docker ps")
	detect := commandIndex(t, sent, "pg_database")
	download := commandIndex(t, sent, "aws s3 cp")
	drop := commandIndex(t, sent, "DROP DATABASE")
	create := commandIndex(t, sent, "CREATE DATABASE")
	load := commandIndex(t, sent, "docker exec -i")
	count := commandIndex(t, sent, "information_schema.tables")
	if !(discover < detect && detect < download && download < drop && drop < create && create < load && load < count) {
		t.Errorf("want discover < detect < download < drop < create < load < count, got %d %d %d %d %d %d %d; sent: %v", discover, detect, download, drop, create, load, count, sent)
	}
	if !strings.Contains(out, "Confirmed (--confirm caltechdata)") || !strings.Contains(out, "Restored") {
		t.Errorf("want the one-line confirmation and the result, got:\n%s", out)
	}
}

func TestRestoreSQLCLI_ConfirmAcceptsTheInstanceID(t *testing.T) {
	if _, _, _, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b.sql.gz"), restoreSQLParams(), "i-1", false, ""); err != nil {
		t.Fatalf("--confirm with the instance ID must run: %v", err)
	}
}

func TestRestoreSQLCLI_ConfirmMismatchIsAUsageErrorBeforeAnyCall(t *testing.T) {
	ssmClient, s3Client, out, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b.sql.gz"), restoreSQLParams(), "someone-else", false, "")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a *UsageError, got: %v", err)
	}
	if len(ssmClient.sentCommands) != 0 || len(s3Client.listObjectsV2Calls) != 0 || out != "" {
		t.Errorf("no call and no output may precede the mismatch: ssm %v, s3 %d, out %q", ssmClient.sentCommands, len(s3Client.listObjectsV2Calls), out)
	}
}

// --- the terminal branch ---

func TestRestoreSQLCLI_TerminalPromptsAndProceedsOnTheExactName(t *testing.T) {
	ssmClient, _, out, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b.sql.gz"), restoreSQLParams(), "", true, "caltechdata\n")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !commandSent(ssmClient.sentCommands, "DROP DATABASE") {
		t.Errorf("the restore did not run after the right answer; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreSQLCLI_TerminalWrongAnswerChangesNothing(t *testing.T) {
	ssmClient, _, out, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b.sql.gz"), restoreSQLParams(), "", true, "wrong\n")
	if err != nil {
		t.Fatalf("declining is not an error: %v", err)
	}
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("got:\n%s", out)
	}
	for _, f := range []string{"aws s3 cp", "DROP DATABASE"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q was sent after a declined prompt", f)
		}
	}
}

// --- choosing the backup ---

func TestRestoreSQLCLI_LatestPicksTheMostRecentBackup(t *testing.T) {
	ssmClient, _, _, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz", "b-2026-09-02.sql.gz", "b-2026-09-03.sql.gz"), restoreSQLParams(), "caltechdata", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !commandSent(ssmClient.sentCommands, "b-2026-09-03.sql.gz") || commandSent(ssmClient.sentCommands, "b-2026-09-01.sql.gz") {
		t.Errorf("latest must resolve to the newest; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreSQLCLI_AnExactKeyOrAFileNameIsAccepted(t *testing.T) {
	objs := sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz", "b-2026-09-02.sql.gz")
	for _, backup := range []string{"caltechdata/b-2026-09-01.sql.gz", "b-2026-09-01.sql.gz"} {
		p := restoreSQLParams()
		p.Backup = backup
		ssmClient, _, _, err := runRestoreSQLCLI(t, "1\n", objs, p, "caltechdata", false, "")
		if err != nil {
			t.Errorf("%q: unexpected error: %v", backup, err)
			continue
		}
		if !commandSent(ssmClient.sentCommands, "b-2026-09-01.sql.gz") || commandSent(ssmClient.sentCommands, "b-2026-09-02.sql.gz") {
			t.Errorf("%q: wrong backup chosen; sent: %v", backup, ssmClient.sentCommands)
		}
	}
}

func TestRestoreSQLCLI_UnknownBackupListsWhatExistsAndChangesNothing(t *testing.T) {
	p := restoreSQLParams()
	p.Backup = "nope.sql.gz"
	ssmClient, _, _, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz", "b-2026-09-02.sql.gz"), p, "caltechdata", false, "")
	if err == nil {
		t.Fatal("expected an error for an unknown backup")
	}
	for _, want := range []string{"nope.sql.gz", "b-2026-09-01.sql.gz", "b-2026-09-02.sql.gz"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	if CLIExitCode(err) != 1 {
		t.Errorf("an unknown backup is an action failure (exit 1), got %d", CLIExitCode(err))
	}
	if commandSent(ssmClient.sentCommands, "aws s3 cp") || commandSent(ssmClient.sentCommands, "DROP DATABASE") {
		t.Errorf("nothing may change for an unknown backup; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreSQLCLI_EmptySourceIsAnErrorNamingTheBucketsOtherPrefixes(t *testing.T) {
	ssmClient, _, _, err := runRestoreSQLCLI(t, "1\n", sqlBackupObjects("other-box", 10, "b.sql.gz"), restoreSQLParams(), "caltechdata", false, "")
	if err == nil || !strings.Contains(err.Error(), "no SQL backups found") || !strings.Contains(err.Error(), "other-box") {
		t.Fatalf("want the no-backups error naming the other prefixes, got: %v", err)
	}
	if commandSent(ssmClient.sentCommands, "DROP DATABASE") {
		t.Error("the database was touched for an empty source")
	}
}
