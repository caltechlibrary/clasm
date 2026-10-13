package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Restore SQL's file-location guard (DR-0187). Before anything is replaced, the
// backup's files_location rows are read; each S3 bucket they name is judged
// writable or read-only for this instance; a writable bucket needs its own
// --confirm (or, on a terminal, its name typed). The safe setup -- an instance that
// cannot write to any bucket the backup names -- is never blocked.

const (
	authorsLocationRow = `INSERT INTO public.files_location (created, updated, id, name, uri, "default", size) VALUES ('2024-03-07 18:02:11', '2024-03-07 18:02:11', 'x', 'default-location', 's3://caltechauthors', true, 0);`
	dataLocationRow    = `INSERT INTO public.files_location (name, uri) VALUES ('data', 's3://caltechdata');`
	localLocationRow   = `INSERT INTO public.files_location (name, uri) VALUES ('scratch', '/var/lib/files');`
)

func scanOut(rows ...string) string { return strings.Join(rows, "\n") + "\nclasm-scan-rc=0\n" }

// guardScenario is one restore onto restoreSQLInst. streamScan is what streaming
// the backup finds, localScan what grep finds on the downloaded file (the same
// unless a test makes them differ), decisions what the simulator says about the
// instance's role.
type guardScenario struct {
	streamScan, localScan string
	decisions             map[string]iamtypes.PolicyEvaluationDecisionType
	envOut                string
	confirms              []string
	interactive           bool
	typed                 string
}

func (s guardScenario) run(t *testing.T) (*fakeSSMClient, string, error) {
	t.Helper()
	ssmFake := restoreSQLFake(restoreSQLDiscovery, "1\n", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	local := s.localScan
	if local == "" {
		local = s.streamScan
	}
	env := s.envOut
	if env == "" {
		env = "clasm-env=absent\n"
	}
	ssmFake.responses = append([]ssmCommandResponse{
		{substring: "/var/tmp/clasm-sql-restore", stdout: local, status: types.CommandInvocationStatusSuccess}, // the local re-scan
		{substring: "pipefail", stdout: s.streamScan, status: types.CommandInvocationStatusSuccess},            // the stream scan
		{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: env, status: types.CommandInvocationStatusSuccess},
	}, ssmFake.responses...)
	fx := newWriteFixture(s.decisions)
	guard := RestoreGuardClients{EC2: map[string]awsclient.EC2API{"us-east-1": fx.ec2}, IAM: fx.iam}

	var w io.Writer
	var in io.Reader
	var buf *bytes.Buffer
	if s.interactive {
		w, in, buf = newPipeEditor(s.typed)
	} else {
		buf = &bytes.Buffer{}
		w, in = buf, failingReader{t}
	}
	s3Client := &fakeS3Client{allObjects: sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz")}
	err := RunRestoreSQLBackupAuto(context.Background(), w, ssmFake, s3Client, restoreSQLInst, guard, nil, nil, restoreSQLParams(), s.confirms, s.interactive, in, buf)
	return ssmFake, buf.String(), err
}

func writableAuthors() map[string]iamtypes.PolicyEvaluationDecisionType {
	return map[string]iamtypes.PolicyEvaluationDecisionType{allowed(writeRoleARN, "caltechauthors"): iamtypes.PolicyEvaluationDecisionTypeAllowed}
}

func nothingChanged(t *testing.T, sent []string) {
	t.Helper()
	for _, f := range []string{remoteRestoreDownloadPath, "DROP DATABASE", "CREATE DATABASE", "docker exec -i"} {
		if commandSent(sent, f) {
			t.Errorf("%q was sent, but nothing may change: %v", f, sent)
		}
	}
}

// --- the dry run ---

func TestRestoreGuard_DryRunPrintsEachLocationWithItsVerdictAndTheFullApplyCommand(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors()}.run(t)
	if err != nil || CLIExitCode(err) != 0 {
		t.Fatalf("a dry run is exit 0: %v", err)
	}
	for _, want := range []string{
		"s3://caltechauthors", "default-location",
		"bucket caltechauthors: WRITABLE", "restore-role",
		"--confirm caltechdata --confirm caltechauthors", "Nothing was changed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan should mention %q:\n%s", want, out)
		}
	}
	nothingChanged(t, ssmClient.sentCommands)
}

func TestRestoreGuard_DryRunOnTheSafeSetupSaysReadOnlyAndAsksForNoBucket(t *testing.T) {
	_, out, err := guardScenario{streamScan: scanOut(authorsLocationRow)}.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bucket caltechauthors: read-only") || strings.Contains(out, "WRITABLE") {
		t.Errorf("want a read-only verdict:\n%s", out)
	}
	if strings.Contains(out, "--confirm caltechdata --confirm") {
		t.Errorf("a read-only restore needs only the instance confirm:\n%s", out)
	}
}

