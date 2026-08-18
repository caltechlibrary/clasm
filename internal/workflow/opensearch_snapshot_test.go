package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestBuildRegisterRepoCommand(t *testing.T) {
	got := buildRegisterRepoCommand("rdm_backup_repo", "/opt/rdm_opensearch_backups")
	for _, want := range []string{"curl --fail-with-body -sS -X PUT", "localhost:9200/_snapshot/rdm_backup_repo", `"type":"fs"`, `"location":"/opt/rdm_opensearch_backups"`} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

func TestBuildCreateSnapshotCommand(t *testing.T) {
	got := buildCreateSnapshotCommand("rdm_backup_repo", "rdm-20260729-000000", []string{"caltechauthors-rdmrecords-*", "caltechauthors-users-*"})
	for _, want := range []string{
		"curl --fail-with-body -sS -X PUT",
		"localhost:9200/_snapshot/rdm_backup_repo/rdm-20260729-000000",
		"caltechauthors-rdmrecords-*,caltechauthors-users-*",
		`"ignore_unavailable":true`,
		`"include_global_state":false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

func TestBuildSnapshotStateCommand(t *testing.T) {
	got := buildSnapshotStateCommand("rdm_backup_repo", "rdm-20260729-000000")
	want := "curl --fail-with-body -sS -X GET 'localhost:9200/_snapshot/rdm_backup_repo/rdm-20260729-000000'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDeleteSnapshotCommand(t *testing.T) {
	got := buildDeleteSnapshotCommand("rdm_backup_repo", "rdm-20260729-000000")
	want := "curl --fail-with-body -sS -X DELETE 'localhost:9200/_snapshot/rdm_backup_repo/rdm-20260729-000000'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseSnapshotState(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"success", `{"snapshots":[{"snapshot":"rdm-1","state":"SUCCESS"}]}`, "SUCCESS", false},
		{"in progress", `{"snapshots":[{"snapshot":"rdm-1","state":"IN_PROGRESS"}]}`, "IN_PROGRESS", false},
		{"failed", `{"snapshots":[{"snapshot":"rdm-1","state":"FAILED"}]}`, "FAILED", false},
		{"partial", `{"snapshots":[{"snapshot":"rdm-1","state":"PARTIAL"}]}`, "PARTIAL", false},
		{"malformed", `not json`, "", true},
		{"no snapshots", `{"snapshots":[]}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSnapshotState([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterSnapshotRepo_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := RegisterSnapshotRepo(context.Background(), fake, "i-1", "rdm_backup_repo", "/opt/rdm_opensearch_backups", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed SSM status")
	}
}

// TestRegisterSnapshotRepo_ErrorIncludesResponseBody is a regression
// test for a real incident (2026-07-29, CaltechAUTHORS production,
// i-0c4c81336aea33d27): `curl -fsS` suppresses the HTTP response body on
// a server error, so a failed registration surfaced only curl's own
// generic "exit status 22" -- OpenSearch's actual error (almost always
// the `path.repo` prerequisite not being configured yet) was invisible
// without manually inspecting the --debug JSONL log. `--fail-with-body`
// (curl >= 7.76, present on every Ubuntu LTS this project targets)
// preserves the body on stdout even on a non-2xx response; the error
// message here must surface it.
func TestRegisterSnapshotRepo_ErrorIncludesResponseBody(t *testing.T) {
	body := `{"error":{"root_cause":[{"type":"repository_exception","reason":"[rdm_backup_repo] location [/opt/rdm_opensearch_backups] doesn't match any of the locations specified by path.repo"}]},"status":500}`
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: body}
	err := RegisterSnapshotRepo(context.Background(), fake, "i-1", "rdm_backup_repo", "/opt/rdm_opensearch_backups", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed SSM status")
	}
	if !strings.Contains(err.Error(), "path.repo") {
		t.Errorf("error = %v, want it to include OpenSearch's own response body (e.g. mentioning path.repo)", err)
	}
}

func TestCreateSnapshot_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := CreateSnapshot(context.Background(), fake, "i-1", "rdm_backup_repo", "rdm-1", []string{"caltechauthors-rdmrecords-*"}, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed SSM status")
	}
}

func TestCreateSnapshot_ErrorIncludesResponseBody(t *testing.T) {
	body := `{"error":{"reason":"no such index"},"status":404}`
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: body}
	err := CreateSnapshot(context.Background(), fake, "i-1", "rdm_backup_repo", "rdm-1", []string{"caltechauthors-rdmrecords-*"}, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no such index") {
		t.Errorf("error = %v, want it to include the response body", err)
	}
}

func TestDeleteSnapshot_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := DeleteSnapshot(context.Background(), fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed SSM status")
	}
}

