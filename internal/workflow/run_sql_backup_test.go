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

// sqlBackupFake builds a fakeSSMClient that distinguishes the three
// commands runSQLBackup sends in sequence (CLI check, docker ps
// discovery, pg_dump) by substring, so each can report its own
// stdout/status independently.
func sqlBackupFake(discoveryStdout string, dumpStatus types.CommandInvocationStatus) *fakeSSMClient {
	return &fakeSSMClient{
		commandID:   "cmd-1",
		finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", stdout: "/usr/bin/aws", status: types.CommandInvocationStatusSuccess},
			{substring: "docker ps", stdout: discoveryStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "pg_dump", stdout: "", status: dumpStatus},
		},
	}
}

func TestRunSQLBackup_HappyPathDumpsAndDeclinesArchive(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + // directory
		"n\n" // decline "Continue to Archive SQL Backup to S3 now?"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	var archiveCalls int
	archiveSQL := func(ctx context.Context) error { archiveCalls++; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, archiveSQL, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if archiveCalls != 0 {
		t.Errorf("archiveCalls = %d, want 0 (operator declined)", archiveCalls)
	}
	if ssmClient.sendCommandCalls() != 3 {
		t.Errorf("sendCommandCalls = %d, want 3 (CLI check, docker ps, pg_dump)", ssmClient.sendCommandCalls())
	}
}

func TestRunSQLBackup_HappyPathDumpsAndChainsIntoArchive(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" +
		"y\n" // confirm "Continue to Archive SQL Backup to S3 now?"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	var archiveCalls int
	archiveSQL := func(ctx context.Context) error { archiveCalls++; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, archiveSQL, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if archiveCalls != 1 {
		t.Errorf("archiveCalls = %d, want 1 (operator confirmed)", archiveCalls)
	}
}

func TestRunSQLBackup_DiscoveryFailureAbortsBeforeDump(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("", types.CommandInvocationStatusSuccess) // zero containers found
	archiveSQL := func(ctx context.Context) error { t.Fatal("archiveSQL should not be called"); return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, archiveSQL, le, buf)
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
	archiveSQL := func(ctx context.Context) error { t.Fatal("archiveSQL should not be called"); return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, archiveSQL, le, buf)
	if err == nil {
		t.Fatal("expected a dump-failure error")
	}
}

func TestRunSQLBackup_PreFillsDirectoryFromMatchingRule(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "rdm-prod-01", Region: "us-east-1"}
	input := "\n" + // accept the pre-filled directory default
		"n\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\trdm-prod-01-db-1\n", types.CommandInvocationStatusSuccess)
	rules := []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}}

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, rules, nil, BackupHistory{}, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.Join(ssmClient.sentCommands, "\n"), "/opt/rdm_sql_backups") {
		t.Errorf("expected the pre-filled directory to be used, sent commands: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_SavesInstanceAndDirectoryAfterPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + "n\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)

	var savedInstanceID, savedDirectory string
	hist := BackupHistory{Save: func(instanceID, directory string) error {
		savedInstanceID, savedDirectory = instanceID, directory
		return nil
	}}

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, hist, nil, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if savedInstanceID != "i-1" || savedDirectory != "/opt/rdm_sql_backups" {
		t.Errorf("saved (%q, %q), want (%q, %q)", savedInstanceID, savedDirectory, "i-1", "/opt/rdm_sql_backups")
	}
}

func TestRunSQLBackup_SavesRDMPostgresRulesWhenChanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + "n\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors_db_1"}}

	var savedRules []config.RDMPostgresRule
	saveFn := func(rules []config.RDMPostgresRule) error { savedRules = rules; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, existing, BackupHistory{}, saveFn, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(savedRules) != 1 || savedRules[0].ContainerName != "caltechauthors-db-1" {
		t.Errorf("savedRules = %v, want ContainerName updated to %q", savedRules, "caltechauthors-db-1")
	}
}

func TestRunSQLBackup_DoesNotSaveRDMPostgresRulesWhenUnchanged(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + "n\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"}}

	saveCalls := 0
	saveFn := func(rules []config.RDMPostgresRule) error { saveCalls++; return nil }

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, existing, BackupHistory{}, saveFn, nil, le, buf)
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
	input := "/opt/rdm_sql_backups\n" + "n\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, nil, le, buf)
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

	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, nil, le, buf)
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
