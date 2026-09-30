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

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestBuildSyncFromS3Command(t *testing.T) {
	got := buildSyncFromS3Command("my-bucket", "caltechdata", "rdm-20260819-160031", "/opt/rdm_opensearch_backups")
	for _, want := range []string{
		"aws s3 sync --only-show-errors",
		"s3://my-bucket/caltechdata/opensearch-snapshots/rdm-20260819-160031/",
		"/opt/rdm_opensearch_backups",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

// The gid is the service user's own, not the container's: on the current
// images ubuntu is 1000:1001 and gid 1000 is the docker group, which is
// what the old hardcoded 1000:1000 was quietly handing the repository to.
func TestBuildChownTreeCommand(t *testing.T) {
	got := buildChownTreeCommand("/opt/rdm_opensearch_backups", ServiceOwner{UID: 1000, GID: 1001})
	want := "chown -R 1000:1001 '/opt/rdm_opensearch_backups'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The directory is operator-typed (the workflow's own backup-directory
// prompt), so it has to survive a space the same way every other
// SSM-bound path in this package does.
func TestBuildChownTreeCommand_QuotesDirectory(t *testing.T) {
	got := buildChownTreeCommand("/opt/rdm opensearch backups", ServiceOwner{UID: 1000, GID: 1001})
	want := "chown -R 1000:1001 '/opt/rdm opensearch backups'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeSnapshotRepoOwnership_SendsChown(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	if err := NormalizeSnapshotRepoOwnership(context.Background(), fake, "i-1", "/opt/rdm_opensearch_backups", ServiceOwner{UID: 1000, GID: 1001}, time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 1 {
		t.Fatalf("expected exactly 1 SendCommand call, got %d", fake.sendCommandCalls())
	}
	if want := "chown -R 1000:1001 '/opt/rdm_opensearch_backups'"; fake.lastCommandText != want {
		t.Errorf("sent %q, want %q", fake.lastCommandText, want)
	}
}

// A chown that reports a non-Success status must surface the remote
// output. This is the step whose silent absence left caltechauthors-v13
// unable to archive, so it must never fail quietly.
func TestNormalizeSnapshotRepoOwnership_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "chown: cannot access"}
	err := NormalizeSnapshotRepoOwnership(context.Background(), fake, "i-1", "/opt/rdm_opensearch_backups", ServiceOwner{UID: 1000, GID: 1001}, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "chown: cannot access") {
		t.Errorf("expected an error including the remote output, got: %v", err)
	}
}

func TestNormalizeSnapshotRepoOwnership_PropagatesTransportError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errors.New("network is unreachable")}
	err := NormalizeSnapshotRepoOwnership(context.Background(), fake, "i-1", "/opt/rdm_opensearch_backups", ServiceOwner{UID: 1000, GID: 1001}, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "network is unreachable") {
		t.Errorf("expected the transport error to propagate, got: %v", err)
	}
}

