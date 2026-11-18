package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Compile-time proof that SSMExecutor satisfies Executor (DR-0201).
var _ Executor = (*SSMExecutor)(nil)

func TestSSMExecutor_Run_ReturnsStdoutAndSuccess(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "hello\n"}
	exec := NewSSMExecutor(fake, "i-1")
	stdout, status, err := exec.Run(context.Background(), "echo hello", time.Minute, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusSuccess {
		t.Errorf("status = %q, want %q", status, StatusSuccess)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
}

// The executor must send exactly what RunShellCommand sends: the bound
// instance, the command text, and both timeouts (Phase 20.61).
func TestSSMExecutor_Run_SendsTheBoundInstanceAndCommand(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	exec := NewSSMExecutor(fake, "i-abc")
	if _, _, err := exec.Run(context.Background(), "uname -a", 2*time.Minute, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastInstanceIDs) != 1 || fake.lastInstanceIDs[0] != "i-abc" {
		t.Errorf("InstanceIds = %v, want [i-abc]", fake.lastInstanceIDs)
	}
	if fake.lastCommandText != "uname -a" {
		t.Errorf("command = %q, want %q", fake.lastCommandText, "uname -a")
	}
	if fake.lastTimeoutSeconds == nil || *fake.lastTimeoutSeconds != 120 {
		t.Errorf("TimeoutSeconds = %v, want 120", fake.lastTimeoutSeconds)
	}
	if !fake.sawExecutionTimeoutKey || fake.lastExecutionTimeout != "120" {
		t.Errorf("executionTimeout = %q (seen=%v), want \"120\"", fake.lastExecutionTimeout, fake.sawExecutionTimeoutKey)
	}
}

// AWS rejects TimeoutSeconds below 30, so a short caller timeout is floored.
func TestSSMExecutor_Run_FloorsTheAWSTimeoutAtThirtySeconds(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	exec := NewSSMExecutor(fake, "i-1")
	if _, _, err := exec.Run(context.Background(), "true", time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTimeoutSeconds == nil || *fake.lastTimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %v, want 30", fake.lastTimeoutSeconds)
	}
	if fake.lastExecutionTimeout != "30" {
		t.Errorf("executionTimeout = %q, want \"30\"", fake.lastExecutionTimeout)
	}
}

// The status strings are SSM's own, so every message that prints a status is
// byte for byte what it was before the executor existed.
func TestSSMExecutor_Run_MapsTerminalStatusesKeepingTheirText(t *testing.T) {
	cases := []struct {
		ssm  types.CommandInvocationStatus
		want CommandStatus
		text string
	}{
		{types.CommandInvocationStatusSuccess, StatusSuccess, "Success"},
		{types.CommandInvocationStatusFailed, StatusFailed, "Failed"},
		{types.CommandInvocationStatusCancelled, StatusCancelled, "Cancelled"},
		{types.CommandInvocationStatusTimedOut, StatusTimedOut, "TimedOut"},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: c.ssm}
			exec := NewSSMExecutor(fake, "i-1")
			_, status, err := exec.Run(context.Background(), "true", time.Minute, testPollInterval)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != c.want {
				t.Errorf("status = %q, want %q", status, c.want)
			}
			if string(status) != c.text {
				t.Errorf("status text = %q, want %q", string(status), c.text)
			}
		})
	}
}

func TestSSMExecutor_Run_PropagatesSendCommandError(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeSSMClient{sendCommandErr: boom}
	exec := NewSSMExecutor(fake, "i-1")
	_, _, err := exec.Run(context.Background(), "true", time.Minute, testPollInterval)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestSSMExecutor_Run_TimesOutWithError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", pendingCalls: 1000}
	exec := NewSSMExecutor(fake, "i-1")
	_, _, err := exec.Run(context.Background(), "true", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %q, want it to say the command timed out", err)
	}
}

func TestSSMExecutor_Name_IsTheInstanceID(t *testing.T) {
	exec := NewSSMExecutor(&fakeSSMClient{}, "i-0123456789")
	if got := exec.Name(); got != "i-0123456789" {
		t.Errorf("Name() = %q, want %q", got, "i-0123456789")
	}
}
