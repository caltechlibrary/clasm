package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// sqlBackupFake builds a fakeSSMClient that distinguishes the commands
// runSQLBackup sends in sequence (docker check, docker ps
// discovery, owner lookup, create-directory-if-missing, pg_dump, hand-over of the dump) by substring, so each can report its own
// stdout/status independently.
func sqlBackupFake(discoveryStdout string, dumpStatus types.CommandInvocationStatus) *fakeSSMClient {
	return &fakeSSMClient{
		commandID:   "cmd-1",
		finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{
			{substring: "command -v docker", stdout: "/usr/bin/docker", status: types.CommandInvocationStatusSuccess},
			{substring: "docker ps", stdout: discoveryStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "id -u", stdout: "1000 1001\n", status: types.CommandInvocationStatusSuccess}, // ubuntu on the current images
			{substring: "install -d", stdout: "", status: types.CommandInvocationStatusSuccess},
			{substring: "pg_dump", stdout: "", status: dumpStatus},
			{substring: "chmod 0664", stdout: "", status: types.CommandInvocationStatusSuccess},
		},
	}
}

// TestRunSQLBackup_HappyPathDumpsAndReturnsWithNoFurtherPrompt is a
// regression test for Phase 20.54 (DECISIONS.md, "Run SQL Backup: drop
// the Archive-SQL auto-chain, rename to 'Generate SQL Backup'"): once the
// dump succeeds, runSQLBackup must return immediately -- no "Continue to
// Archive SQL Backup to S3 now?" prompt, and nothing else consumed from
// input.
func TestRunSQLBackup_HappyPathDumpsAndReturnsWithNoFurtherPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" // directory only -- no further prompt to answer

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ssmClient.sendCommandCalls() != 6 {
		t.Errorf("sendCommandCalls = %d, want 6 (CLI check, docker ps, owner lookup, create directory if missing, pg_dump, hand over the dump)", ssmClient.sendCommandCalls())
	}
}

func TestRunSQLBackup_DiscoveryFailureAbortsBeforeDump(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("", types.CommandInvocationStatusSuccess) // zero containers found
	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	if err == nil {
		t.Fatal("expected a discovery-failure error")
	}
	if ssmClient.sendCommandCalls() != 2 {
		t.Errorf("sendCommandCalls = %d, want 2 (CLI check, docker ps -- no dump attempt)", ssmClient.sendCommandCalls())
	}
}

func TestRunSQLBackup_DumpCommandFailureReported(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusFailed)
	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	if err == nil {
		t.Fatal("expected a dump-failure error")
	}
}

func TestRunSQLBackup_PreFillsDirectoryFromMatchingRule(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "rdm-prod-01", Region: "us-east-1"}
	input := "\n" // accept the pre-filled directory default

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\trdm-prod-01-db-1\n", types.CommandInvocationStatusSuccess)
	rules := []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}}

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, rules, nil, BackupHistory{}, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.Join(ssmClient.sentCommands, "\n"), "/opt/rdm_sql_backups") {
		t.Errorf("expected the pre-filled directory to be used, sent commands: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_SavesInstanceAndDirectoryAfterPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)

	var savedInstanceID, savedDirectory string
	hist := BackupHistory{Save: func(instanceID, directory string) error {
		savedInstanceID, savedDirectory = instanceID, directory
		return nil
	}}

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, hist, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if savedInstanceID != "i-1" || savedDirectory != "/opt/rdm_sql_backups" {
		t.Errorf("saved (%q, %q), want (%q, %q)", savedInstanceID, savedDirectory, "i-1", "/opt/rdm_sql_backups")
	}
}

func TestRunSQLBackup_SavesRDMPostgresRulesWhenChanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors_db_1"}}

	var savedRules []config.RDMPostgresRule
	saveFn := func(rules []config.RDMPostgresRule) error { savedRules = rules; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, existing, BackupHistory{}, saveFn, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(savedRules) != 1 || savedRules[0].ContainerName != "caltechauthors-db-1" {
		t.Errorf("savedRules = %v, want ContainerName updated to %q", savedRules, "caltechauthors-db-1")
	}
}

func TestRunSQLBackup_DoesNotSaveRDMPostgresRulesWhenUnchanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"}}

	saveCalls := 0
	saveFn := func(rules []config.RDMPostgresRule) error { saveCalls++; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, existing, BackupHistory{}, saveFn, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saveCalls != 0 {
		t.Errorf("saveCalls = %d, want 0 (nothing changed)", saveCalls)
	}
}

// TestRunSQLBackup_UsesProjectTagOverNameTagForDatabaseName reproduces
// the real 2026-07-29 CaltechAUTHORS incident directly:
// i-0c4c81336aea33d27's own EC2 Name tag is "newauthors" (a legacy
// label), while its Project tag is "caltechauthors" (the real project
// shortname) -- the dump must use "caltechauthors" as the database name/
// user, not "newauthors".
func TestRunSQLBackup_UsesProjectTagOverNameTagForDatabaseName(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Project: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := strings.Join(ssmClient.sentCommands, "\n")
	if strings.Contains(sent, "newauthors") {
		t.Errorf("expected the Name tag %q never to appear in the dump command, got: %s", "newauthors", sent)
	}
	if !strings.Contains(sent, "--username='caltechauthors' --column-inserts 'caltechauthors'") {
		t.Errorf("expected the dump command to use the Project tag %q as db name/user, got: %s", "caltechauthors", sent)
	}
}

func TestRunSQLBackup_CLIUnavailableAbortsBeforeAnyPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor("")
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, sendCommandErr: errUnavailable}

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	if !errors.Is(err, errUnavailable) {
		t.Fatalf("expected errUnavailable to propagate, got: %v", err)
	}
}