func TestBuildListIndicesCommand(t *testing.T) {
	got := buildListIndicesCommand("caltechdata")
	want := "curl --fail-with-body -sS -X GET 'localhost:9200/_cat/indices/caltechdata-*,.ds-caltechdata-*?h=index'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMatchesAnyPattern(t *testing.T) {
	patterns := rdmOpenSearchSnapshotIndexPatterns("caltechdata")
	tests := []struct {
		name string
		want bool
	}{
		{"caltechdata-rdmrecords-records-record-v7.0.0", true},
		{"caltechdata-stats-bookmarks", true},
		{".ds-caltechdata-auditlog-audit-log-000001", true},
		{"caltechdata-auditlog-audit-log-v1.0.0", true}, // the non-data-stream form a restored instance creates
		{"caltechdata-job-logs", true},
		{".ds-caltechdata-job-logs-000001", true},
		{"caltechdata-events-stats-file-download-2025-09", false}, // deliberately excluded, see rdmOpenSearchSnapshotIndexPatterns
		{"unrelated-index", false},
	}
	for _, tt := range tests {
		if got := matchesAnyPattern(tt.name, patterns); got != tt.want {
			t.Errorf("matchesAnyPattern(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseListedIndices(t *testing.T) {
	got := parseListedIndices("caltechdata-rdmrecords-records-record-v7.0.0\ncaltechdata-users-user-v3.0.0\n\n")
	want := []string{"caltechdata-rdmrecords-records-record-v7.0.0", "caltechdata-users-user-v3.0.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDetectExistingOpenSearchIndices_FiltersToCuratedPatterns(t *testing.T) {
	patterns := rdmOpenSearchSnapshotIndexPatterns("caltechdata")
	fake := &fakeSSMClient{
		commandID:   "cmd-1",
		finalStatus: types.CommandInvocationStatusSuccess,
		stdout:      "caltechdata-rdmrecords-records-record-v7.0.0\ncaltechdata-events-stats-file-download-2025-09\ncaltechdata-users-user-v3.0.0\n",
	}
	got, err := detectExistingOpenSearchIndices(context.Background(), fake, "i-1", "caltechdata", patterns, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 matches (events-stats excluded, not a curated pattern)", got)
	}
}

func TestDetectExistingOpenSearchIndices_SSMFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "boom"}
	_, err := detectExistingOpenSearchIndices(context.Background(), fake, "i-1", "caltechdata", nil, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestBuildDeleteIndicesCommand(t *testing.T) {
	got := buildDeleteIndicesCommand([]string{"caltechdata-rdmrecords-a", "caltechdata-rdmrecords-b"})
	want := "curl --fail-with-body -sS -X DELETE 'localhost:9200/caltechdata-rdmrecords-a,caltechdata-rdmrecords-b'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeleteConflictingIndices_NoopWhenEmpty(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1"}
	if err := DeleteConflictingIndices(context.Background(), fake, "i-1", nil, time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 0 {
		t.Errorf("expected no SendCommand calls for an empty index list, got %d", fake.sendCommandCalls())
	}
}

func TestDeleteConflictingIndices_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "boom"}
	err := DeleteConflictingIndices(context.Background(), fake, "i-1", []string{"a"}, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected an error including the response body, got: %v", err)
	}
}

func TestBuildRestoreSnapshotCommand(t *testing.T) {
	got := buildRestoreSnapshotCommand("rdm_backup_repo", "rdm-20260819-160031", []string{"caltechdata-rdmrecords-*", "caltechdata-users-*"})
	for _, want := range []string{
		"curl --fail-with-body -sS -X POST",
		"localhost:9200/_snapshot/rdm_backup_repo/rdm-20260819-160031/_restore",
		"caltechdata-rdmrecords-*,caltechdata-users-*",
		`"ignore_unavailable":true`,
		`"include_global_state":false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

func TestRestoreSnapshot_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "boom"}
	err := RestoreSnapshot(context.Background(), fake, "i-1", "rdm_backup_repo", "rdm-1", []string{"a-*"}, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected an error including the response body, got: %v", err)
	}
}

func TestBuildRestoreRecoveryCommand(t *testing.T) {
	got := buildRestoreRecoveryCommand([]string{"caltechdata-rdmrecords-*", "caltechdata-users-*"})
	want := "curl --fail-with-body -sS -X GET 'localhost:9200/_cat/recovery/caltechdata-rdmrecords-*,caltechdata-users-*?h=index,type,stage'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseRestoreRecovery(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantDone bool
		wantErr  bool
	}{
		{"no rows yet, not started", "", false, false},
		{"peer recovery only, no snapshot row yet", "caltechdata-users-a peer done\n", false, false},
		{"snapshot recovery in progress", "caltechdata-rdmrecords-a snapshot index\n", false, false},
		{"snapshot recovery done", "caltechdata-rdmrecords-a snapshot done\n", true, false},
		{"one done one still in progress", "caltechdata-rdmrecords-a snapshot done\ncaltechdata-users-a snapshot index\n", false, false},
		{"all done across multiple rows", "caltechdata-rdmrecords-a snapshot done\ncaltechdata-users-a snapshot done\n", true, false},
		{"malformed row", "not-enough-fields\n", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, err := parseRestoreRecovery(tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
		})
	}
}

func TestPollRestoreUntilComplete_SucceedsAfterInProgress(t *testing.T) {
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{"", "a snapshot index\n", "a snapshot done\n"},
	}
	err := PollRestoreUntilComplete(context.Background(), io.Discard, fake, "i-1", []string{"a-*"}, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPollRestoreUntilComplete_NeverCompletingSequenceTimesOut(t *testing.T) {
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{"a snapshot index\n"},
	}
	err := PollRestoreUntilComplete(context.Background(), io.Discard, fake, "i-1", []string{"a-*"}, 30*time.Millisecond, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected a timeout error, got: %v", err)
	}
}

func TestPollRestoreUntilComplete_PrintsProgressWhileWaiting(t *testing.T) {
	var buf bytes.Buffer
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{"a snapshot index\n", "a snapshot index\n", "a snapshot done\n"},
	}
	err := PollRestoreUntilComplete(context.Background(), &buf, fake, "i-1", []string{"a-*"}, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "waiting for") {
		t.Errorf("output = %q, want an initial waiting message", buf.String())
	}
}

func TestBuildRestoreVerificationCommand(t *testing.T) {
	got := buildVerifyRestoredIndicesCommand([]string{"caltechdata-rdmrecords-*"})
	want := "curl --fail-with-body -sS -X GET 'localhost:9200/_cat/indices/caltechdata-rdmrecords-*?h=index,health,status,docs.count'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseRestoredIndices(t *testing.T) {
	body := "caltechdata-rdmrecords-records-record-v7.0.0  yellow open  147456\ncaltechdata-rdmrecords-drafts-draft-v6.0.0    red    open       0\n"
	got, err := parseRestoredIndices(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Index != "caltechdata-rdmrecords-records-record-v7.0.0" || got[0].Health != "yellow" || got[0].DocsCount != 147456 {
		t.Errorf("row 0 = %+v, unexpected", got[0])
	}
	if got[1].Health != "red" {
		t.Errorf("row 1 health = %q, want red", got[1].Health)
	}
}

func TestParseRestoredIndices_MalformedRow(t *testing.T) {
	_, err := parseRestoredIndices("not enough fields\n")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestVerifyRestoredIndices_SSMFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "boom"}
	_, err := VerifyRestoredIndices(context.Background(), fake, "i-1", []string{"a-*"}, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected an error including the response body, got: %v", err)
	}
}

// --- restoreOpenSearchSnapshot (testable core) integration tests ---

// restoreOpenSearchFake mirrors restoreSQLFake's shape (restore_sql_test.go):
// matches restoreOpenSearchSnapshot's sequence of remote commands by
// substring.
func restoreOpenSearchFake(existingIndicesStdout, recoveryStdout, verifyStdout string) *fakeSSMClient {
	return &fakeSSMClient{
		commandID:   "cmd-1",
		finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", stdout: "/usr/bin/aws", status: types.CommandInvocationStatusSuccess},
			{substring: "id -u", stdout: "1000 1001\n", status: types.CommandInvocationStatusSuccess}, // the service owner lookup: ubuntu is uid 1000, gid 1001
			{substring: "_cat/indices/caltechdata-*,.ds-caltechdata-*", stdout: existingIndicesStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "_cat/indices/caltechdata-rdmrecords", stdout: verifyStdout, status: types.CommandInvocationStatusSuccess},
			{substring: "DELETE 'localhost:9200/", status: types.CommandInvocationStatusSuccess},
			{substring: "aws s3 sync", status: types.CommandInvocationStatusSuccess},
			{substring: "_snapshot/rdm_backup_repo", stdout: "", status: types.CommandInvocationStatusSuccess}, // register + restore + _restore
			{substring: "_cat/recovery", stdout: recoveryStdout, status: types.CommandInvocationStatusSuccess},
		},
	}
}

func oneOpenSearchSnapshotObject(sourceName, snapshotName string) []s3types.Object {
	key := sourceName + "/opensearch-snapshots/" + snapshotName + "/index-0"
	return []s3types.Object{{Key: aws.String(key), Size: aws.Int64(1024), LastModified: aws.Time(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC))}}
}

func TestRestoreOpenSearchSnapshot_NoSnapshotsFoundUnderPrefix(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" // accept index-prefix default, directory, bucket, source name
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{}

	// An empty source is an error, not a quiet success: nothing was restored.
	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil || !strings.Contains(err.Error(), "no OpenSearch snapshots found") {
		t.Errorf("expected a no-snapshots error, got: %v", err)
	}
}

func TestRestoreOpenSearchSnapshot_HappyPathNoExistingIndices(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n" // index-prefix default, directory, bucket, source name, pick the (only) snapshot
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored OpenSearch snapshot") {
		t.Errorf("expected a success report, got:\n%s", buf.String())
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("did not expect a delete-indices call with no conflicting indices, sent: %v", ssmClient.sentCommands)
	}
}

// TestRestoreOpenSearchSnapshot_IndexPrefixPromptOverridesTargetTagDefault
// is the regression test for correction 5 (DECISIONS.md, "Restore
// OpenSearch Snapshot from S3: a fifth correction -- the restore index
// prefix must be editable, not silently derived from the target's own
// tags"): the target instance's own tag-derived prefix
// ("caltechdata-restore-test") must NOT be what the conflict-check/
// restore/verify commands use once the operator overrides the new
// index-prefix prompt to the snapshot's own real prefix ("caltechdata")
// -- a cross-instance restore scenario this project's own real-AWS test
// against caltechdata-restore-test needs.
func TestRestoreOpenSearchSnapshot_IndexPrefixPromptOverridesTargetTagDefault(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata-restore-test", Region: "us-east-1"}
	input := "caltechdata\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "new-data\n" + "\n" // override index-prefix, directory, bucket, source name, pick
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("new-data", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored OpenSearch snapshot") {
		t.Errorf("expected a success report, got:\n%s", buf.String())
	}
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "caltechdata-restore-test") {
			t.Errorf("sent command used the target's own tag-derived prefix instead of the overridden one: %q", sent)
		}
	}
}

func TestRestoreOpenSearchSnapshot_ConflictingIndicesRequireConfirmDestructive_DeclinedCancelsBeforeAnyS3Activity(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "wrong-name\n" // accept index-prefix default, then decline the type-to-confirm -- no other input should even be consumed
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancellation message, got:\n%s", buf.String())
	}
	if len(s3Client.listObjectsV2Calls) != 0 {
		t.Errorf("expected zero S3 calls before the declined confirmation, got: %+v", s3Client.listObjectsV2Calls)
	}
}

func TestRestoreOpenSearchSnapshot_ConflictingIndicesConfirmedDeletesThenProceeds(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "i-1\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n" // index-prefix default, confirm, directory, bucket, source name, pick
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restored OpenSearch snapshot") {
		t.Errorf("expected a success report, got:\n%s", buf.String())
	}
	if !deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("expected a delete-indices call, sent: %v", ssmClient.sentCommands)
	}
}

// commandIndex returns the position of the first sent SSM command
// containing want, failing the test if none does.
func commandIndex(t *testing.T, sent []string, want string) int {
	t.Helper()
	for i, s := range sent {
		if strings.Contains(s, want) {
			return i
		}
	}
	t.Fatalf("no sent command contains %q; sent: %v", want, sent)
	return -1
}

func commandSent(sent []string, want string) bool {
	for _, s := range sent {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// isDeleteIndicesCommand distinguishes a conflicting-index deletion from
// the two _snapshot/ DELETEs the post-verification cleanup issues (the
// snapshot itself, then the repository registration). Both are DELETEs
// against localhost:9200, so a bare "DELETE 'localhost:9200/" substring
// no longer identifies index deletion: since Phase 20.63 that match is
// true on every successful restore, which would make a "no index deletion
// happened" assertion fail spuriously and a "an index deletion happened"
// assertion pass without one.
func isDeleteIndicesCommand(s string) bool {
	return strings.Contains(s, "-X DELETE 'localhost:9200/") && !strings.Contains(s, "_snapshot/")
}

func deleteIndicesCommandSent(sent []string) bool {
	for _, s := range sent {
		if isDeleteIndicesCommand(s) {
			return true
		}
	}
	return false
}

// Command fragments that identify each step of a restore in the order the
// fake SSM client recorded them. The deregister and the snapshot delete
// are both DELETEs against _snapshot/, distinguished by whether a
// snapshot name follows the repo: the trailing quote in
// deregisterRepoCmdFragment is load-bearing.
const (
	syncCmdFragment           = "aws s3 sync"
	chownCmdFragment          = "chown -R 1000:1001"
	registerRepoCmdFragment   = "-X PUT 'localhost:9200/_snapshot/rdm_backup_repo'"
	deregisterRepoCmdFragment = "-X DELETE 'localhost:9200/_snapshot/rdm_backup_repo'"
	deleteSnapshotCmdFragment = "-X DELETE 'localhost:9200/_snapshot/rdm_backup_repo/rdm-20260819-160031'"
	verifyIndicesCmdFragment  = "_cat/indices/caltechdata-rdmrecords"
)

// The chown must land strictly between the sync and the registration.
// After the sync because that is what creates the root-owned files; before
// the registration because registration only ever verifies a write into
// the repository's *top-level* directory -- the one place the path.repo
// retrofit's own chown already reached -- so registering first would
// succeed against a broken tree and destroy the signal (DR-0175).
func TestRestoreOpenSearchSnapshot_ChownsBetweenSyncAndRegister(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := ssmClient.sentCommands
	sync := commandIndex(t, sent, syncCmdFragment)
	chown := commandIndex(t, sent, chownCmdFragment)
	register := commandIndex(t, sent, registerRepoCmdFragment)
	if !(sync < chown && chown < register) {
		t.Errorf("want sync < chown < register, got sync=%d chown=%d register=%d; sent: %v", sync, chown, register, sent)
	}
	if want := "chown -R 1000:1001 '/opt/rdm_opensearch_backups'"; !commandSent(sent, want) {
		t.Errorf("expected the chown to target the operator's own directory (%q); sent: %v", want, sent)
	}
}

// The owner is looked up on the instance, not assumed: a chown to the
// hardcoded 1000:1000 is what made the repository ubuntu:docker.
func TestRestoreOpenSearchSnapshot_ChownsToTheResolvedOwner(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	for i := range ssmClient.responses {
		if ssmClient.responses[i].substring == "id -u" {
			ssmClient.responses[i].stdout = "1000 1234\n"
		}
	}
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "chown -R 1000:1234 '/opt/rdm_opensearch_backups'"; !commandSent(ssmClient.sentCommands, want) {
		t.Errorf("expected the chown to use the looked-up gid (%q); sent: %v", want, ssmClient.sentCommands)
	}
}

const openSearchEnsureExact = "install -d -o 1000 -g 1001 -m 0775 '/opt/rdm_opensearch_backups'"

func setRestoreOwnerStdout(f *fakeSSMClient, stdout string) {
	for i := range f.responses {
		if f.responses[i].substring == "id -u" {
			f.responses[i].stdout = stdout
			return
		}
	}
	panic("no id -u response scripted")
}

func countCommandsContaining(sent []string, fragment string) int {
	n := 0
	for _, c := range sent {
		if strings.Contains(c, fragment) {
			n++
		}
	}
	return n
}

// DR-0176 decision 5: when the service user's uid is not the container's,
// no owner can serve both, so stop *before anything is written or destroyed*.
// The fixture has a conflicting index, so the deletion is reachable and a
// mismatch found late would already have destroyed it: the failure this
// test exists to prevent (never destroy the current state before a verified
// replacement). Nothing after the lookup may be sent -- no index deletion,
// no ensure, no sync, no chown, no register, no restore.
func TestRestoreOpenSearchSnapshot_OwnerUIDMismatchStopsBeforeAnythingIsDestroyed(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	// If the workflow wrongly carried on, these answers would drive it
	// through the confirm and the whole restore.
	input := "\n" + "i-1\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	setRestoreOwnerStdout(ssmClient, "1001 1001\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("expected an error naming both uids, got: %v", err)
	}
	sent := ssmClient.sentCommands
	if deleteIndicesCommandSent(sent) {
		t.Errorf("an index was deleted before the uid mismatch was reported; sent: %v", sent)
	}
	for _, fragment := range []string{"install -d", syncCmdFragment, "chown -R", registerRepoCmdFragment, "_restore"} {
		if commandSent(sent, fragment) {
			t.Errorf("a command containing %q was sent after a uid mismatch; sent: %v", fragment, sent)
		}
	}
}

// The full order of a restore that has to delete conflicting indices: the
// owner is known first, then the directory is ensured, the snapshot synced,
// chowned and registered, and only then are the conflicting indices deleted,
// immediately before the _restore that replaces them. The deletion used to
// come right after the index-prefix prompt, before the source was even
// chosen, so a mistyped source destroyed the live indices and then found
// nothing to restore (found live 2026-09-30; DR-0175 decision 2). The lookup
// happens once -- at the top, not duplicated beside the chown.
func TestRestoreOpenSearchSnapshot_DeletionComesAfterSyncAndRegisterBeforeRestore(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "i-1\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	lookup := commandIndex(t, sent, "id -u")
	del := -1
	for i, c := range sent {
		if isDeleteIndicesCommand(c) {
			del = i
			break
		}
	}
	ensure := commandIndex(t, sent, openSearchEnsureExact)
	sync := commandIndex(t, sent, syncCmdFragment)
	chown := commandIndex(t, sent, chownCmdFragment)
	register := commandIndex(t, sent, registerRepoCmdFragment)
	if del < 0 {
		t.Fatalf("fixture should have deleted a conflicting index; sent: %v", sent)
	}
	restore := commandIndex(t, sent, "/_restore")
	if !(lookup < ensure && ensure < sync && sync < chown && chown < register && register < del && del < restore) {
		t.Errorf("want lookup < ensure < sync < chown < register < delete < restore, got %d %d %d %d %d %d %d; sent: %v", lookup, ensure, sync, chown, register, del, restore, sent)
	}
	if n := countCommandsContaining(sent, "id -u"); n != 1 {
		t.Errorf("the owner was looked up %d times, want exactly 1", n)
	}
}

// The ensure step is create-or-repair of the directory itself and must not
// recurse: the restore's own chown -R, after the sync, is the only
// recursive step, and it is asserted separately.
func TestRestoreOpenSearchSnapshot_EnsureIsNonRecursive(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !commandSent(ssmClient.sentCommands, "install -d") {
		t.Fatalf("no ensure step was sent, so there is nothing to check; sent: %v", ssmClient.sentCommands)
	}
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "install -d") && (strings.Contains(c, "-R") || strings.Contains(c, "chown")) {
			t.Errorf("the ensure step recursed or chowned: %q", c)
		}
	}
}

// A directory that cannot be made or repaired means the sync would write
// into the wrong place: stop before it.
func TestRestoreOpenSearchSnapshot_FailedEnsureAbortsBeforeSync(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	ssmClient.responses = append([]ssmCommandResponse{{substring: "install -d", stdout: "install: cannot change owner", status: types.CommandInvocationStatusFailed}}, ssmClient.responses...)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil || !strings.Contains(err.Error(), "/opt/rdm_opensearch_backups") {
		t.Fatalf("expected an error naming the directory, got: %v", err)
	}
	for _, f := range []string{syncCmdFragment, "chown -R", registerRepoCmdFragment} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q sent after a failed ensure; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

// Cleanup is the mirror of Archive's own post-verify delete: the snapshot
// goes through the OpenSearch API (DR-0131), then the repository is
// deregistered, leaving the directory empty and owned by uid 1000 and the
// instance ready to archive.
func TestRestoreOpenSearchSnapshot_CleansUpAfterVerification(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := ssmClient.sentCommands
	verify := commandIndex(t, sent, verifyIndicesCmdFragment)
	deleteSnap := commandIndex(t, sent, deleteSnapshotCmdFragment)
	deregister := commandIndex(t, sent, deregisterRepoCmdFragment)
	if !(verify < deleteSnap && deleteSnap < deregister) {
		t.Errorf("want verify < delete-snapshot < deregister, got verify=%d delete=%d deregister=%d; sent: %v", verify, deleteSnap, deregister, sent)
	}
}

// TestRestoreOpenSearchSnapshot_FailedVerificationLeavesTheRepositoryAlone
// is the test that carries DR-0175's governing principle: never destroy
// the current state before a verified replacement exists. An earlier draft
// of the design also cleared the repository directory *before* the sync,
// which would leave a failed sync with neither the old lineage nor a
// restore; that was rejected at review. This asserts the shape the review
// settled on, so a later refactor cannot quietly reintroduce it -- if
// verification fails, nothing is deleted and nothing is deregistered.
func TestRestoreOpenSearchSnapshot_FailedVerificationLeavesTheRepositoryAlone(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "")
	// Make the post-restore verification itself fail.
	for i, r := range ssmClient.responses {
		if r.substring == "_cat/indices/caltechdata-rdmrecords" {
			ssmClient.responses[i].status = types.CommandInvocationStatusFailed
			ssmClient.responses[i].stdout = "boom"
		}
	}
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil {
		t.Fatal("expected the verification failure to propagate")
	}

	sent := ssmClient.sentCommands
	if commandSent(sent, deleteSnapshotCmdFragment) {
		t.Errorf("snapshot was deleted despite an unverified restore; sent: %v", sent)
	}
	if commandSent(sent, deregisterRepoCmdFragment) {
		t.Errorf("repository was deregistered despite an unverified restore; sent: %v", sent)
	}
	// The chown is not destructive and must still have happened -- it is
	// the repair, not the cleanup.
	if !commandSent(sent, chownCmdFragment) {
		t.Errorf("expected the chown to have run before the failure; sent: %v", sent)
	}
}

// TestRestoreOpenSearchSnapshot_ConflictDetectionAbortsBeforeAnyS3Activity is
// the OpenSearch-restore analog of Restore SQL Backup's own step-order
// regression test (DECISIONS.md, "Restore SQL Backup: resolve the Postgres
// target before any S3 prompt, not after"; PLAN.md Phase 20.50) -- applied
// here from the start rather than needing a second live-testing round to
// rediscover the same lesson (DECISIONS.md, "Restore OpenSearch: detect and
// resolve conflicting indices before any S3 activity, applying the SQL
// restore lesson from the start"). Existing-index conflict detection only
// needs the target instance's own index-prefix (Project/Name tag), not any
// S3 bucket/source-name/snapshot choice, so it runs first.
func TestRestoreOpenSearchSnapshot_ConflictDetectionAbortsBeforeAnyS3Activity(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	term, le, buf := newPipeEditor("") // no directory/bucket/source-name input available at all
	ssmClient := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errors.New("boom")}
	s3Client := &fakeS3Client{}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil {
		t.Fatal("expected an error from the failed existing-indices check")
	}
	if len(s3Client.listObjectsV2Calls) != 0 {
		t.Errorf("expected zero S3 calls before the discovery failure, got: %+v", s3Client.listObjectsV2Calls)
	}
}

func TestRestoreOpenSearchSnapshot_ReportsRedHealthWarning(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a red open 0\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "WARNING") || !strings.Contains(buf.String(), "red") {
		t.Errorf("expected a red-health warning, got:\n%s", buf.String())
	}
}

func TestRestoreOpenSearchSnapshot_CLIUnavailableAbortsBeforeAnyPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	term, le, buf := newPipeEditor("")
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	s3Client := &fakeS3Client{}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil {
		t.Fatal("expected a CLI-unavailable error")
	}
	if len(s3Client.listObjectsV2Calls) != 0 {
		t.Errorf("expected zero S3 calls before the CLI-availability failure, got: %+v", s3Client.listObjectsV2Calls)
	}
}
