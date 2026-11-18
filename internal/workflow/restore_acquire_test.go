package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// routedExecutor answers by substring of the command, in order; no match is a
// success with empty output. It records every command it is given.
type routedExecutor struct {
	name      string
	responses []ssmCommandResponse
	commands  []string
}

func (r *routedExecutor) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, CommandStatus, error) {
	r.commands = append(r.commands, command)
	for _, resp := range r.responses {
		if strings.Contains(command, resp.substring) {
			return resp.stdout, CommandStatus(resp.status), nil
		}
	}
	return "", StatusSuccess, nil
}
func (r *routedExecutor) Put(ctx context.Context, l, rm string) error { return nil }
func (r *routedExecutor) Get(ctx context.Context, rm, l string) error { return nil }
func (r *routedExecutor) Name() string                                { return r.name }

func (r *routedExecutor) indexOf(substr string) int { return indexOfCommand(r.commands, substr) }

// ---- SQL ----

func TestExecuteSQLRestoreOn_TheAcquirerRunsFirstAndItsPathIsWhatIsLoaded(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	ex := NewSSMExecutor(fake, "i-1")
	var sentWhenCalled = -1
	acquire := func(ctx context.Context, e Executor) (string, error) {
		sentWhenCalled = len(fake.sentCommands)
		return "/var/tmp/custom-acquired.sql", nil
	}
	if err := executeSQLRestoreOn(context.Background(), &strings.Builder{}, ex, cleanupRun(), acquire); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentWhenCalled != 0 {
		t.Errorf("the acquirer must run before any command, %d were sent first", sentWhenCalled)
	}
	if i := indexOfCommand(fake.sentCommands, "/var/tmp/custom-acquired.sql"); i < 0 {
		t.Errorf("the load must use the acquirer's path; sent: %v", fake.sentCommands)
	}
	if i := indexOfCommand(fake.sentCommands, "aws s3"); i >= 0 {
		t.Errorf("a custom acquirer means no on-host S3 command: %q", fake.sentCommands[i])
	}
}