func TestBuildSQLDumpCommand_ExactShape(t *testing.T) {
	cases := []struct {
		containerName, dbName, dbUser, directory, date string
	}{
		{"caltechauthors-db-1", "caltechauthors", "caltechauthors", "/opt/rdm_sql_backups", "2026-07-29"},
		{"caltechdata-db-1", "caltechdata", "caltechdata", "/opt/rdm_sql_backups", "2026-08-01"},
	}
	for _, c := range cases {
		got := buildSQLDumpCommand(c.containerName, c.dbName, c.dbUser, c.directory, c.date)
		rawFile := c.directory + "/" + c.containerName + "-" + c.dbName + "-" + c.date + ".sql"
		if !strings.Contains(got, "docker exec") || !strings.Contains(got, c.containerName) ||
			!strings.Contains(got, "pg_dump") || !strings.Contains(got, "--column-inserts") ||
			!strings.Contains(got, c.dbUser) || !strings.Contains(got, c.dbName) ||
			!strings.Contains(got, "gzip") || !strings.Contains(got, rawFile) {
			t.Errorf("buildSQLDumpCommand(%+v) = %q, missing an expected element (want raw file %q)", c, got, rawFile)
		}
	}
}

// TestBuildSQLDumpCommand_NoPipeAvoidsExitStatusMasking reproduces the
// real 2026-07-29 CaltechAUTHORS incident: an earlier design piped
// pg_dump directly into gzip (`pg_dump ... | gzip > file`), so a failed
// pg_dump (e.g. connecting to a nonexistent database) still reported
// Success, since gzip's own exit status (always 0 on empty input) is
// what the shell -- and therefore SSM's CommandInvocationStatus --
// actually sees. Fixed by matching invenio-sql-backup.bash's own real,
// already-battle-tested approach exactly: pg_dump redirects to a plain
// file first, gzip compresses it as a separate step second, joined with
// `set -e` (this project's own established pattern, ssm_grow.go's
// rootFilesystemGrowCommand) so pg_dump's own failure aborts before gzip
// ever runs -- no pipe, nothing to mask.
func TestBuildSQLDumpCommand_NoPipeAvoidsExitStatusMasking(t *testing.T) {
	got := buildSQLDumpCommand("caltechauthors-db-1", "caltechauthors", "caltechauthors", "/opt/rdm_sql_backups", "2026-07-29")
	if strings.Contains(got, "|") {
		t.Errorf("buildSQLDumpCommand contains a pipe, which masks pg_dump's own exit status behind gzip's -- got: %q", got)
	}
	if !strings.Contains(got, "set -e") {
		t.Errorf("expected \"set -e\" so pg_dump's failure aborts before gzip runs, got: %q", got)
	}
}

// --- ownership (Phase 20.65 item 4, DR-0176) ---

const (
	sqlDir            = "/opt/rdm_sql_backups"
	sqlEnsureExact    = "[ -d '/opt/rdm_sql_backups' ] || install -d -o 1000 -g 'www-data' -m 0770 '/opt/rdm_sql_backups'"
	sqlChownExact     = "chmod 0664 '/opt/rdm_sql_backups/caltechauthors-db-1-caltechauthors-"
	sqlOwnerLookupFrg = "id -u"
	sqlDumpFrg        = "pg_dump"
)

func runSQLBackupWith(t *testing.T, ssmClient *fakeSSMClient) (string, error) {
	t.Helper()
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor(sqlDir + "\n")
	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
	return buf.String(), err
}

// A missing directory is made before the dump, and the new dump is handed
// over after it. Create-before-dump so the dump never lands nowhere; hand-over
// after, so the new file -- root-owned, because SSM runs as root, and therefore
// unwritable by the cron script's same-day redirect -- is the service user's,
// in the directory's own group, group-writable. It asserts on the commands
// actually sent, not on the absence of errors: the fake answers an unscripted
// command with a default success.
func TestRunSQLBackup_CreatesBeforeDumpAndHandsOverAfter(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	if _, err := runSQLBackupWith(t, ssmClient); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	lookup := commandIndex(t, sent, sqlOwnerLookupFrg)
	ensure := commandIndex(t, sent, sqlEnsureExact)
	dump := commandIndex(t, sent, sqlDumpFrg)
	chown := commandIndex(t, sent, sqlChownExact)
	if !(lookup < ensure && ensure < dump && dump < chown) {
		t.Errorf("want lookup < ensure < dump < chown, got %d %d %d %d; sent: %v", lookup, ensure, dump, chown, sent)
	}
}