func TestRestoreGuard_ADryRunStreamsTheBackupAndWritesNothingToTheInstance(t *testing.T) {
	ssmClient, _, err := guardScenario{streamScan: scanOut(authorsLocationRow)}.run(t)
	if err != nil {
		t.Fatal(err)
	}
	var stream string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "pipefail") {
			stream = c
		}
	}
	if !strings.Contains(stream, "s3://my-bucket/caltechdata/b-2026-09-01.sql.gz") {
		t.Errorf("the dry run must stream the chosen backup, sent: %v", ssmClient.sentCommands)
	}
}

// --- a confirmed run ---

func TestRestoreGuard_ConfirmedWithoutTheWritableBucketRefusesBeforeDownloadingOrDropping(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), confirms: []string{"caltechdata"}}.run(t)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "--confirm caltechauthors") {
		t.Fatalf("want an exit-1 refusal naming --confirm caltechauthors, got %v", err)
	}
	if !strings.Contains(out, "WRITABLE") {
		t.Errorf("the plan is shown with the refusal:\n%s", out)
	}
	nothingChanged(t, ssmClient.sentCommands)
}

func TestRestoreGuard_ConfirmedWithTheBucketRestoresAndRechecksTheDownloadBeforeDropping(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), confirms: []string{"caltechdata", "caltechauthors"}}.run(t)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	sent := ssmClient.sentCommands
	download := commandIndex(t, sent, remoteRestoreDownloadPath)
	recheck := -1
	for i, c := range sent {
		if i > download && strings.Contains(c, "files_location") && strings.Contains(c, "/var/tmp/clasm-sql-restore") && !strings.Contains(c, "pipefail") {
			recheck = i
			break
		}
	}
	drop := commandIndex(t, sent, "DROP DATABASE")
	if !(download < recheck && recheck < drop) {
		t.Errorf("want download < local re-scan < drop, got %d %d %d: %v", download, recheck, drop, sent)
	}
	if !strings.Contains(out, "--confirm caltechdata --confirm caltechauthors") || !strings.Contains(out, "Restored") {
		t.Errorf("the log line shows both confirms and the restore ran:\n%s", out)
	}
}

// The safe setup is never blocked.
func TestRestoreGuard_ReadOnlyInstanceRestoresWithTheInstanceConfirmAlone(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), confirms: []string{"caltechdata"}}.run(t)
	if err != nil {
		t.Fatalf("the read-only setup must restore: %v\n%s", err, out)
	}
	if !commandSent(ssmClient.sentCommands, "DROP DATABASE") {
		t.Errorf("the restore did not run: %v", ssmClient.sentCommands)
	}
}

func TestRestoreGuard_ConfirmThatIsNeitherTheInstanceNorAWritableBucketIsAUsageError(t *testing.T) {
	ssmClient, _, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), confirms: []string{"caltechdata", "caltechauthors", "typo-bucket"}}.run(t)
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `"typo-bucket"`) {
		t.Fatalf("want a usage error naming the stray value, got %v", err)
	}
	nothingChanged(t, ssmClient.sentCommands)
}

func TestRestoreGuard_ABucketConfirmWhenNothingIsWritableIsAUsageError(t *testing.T) {
	_, _, err := guardScenario{streamScan: scanOut(authorsLocationRow), confirms: []string{"caltechdata", "caltechauthors"}}.run(t)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("a confirm for a bucket that is not writable is a mistake, got %v", err)
	}
}

func TestRestoreGuard_ConfirmingOnlyABucketIsAUsageErrorBeforeAnyCall(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), confirms: []string{"caltechauthors"}}.run(t)
	var ue *UsageError
	if !errors.As(err, &ue) || len(ssmClient.sentCommands) != 0 || out != "" {
		t.Fatalf("want a usage error with no call, got %v, sent %v, out %q", err, ssmClient.sentCommands, out)
	}
}

// --- the locations themselves ---

func TestRestoreGuard_SeveralRowsOnOneBucketAreOneVerdictAndMixedBucketsAreJudgedApart(t *testing.T) {
	dup := strings.Replace(authorsLocationRow, "default-location", "other-name", 1)
	_, out, err := guardScenario{streamScan: scanOut(authorsLocationRow, dup, dataLocationRow, localLocationRow), decisions: writableAuthors()}.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "bucket caltechauthors:") != 1 || !strings.Contains(out, "bucket caltechdata: read-only") {
		t.Errorf("want one verdict per bucket:\n%s", out)
	}
	if !strings.Contains(out, "/var/lib/files") || !strings.Contains(out, "not an S3 bucket") {
		t.Errorf("a non-S3 location is listed and said not to be checked:\n%s", out)
	}
}