func TestDeleteSnapshot_ErrorIncludesResponseBody(t *testing.T) {
	body := `{"error":{"reason":"snapshot missing"},"status":404}`
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: body}
	err := DeleteSnapshot(context.Background(), fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "snapshot missing") {
		t.Errorf("error = %v, want it to include the response body", err)
	}
}

func TestPollSnapshotUntilComplete_SucceedsAfterInProgress(t *testing.T) {
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{`{"snapshots":[{"state":"IN_PROGRESS"}]}`, `{"snapshots":[{"state":"IN_PROGRESS"}]}`, `{"snapshots":[{"state":"SUCCESS"}]}`},
	}
	state, err := PollSnapshotUntilComplete(context.Background(), io.Discard, fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "SUCCESS" {
		t.Errorf("state = %q, want SUCCESS", state)
	}
	if fake.sendCommandCalls() < 3 {
		t.Errorf("sendCommandCalls = %d, want at least 3", fake.sendCommandCalls())
	}
}

// TestPollSnapshotUntilComplete_PrintsProgressWhileWaiting is a
// regression test for the silent-wait UX gap found live 2026-08-17
// against CaltechAUTHORS production (DECISIONS.md, "Poll-loop progress
// output: fix PollSnapshotUntilComplete ahead of Phase 20.51's sibling
// poller") -- a real multi-minute wait must produce visible output, not
// leave the terminal looking hung.
func TestPollSnapshotUntilComplete_PrintsProgressWhileWaiting(t *testing.T) {
	var buf bytes.Buffer
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{`{"snapshots":[{"state":"IN_PROGRESS"}]}`, `{"snapshots":[{"state":"IN_PROGRESS"}]}`, `{"snapshots":[{"state":"SUCCESS"}]}`},
	}
	_, err := PollSnapshotUntilComplete(context.Background(), &buf, fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "waiting for") {
		t.Errorf("output = %q, want an initial waiting message", buf.String())
	}
	if got := strings.Count(buf.String(), "elapsed"); got != 2 {
		t.Errorf("elapsed lines = %d, want 2 (one per tick before the final SUCCESS check), got:\n%s", got, buf.String())
	}
}

func TestPollSnapshotUntilComplete_FailedStateReturnsErrorNotTimeout(t *testing.T) {
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{`{"snapshots":[{"state":"FAILED"}]}`},
	}
	_, err := PollSnapshotUntilComplete(context.Background(), io.Discard, fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a FAILED snapshot state")
	}
	if !strings.Contains(err.Error(), "FAILED") {
		t.Errorf("error = %v, want it to mention FAILED", err)
	}
}

func TestPollSnapshotUntilComplete_NeverCompletingSequenceTimesOut(t *testing.T) {
	fake := &fakeSSMClient{
		commandID:      "cmd-1",
		finalStatus:    types.CommandInvocationStatusSuccess,
		stdoutSequence: []string{`{"snapshots":[{"state":"IN_PROGRESS"}]}`},
	}
	_, err := PollSnapshotUntilComplete(context.Background(), io.Discard, fake, "i-1", "rdm_backup_repo", "rdm-1", 30*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want a timeout message", err)
	}
}

func TestPollSnapshotUntilComplete_SSMFailureIncludesResponseBody(t *testing.T) {
	body := `{"error":{"reason":"no such repository"},"status":404}`
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: body}
	_, err := PollSnapshotUntilComplete(context.Background(), io.Discard, fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no such repository") {
		t.Errorf("error = %v, want it to include the response body", err)
	}
}

func TestPollSnapshotUntilComplete_PropagatesSendCommandError(t *testing.T) {
	fake := &fakeSSMClient{sendCommandErr: errors.New("boom")}
	_, err := PollSnapshotUntilComplete(context.Background(), io.Discard, fake, "i-1", "rdm_backup_repo", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}