// A dump that fails leaves the tree exactly as it was: nothing is handed over.
func TestRunSQLBackup_FailedDumpHandsOverNothing(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusFailed)
	if _, err := runSQLBackupWith(t, ssmClient); err == nil {
		t.Fatal("expected a dump-failure error")
	}
	if commandSent(ssmClient.sentCommands, "chmod 0664") {
		t.Errorf("a chown was sent after a failed dump; sent: %v", ssmClient.sentCommands)
	}
}

// A directory that cannot be made or repaired means there is nowhere sound
// to dump, so nothing is dumped.
func TestRunSQLBackup_FailedEnsureAbortsBeforeDump(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	setResponseStatus(ssmClient, "install -d", types.CommandInvocationStatusFailed, "install: cannot change owner")
	_, err := runSQLBackupWith(t, ssmClient)
	if err == nil || !strings.Contains(err.Error(), sqlDir) {
		t.Fatalf("expected an error naming the directory, got: %v", err)
	}
	if commandSent(ssmClient.sentCommands, sqlDumpFrg) || commandSent(ssmClient.sentCommands, "chmod 0664") {
		t.Errorf("dump or chown sent after a failed ensure; sent: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_FailedOwnerLookupAbortsBeforeAnyWrite(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	setResponseStatus(ssmClient, sqlOwnerLookupFrg, types.CommandInvocationStatusFailed, "id: 'ubuntu': no such user")
	_, err := runSQLBackupWith(t, ssmClient)
	if err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Fatalf("expected the lookup failure, got: %v", err)
	}
	for _, f := range []string{"install -d", sqlDumpFrg, "chmod 0664"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q sent after a failed owner lookup; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

// The dump exists but is still root-owned, which is the defect: the run
// must not report success.
func TestRunSQLBackup_FailedChownIsNotReportedAsSuccess(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	setResponseStatus(ssmClient, "chmod 0664", types.CommandInvocationStatusFailed, "chown: Operation not permitted")
	out, err := runSQLBackupWith(t, ssmClient)
	if err == nil || !strings.Contains(err.Error(), sqlDir) {
		t.Fatalf("expected an error naming the directory, got: %v", err)
	}
	if strings.Contains(out, "SQL backup created") {
		t.Errorf("reported success although the chown failed:\n%s", out)
	}
}

// The uid rule is an OpenSearch rule (DR-0176 decision 5): the SQL dump has
// no container writing into the directory, so a service user that is not
// uid 1000 is not a reason to refuse a backup.
func TestRunSQLBackup_ServiceUserNeedNotBeUID1000(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	setResponseStatus(ssmClient, sqlOwnerLookupFrg, types.CommandInvocationStatusSuccess, "1234 1235\n")
	if _, err := runSQLBackupWith(t, ssmClient); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "install -d -o 1234 -g 'www-data' -m 0770 '/opt/rdm_sql_backups'"; !commandSent(ssmClient.sentCommands, want) {
		t.Errorf("expected %q; sent: %v", want, ssmClient.sentCommands)
	}
	if want := "chown 1234:"; !commandSent(ssmClient.sentCommands, want) {
		t.Errorf("expected %q; sent: %v", want, ssmClient.sentCommands)
	}
}

// setResponseStatus rewrites the first scripted response matching substring.
func setResponseStatus(f *fakeSSMClient, substring string, status types.CommandInvocationStatus, stdout string) {
	for i := range f.responses {
		if f.responses[i].substring == substring {
			f.responses[i].status = status
			f.responses[i].stdout = stdout
			return
		}
	}
	panic("no scripted response matching " + substring)
}

// DR-0176 decisions 2-4 were wrong for the SQL directory, and the consequence
// DR-0176 itself recorded ("existing production instances have their SQL
// directory changed from root to ubuntu") is what stopped caltechauthors-v13's
// dumps on 2026-09-30: the cron that writes them runs as rsdoiel, through the
// www-data group, so a directory re-owned to ubuntu:ubuntu 0750 locked it
// out. Pin the rule that replaced them: no run ever rewrites an existing
// directory's owner or mode, and nothing is chowned recursively.
func TestRunSQLBackup_NeverRewritesAnExistingDirectoryOrChownsRecursively(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	if _, err := runSQLBackupWith(t, ssmClient); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "chown -R") || strings.Contains(sent, "chmod -R") {
			t.Errorf("a recursive ownership change was sent: %q", sent)
		}
		if strings.Contains(sent, "install -d") && !strings.HasPrefix(sent, "[ -d ") {
			t.Errorf("install -d must be guarded by a directory-exists test, got: %q", sent)
		}
	}
}
