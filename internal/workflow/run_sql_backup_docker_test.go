package workflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// DR-0178 (design brief generate_sql_backup_preflight_and_archive_repair.md,
// Part 1). Generate SQL Backup ran `command -v aws` first, although nothing it
// does touches S3 or the CLI: `docker exec pg_dump`, `install -d`, `chown -R`.
// An instance without the AWS CLI could not be backed up, and the error blamed
// a tool the operation does not use (found 2026-09-29). The check is replaced by
// one for what the workflow needs, in the same place -- the first SSM call,
// before any prompt, so it still doubles as the early reachability check.

func TestCheckDockerAvailable_FoundIsNil(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/docker"}
	if err := CheckDockerAvailable(context.Background(), fake, "i-1", time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.sentCommands) != 1 || !strings.Contains(fake.sentCommands[0], "command -v docker") {
		t.Errorf("want exactly `command -v docker`, sent: %v", fake.sentCommands)
	}
}

func TestCheckDockerAvailable_MissingNamesDockerAndWhy(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := CheckDockerAvailable(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"docker", "i-1", "pg_dump"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "AWS CLI") {
		t.Errorf("the error must not blame the AWS CLI, got: %v", err)
	}
}

func TestCheckDockerAvailable_TransportErrorPropagates(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errUnavailable}
	if err := CheckDockerAvailable(context.Background(), fake, "i-1", time.Second, testPollInterval); !errors.Is(err, errUnavailable) {
		t.Fatalf("want errUnavailable, got: %v", err)
	}
}

func sqlBackupInput() string { return "/opt/rdm_sql_backups\n" }

func runSQLBackupOn(ssmClient *fakeSSMClient, input string) error {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor(input)
	return runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, nil, nil, BackupHistory{}, nil, le, buf)
}

func TestRunSQLBackup_WithoutTheAWSCLIStillDumps(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	ssmClient.responses = append([]ssmCommandResponse{failingResponse("command -v aws", "")}, ssmClient.responses...)
	if err := runSQLBackupOn(ssmClient, sqlBackupInput()); err != nil {
		t.Fatalf("an instance with no AWS CLI must still be backed up, got: %v", err)
	}
	if !commandSent(ssmClient.sentCommands, "pg_dump") {
		t.Errorf("the dump never ran; sent: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_NeverChecksForTheAWSCLI(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	if err := runSQLBackupOn(ssmClient, sqlBackupInput()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commandSent(ssmClient.sentCommands, "command -v aws") {
		t.Errorf("Generate SQL Backup checked for the AWS CLI, which it never uses; sent: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_DockerCheckIsTheFirstCommand(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	if err := runSQLBackupOn(ssmClient, sqlBackupInput()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ssmClient.sentCommands) == 0 || !strings.Contains(ssmClient.sentCommands[0], "command -v docker") {
		t.Errorf("the first SSM call must be the docker check (it is also the early reachability check); sent: %v", ssmClient.sentCommands)
	}
}

func TestRunSQLBackup_MissingDockerStopsBeforeAnyPrompt(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	ssmClient.responses = append([]ssmCommandResponse{failingResponse("command -v docker", "")}, ssmClient.responses...)
	// No input at all: if the workflow reached the directory prompt it would fail on EOF, not on docker.
	err := runSQLBackupOn(ssmClient, "")
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("expected an error naming docker, got: %v", err)
	}
	if len(ssmClient.sentCommands) != 1 {
		t.Errorf("nothing but the docker check may be sent; sent: %v", ssmClient.sentCommands)
	}
}

// A failed dump must say why. RunShellCommand returns the command's stdout, so
// the failure's cause reaches the operator only if the dump command folds
// stderr into it, and the error then carries it.
func TestRunSQLBackup_FailedDumpReportsTheCause(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	ssmClient.responses = append([]ssmCommandResponse{failingResponse("pg_dump", `Error response from daemon: No such container: caltechauthors-db-1`)}, ssmClient.responses...)
	err := runSQLBackupOn(ssmClient, sqlBackupInput())
	if err == nil || !strings.Contains(err.Error(), "No such container: caltechauthors-db-1") {
		t.Fatalf("the error should carry docker's own message, got: %v", err)
	}
}

// The ordering claim in the design brief, tested by running the real command
// under sh with a fake docker: stderr must reach the command's own output, and
// the dump file must hold stdout only. Folding stderr into the dump would
// corrupt every backup with error text.
func TestBuildSQLDumpCommand_StderrReachesTheOperatorAndNeverTheDump(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDocker := "#!/bin/sh\necho 'DUMP-DATA'\necho 'DOCKER-COMPLAINT' >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", buildSQLDumpCommand("db-1", "mydb", "me", dir, "2026-09-30"))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	// Stdout only, because that is all SSM returns to clasm: CombinedOutput would
	// merge stderr for us and hide the very thing under test.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "DOCKER-COMPLAINT") {
		t.Errorf("docker's stderr never reached the command's output, which is all SSM returns; got: %q", out)
	}
	gz, err := os.ReadFile(filepath.Join(dir, "db-1-mydb-2026-09-30.sql.gz"))
	if err != nil {
		t.Fatalf("no compressed dump: %v", err)
	}
	raw, err := exec.Command("sh", "-c", "gzip -dc "+shellQuote(filepath.Join(dir, "db-1-mydb-2026-09-30.sql.gz"))).Output()
	if err != nil {
		t.Fatalf("could not decompress the dump (%d bytes): %v", len(gz), err)
	}
	if strings.Contains(string(raw), "DOCKER-COMPLAINT") {
		t.Errorf("docker's stderr leaked into the dump file: %q", raw)
	}
	if !strings.Contains(string(raw), "DUMP-DATA") {
		t.Errorf("the dump lost its stdout: %q", raw)
	}
}