func TestRestoreGuard_ABackupWithNoFileLocationsHasNothingToGuard(t *testing.T) {
	_, out, err := guardScenario{streamScan: scanOut()}.run(t)
	if err != nil || !strings.Contains(out, "no file locations") {
		t.Fatalf("the plan should say there is nothing to guard: %v\n%s", err, out)
	}
	ssmClient, _, err := guardScenario{streamScan: scanOut(), confirms: []string{"caltechdata"}}.run(t)
	if err != nil || !commandSent(ssmClient.sentCommands, "DROP DATABASE") {
		t.Errorf("a backup with no file locations restores: %v %v", err, ssmClient.sentCommands)
	}
}

// What the guard cannot read it does not pass on, in a dry run and a confirmed run
// alike.
func TestRestoreGuard_AScanThatFailsOrAnUnreadableRowStopsEverything(t *testing.T) {
	for name, scan := range map[string]string{
		"download failed": "fatal error: An error occurred (403)\nclasm-scan-rc=1\n",
		"unreadable row":  "INSERT INTO public.files_location (name) VALUES ('n');\nclasm-scan-rc=0\n",
		"no sentinel":     authorsLocationRow + "\n",
	} {
		for _, confirms := range [][]string{nil, {"caltechdata"}} {
			ssmClient, _, err := guardScenario{streamScan: scan, confirms: confirms}.run(t)
			if err == nil || CLIExitCode(err) != 1 {
				t.Errorf("%s (confirms %v): want an exit-1 error, got %v", name, confirms, err)
			}
			nothingChanged(t, ssmClient.sentCommands)
		}
	}
}

// The scan and the download are two reads of the same object. If the downloaded
// file names a bucket the scan did not, nothing is dropped.
func TestRestoreGuard_ABucketTheDownloadedFileNamesButTheScanDidNotStopsBeforeTheDrop(t *testing.T) {
	ssmClient, _, err := guardScenario{
		streamScan: scanOut(dataLocationRow), localScan: scanOut(dataLocationRow, authorsLocationRow), confirms: []string{"caltechdata"},
	}.run(t)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "caltechauthors") || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("want a refusal naming the new bucket, got %v", err)
	}
	for _, f := range []string{"DROP DATABASE", "CREATE DATABASE", "docker exec -i"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q was sent after the re-scan disagreed", f)
		}
	}
}

func TestRestoreGuard_ARecheckThatCannotRunStopsBeforeTheDrop(t *testing.T) {
	ssmFake := restoreSQLFake(restoreSQLDiscovery, "1\n", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	ssmFake.responses = append([]ssmCommandResponse{
		{substring: "/var/tmp/clasm-sql-restore", stdout: "boom", status: types.CommandInvocationStatusFailed},
		{substring: "pipefail", stdout: scanOut(dataLocationRow), status: types.CommandInvocationStatusSuccess},
	}, ssmFake.responses...)
	fx := newWriteFixture(nil)
	guard := RestoreGuardClients{EC2: map[string]awsclient.EC2API{"us-east-1": fx.ec2}, IAM: fx.iam}
	buf := &bytes.Buffer{}
	err := RunRestoreSQLBackupAuto(context.Background(), buf, ssmFake, &fakeS3Client{allObjects: sqlBackupObjects("caltechdata", 10, "b.sql.gz")}, restoreSQLInst, guard, nil, nil, restoreSQLParams(), []string{"caltechdata"}, false, failingReader{t}, buf)
	if err == nil || commandSent(ssmFake.sentCommands, "DROP DATABASE") {
		t.Fatalf("a failed re-scan must stop the restore before the drop: %v", err)
	}
}

// --- the terminal ---

func TestRestoreGuard_TerminalAsksForTheInstanceThenTheWritableBucket(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), interactive: true, typed: "caltechdata\ncaltechauthors\n"}.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !commandSent(ssmClient.sentCommands, "DROP DATABASE") || !strings.Contains(out, "This instance can write to bucket caltechauthors") {
		t.Errorf("the typed answers must restore, after a warning:\n%s", out)
	}
}

func TestRestoreGuard_TerminalWrongBucketAnswerChangesNothing(t *testing.T) {
	ssmClient, out, err := guardScenario{streamScan: scanOut(authorsLocationRow), decisions: writableAuthors(), interactive: true, typed: "caltechdata\nnope\n"}.run(t)
	if err != nil || !strings.Contains(out, "Cancelled") {
		t.Fatalf("declining is not an error: %v\n%s", err, out)
	}
	nothingChanged(t, ssmClient.sentCommands)
}