func TestExecuteSQLRestoreOn_AFailedAcquireStopsBeforeAnythingIsDropped(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	boom := errors.New("download failed")
	err := executeSQLRestoreOn(context.Background(), &strings.Builder{}, NewSSMExecutor(fake, "i-1"), cleanupRun(),
		func(ctx context.Context, e Executor) (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the acquirer's error", err)
	}
	if len(fake.sentCommands) != 0 {
		t.Errorf("nothing may be sent after a failed acquire: %v", fake.sentCommands)
	}
}

func TestAWSSQLAcquirer_RunsTheOnHostDownloadCommandUnchanged(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	path, err := awsSQLAcquirer("my-bucket", "src/backup.sql.gz")(context.Background(), NewSSMExecutor(fake, "i-1"))
	if err != nil || path != remoteRestoreSQLPath {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if len(fake.sentCommands) != 1 || fake.sentCommands[0] != buildDownloadAndDecompressCommand("my-bucket", "src/backup.sql.gz") {
		t.Errorf("sent: %v", fake.sentCommands)
	}
}

func TestRestoreSQLOnHost_RunsOnAnyExecutorWithNoGuardAndNoS3(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	var out strings.Builder
	err := RestoreSQLOnHost(context.Background(), &out, NewSSMExecutor(fake, "vm-1"),
		SQLRestoreParams{ContainerName: "db-1", DBName: "caltechauthors", DBUser: "postgres", Bucket: "b", Key: "k.sql.gz"},
		func(ctx context.Context, e Executor) (string, error) { return remoteRestoreSQLPath, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Restored") || !strings.Contains(out.String(), "vm-1") {
		t.Errorf("output should name the host: %s", out.String())
	}
}

// ---- OpenSearch ----

func osRoutes() []ssmCommandResponse {
	return []ssmCommandResponse{
		{substring: "clasm-stale", stdout: "clasm-stale-moved 0\n", status: types.CommandInvocationStatusSuccess},
		{substring: "-X DELETE", stdout: "200", status: types.CommandInvocationStatusSuccess},
	}
}

func osRun() openSearchRestoreRun {
	return openSearchRestoreRun{
		owner: ServiceOwner{UID: 1000, GID: 1001}, directory: "/opt/rdm_opensearch_backups", bucket: "b",
		sourceName: "caltechauthors-v13", indexPrefix: "caltechauthors", snap: SnapshotPrefixInfo{Name: "snap-1"},
		indices: []string{"caltechauthors-*"},
	}
}

func TestExecuteOpenSearchRestoreOn_TheAcquirerRunsAfterTheStaleMoveAndBeforeTheOwnershipStep(t *testing.T) {
	ex := &routedExecutor{name: "vm-1", responses: osRoutes()}
	var seen []string
	var gotDir string
	acquire := func(ctx context.Context, e Executor, dir string) error {
		seen = append([]string{}, ex.commands...)
		gotDir = dir
		return errors.New("stop here")
	}
	_ = executeOpenSearchRestoreOn(context.Background(), &strings.Builder{}, ex, osRun(), acquire)
	if gotDir != "/opt/rdm_opensearch_backups" {
		t.Fatalf("the acquirer was not called with the directory: %q (commands: %v)", gotDir, ex.commands)
	}
	if indexOfCommand(seen, "clasm-stale") < 0 {
		t.Errorf("the stale-generation move must come before the acquire; had run: %v", seen)
	}
	if indexOfCommand(seen, "chown") >= 0 {
		t.Errorf("the ownership step must come after the acquire; had run: %v", seen)
	}
}

func TestExecuteOpenSearchRestoreOn_AFailedAcquireStopsBeforeOwnershipRegistrationAndDeletion(t *testing.T) {
	ex := &routedExecutor{name: "vm-1", responses: osRoutes()}
	boom := errors.New("transfer failed")
	err := executeOpenSearchRestoreOn(context.Background(), &strings.Builder{}, ex, osRun(),
		func(ctx context.Context, e Executor, dir string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// The one DELETE allowed is the deregistration that precedes the acquire.
	deletes := 0
	for _, c := range ex.commands {
		if strings.Contains(c, "-X DELETE") {
			deletes++
		}
	}
	if deletes > 1 {
		t.Errorf("an index or snapshot was deleted after a failed acquire: %v", ex.commands)
	}
	for _, bad := range []string{"chown", "-X PUT"} {
		if indexOfCommand(ex.commands, bad) >= 0 {
			t.Errorf("a failed acquire must stop the run; saw %q in %v", bad, ex.commands)
		}
	}
}

func TestAWSSnapshotAcquirer_RunsTheOnHostSyncCommandUnchanged(t *testing.T) {
	ex := &routedExecutor{name: "i-1"}
	if err := awsSnapshotAcquirer("my-bucket", "caltechauthors-v13", "snap-1")(context.Background(), ex, "/opt/dir"); err != nil {
		t.Fatal(err)
	}
	if len(ex.commands) != 1 || ex.commands[0] != buildSyncFromS3Command("my-bucket", "caltechauthors-v13", "snap-1", "/opt/dir") {
		t.Errorf("sent: %v", ex.commands)
	}
}

// ---- the AWS wrappers are wired to the right bucket, key and names ----

func TestExecuteSQLRestore_TheAWSPathStillDownloadsTheRunsBackupFirst(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	if err := executeSQLRestore(context.Background(), &strings.Builder{}, fake, cleanupRun()); err != nil {
		t.Fatal(err)
	}
	r := cleanupRun()
	if len(fake.sentCommands) == 0 || fake.sentCommands[0] != buildDownloadAndDecompressCommand(r.bucket, r.object.Key) {
		t.Errorf("first command = %v, want the on-host download of s3://%s/%s", fake.sentCommands, r.bucket, r.object.Key)
	}
}

func TestExecuteOpenSearchRestore_TheAWSPathStillSyncsTheRunsSnapshot(t *testing.T) {
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, responses: []ssmCommandResponse{
		{substring: "clasm-stale", stdout: "clasm-stale-moved 0\n", status: types.CommandInvocationStatusSuccess},
		{substring: "-X DELETE", stdout: "200", status: types.CommandInvocationStatusSuccess},
		{substring: "chown", stdout: "stop", status: types.CommandInvocationStatusFailed},
	}}
	r := osRun()
	r.inst.InstanceID = "i-1"
	_ = executeOpenSearchRestore(context.Background(), &strings.Builder{}, fake, r)
	want := buildSyncFromS3Command(r.bucket, r.sourceName, r.snap.Name, r.directory)
	if indexOfCommand(fake.sentCommands, want) < 0 {
		t.Errorf("want the on-host sync %q, sent %v", want, fake.sentCommands)
	}
}

// ---- the recheck reads the acquirer's file, and the exported function maps its parameters ----

func TestExecuteSQLRestoreOn_TheFileLocationRecheckReadsTheAcquirersFileBeforeAnythingElse(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	r := cleanupRun()
	r.locations = &restoreLocationReport{}
	_ = executeSQLRestoreOn(context.Background(), &strings.Builder{}, NewSSMExecutor(fake, "i-1"), r,
		func(ctx context.Context, e Executor) (string, error) { return "/var/tmp/custom-acquired.sql", nil })
	if len(fake.sentCommands) == 0 || !strings.Contains(fake.sentCommands[0], "/var/tmp/custom-acquired.sql") || strings.Contains(fake.sentCommands[0], "DROP") {
		t.Errorf("the first command should scan the acquired file: %v", fake.sentCommands)
	}
}

func TestRestoreSQLOnHost_MapsTheDatabaseAndUserToTheRightPlaces(t *testing.T) {
	fake := cleanupFake(types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	err := RestoreSQLOnHost(context.Background(), &strings.Builder{}, NewSSMExecutor(fake, "vm-1"),
		SQLRestoreParams{ContainerName: "the-container", DBName: "mydb", DBUser: "myuser"},
		func(ctx context.Context, e Executor) (string, error) { return remoteRestoreSQLPath, nil })
	if err != nil {
		t.Fatal(err)
	}
	if indexOfCommand(fake.sentCommands, "DROP DATABASE IF EXISTS \"mydb\"") < 0 || indexOfCommand(fake.sentCommands, "--username='myuser'") < 0 || indexOfCommand(fake.sentCommands, "'the-container'") < 0 {
		t.Errorf("sent: %v", fake.sentCommands)
	}
}

// ---- exports for the Multipass acquirers ----

func TestRestoreScratchPathsAreTheOnesTheCleanupRemoves(t *testing.T) {
	if RestoreDownloadPath != remoteRestoreDownloadPath || RestoreSQLPath != remoteRestoreSQLPath {
		t.Errorf("exported paths %q %q differ from %q %q", RestoreDownloadPath, RestoreSQLPath, remoteRestoreDownloadPath, remoteRestoreSQLPath)
	}
}

func TestDecompressRestoreDownloadCommand_IsTheDecompressStepOfTheAWSCommand(t *testing.T) {
	aws := buildDownloadAndDecompressCommand("b", "k/x.sql.gz")
	frag := "gunzip -c " + shellQuote(RestoreDownloadPath) + " > " + shellQuote(RestoreSQLPath)
	if !strings.Contains(aws, frag) {
		t.Fatalf("the AWS command no longer holds %q:\n%s", frag, aws)
	}
	if got := DecompressRestoreDownloadCommand(); !strings.Contains(got, frag) || !strings.HasPrefix(got, "{ set -e;") || !strings.HasSuffix(got, "2>&1") {
		t.Errorf("command = %q", got)
	}
}

func TestNeedsDecompression_IsExportedAndBySuffix(t *testing.T) {
	if !NeedsDecompression("a/b.sql.gz") || NeedsDecompression("a/b.sql") {
		t.Error("by .gz suffix")
	}
}
