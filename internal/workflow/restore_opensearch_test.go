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
	input := "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No OpenSearch snapshots found") {
		t.Errorf("expected a no-snapshots message, got:\n%s", buf.String())
	}
}

func TestRestoreOpenSearchSnapshot_HappyPathNoExistingIndices(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n" // directory, bucket, source name, pick the (only) snapshot
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
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "DELETE 'localhost:9200/") {
			t.Errorf("did not expect a delete-indices call with no conflicting indices, sent: %v", ssmClient.sentCommands)
		}
	}
}

func TestRestoreOpenSearchSnapshot_ConflictingIndicesRequireConfirmDestructive_DeclinedCancelsBeforeAnyS3Activity(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "wrong-name\n" // decline the type-to-confirm -- no other input should even be consumed
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
	input := "i-1\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
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
	var sawDelete bool
	for _, sent := range ssmClient.sentCommands {
		if strings.Contains(sent, "DELETE 'localhost:9200/") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected a delete-indices call, sent: %v", ssmClient.sentCommands)
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
	input := "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
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