// --- the guard clients ---

func TestRestoreGuard_MissingClientsIsAnErrorNotASilentlyUnguardedRestore(t *testing.T) {
	buf := &bytes.Buffer{}
	ssmFake := restoreSQLFake(restoreSQLDiscovery, "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	err := RunRestoreSQLBackupAuto(context.Background(), buf, ssmFake,
		&fakeS3Client{}, restoreSQLInst, RestoreGuardClients{}, nil, nil, restoreSQLParams(), nil, false, failingReader{t}, buf)
	if err == nil || !strings.Contains(err.Error(), "guard") {
		t.Fatalf("want an error about the guard's clients, got %v", err)
	}
	if len(ssmFake.sentCommands) != 0 {
		t.Errorf("the missing clients must be found before any call: %v", ssmFake.sentCommands)
	}
}

// --- the menu flow ---

func TestRestoreGuard_MenuFlowWarnsThenAsksToContinueOrAbort(t *testing.T) {
	for answer, wantRestore := range map[string]bool{"y\n": true, "n\n": false} {
		term, in, buf := newPipeEditor("my-bucket\ncaltechdata\n\n" + answer)
		ssmFake := restoreSQLFake(restoreSQLDiscovery, "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
		ssmFake.responses = append([]ssmCommandResponse{
			{substring: "/var/tmp/clasm-sql-restore", stdout: scanOut(authorsLocationRow), status: types.CommandInvocationStatusSuccess},
			{substring: "pipefail", stdout: scanOut(authorsLocationRow), status: types.CommandInvocationStatusSuccess},
			{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: "clasm-env=absent\n", status: types.CommandInvocationStatusSuccess},
		}, ssmFake.responses...)
		fx := newWriteFixture(writableAuthors())
		guard := RestoreGuardClients{EC2: map[string]awsclient.EC2API{"us-east-1": fx.ec2}, IAM: fx.iam}
		s3Client := &fakeS3Client{allObjects: sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz")}
		err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmFake}, s3Client, sameS3Client(s3Client), restoreSQLInst, guard, nil, nil, in, buf)
		if err != nil {
			t.Fatalf("answer %q: %v\n%s", answer, err, buf.String())
		}
		out := buf.String()
		if !strings.Contains(out, "WARNING") || !strings.Contains(out, "caltechauthors") || !strings.Contains(out, "restore-role") {
			t.Errorf("answer %q: want a warning naming the bucket and how it is writable:\n%s", answer, out)
		}
		if got := commandSent(ssmFake.sentCommands, "DROP DATABASE"); got != wantRestore {
			t.Errorf("answer %q: restore ran = %v, want %v\n%s", answer, got, wantRestore, out)
		}
		if !wantRestore && !strings.Contains(out, "Cancelled") {
			t.Errorf("an aborted restore says Cancelled:\n%s", out)
		}
	}
}

func TestRestoreGuard_MenuFlowOnTheSafeSetupDoesNotAsk(t *testing.T) {
	term, in, buf := newPipeEditor("my-bucket\ncaltechdata\n\n") // no extra answer: a prompt would hit end of input
	ssmFake := restoreSQLFake(restoreSQLDiscovery, "", types.CommandInvocationStatusSuccess, types.CommandInvocationStatusSuccess)
	ssmFake.responses = append([]ssmCommandResponse{
		{substring: "/var/tmp/clasm-sql-restore", stdout: scanOut(authorsLocationRow), status: types.CommandInvocationStatusSuccess},
		{substring: "pipefail", stdout: scanOut(authorsLocationRow), status: types.CommandInvocationStatusSuccess},
		{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: "clasm-env=absent\n", status: types.CommandInvocationStatusSuccess},
	}, ssmFake.responses...)
	fx := newWriteFixture(nil)
	guard := RestoreGuardClients{EC2: map[string]awsclient.EC2API{"us-east-1": fx.ec2}, IAM: fx.iam}
	s3Client := &fakeS3Client{allObjects: sqlBackupObjects("caltechdata", 10, "b-2026-09-01.sql.gz")}
	if err := restoreSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmFake}, s3Client, sameS3Client(s3Client), restoreSQLInst, guard, nil, nil, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if !commandSent(ssmFake.sentCommands, "DROP DATABASE") || strings.Contains(buf.String(), "WARNING") || !strings.Contains(buf.String(), "read-only") {
		t.Errorf("the safe setup restores without a warning:\n%s", buf.String())
	}
}
