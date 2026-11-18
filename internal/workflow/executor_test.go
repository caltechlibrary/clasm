package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
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

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "local.bin")
	if err := os.WriteFile(p, content, 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSSMExecutor_Put_SendsTheFileAndVerifiesItsChecksum(t *testing.T) {
	content := []byte("hello\n")
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: sha256Hex(content) + "  /etc/thing.conf\n"}
	exec := NewSSMExecutor(fake, "i-1")
	if err := exec.Put(context.Background(), writeTempFile(t, content), "/etc/thing.conf"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.Contains(fake.lastCommandText, "/etc/thing.conf") {
		t.Errorf("the command does not name the destination: %q", fake.lastCommandText)
	}
	if !strings.Contains(fake.lastCommandText, base64.StdEncoding.EncodeToString(content)) {
		t.Errorf("the command does not carry the file")
	}
	if fake.sendCommandCallCount != 1 {
		t.Errorf("want one SSM call, got %d", fake.sendCommandCallCount)
	}
}

func TestSSMExecutor_Put_AChecksumMismatchIsAnError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: "deadbeef  /etc/thing.conf\n"}
	err := NewSSMExecutor(fake, "i-1").Put(context.Background(), writeTempFile(t, []byte("hello\n")), "/etc/thing.conf")
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("want a checksum error, got %v", err)
	}
}

func TestSSMExecutor_Put_ARemoteFailureIsAnError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusFailed, stdout: "No space left on device"}
	err := NewSSMExecutor(fake, "i-1").Put(context.Background(), writeTempFile(t, []byte("x")), "/etc/thing.conf")
	if err == nil || !strings.Contains(err.Error(), "No space left") {
		t.Errorf("the remote output belongs in the error: %v", err)
	}
}

func TestSSMExecutor_Put_RefusesAFileOverTheLimitAndSendsNothing(t *testing.T) {
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess}
	err := NewSSMExecutor(fake, "i-1").Put(context.Background(), writeTempFile(t, make([]byte, maxSSMPutBytes+1)), "/etc/big")
	if err == nil || !strings.Contains(err.Error(), "S3") {
		t.Errorf("an oversize file should be refused with a pointer to S3: %v", err)
	}
	if fake.sendCommandCallCount != 0 {
		t.Errorf("nothing may be sent, got %d calls", fake.sendCommandCallCount)
	}
}

func TestSSMExecutor_Put_RefusesARelativeRemotePathAndSendsNothing(t *testing.T) {
	fake := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess}
	err := NewSSMExecutor(fake, "i-1").Put(context.Background(), writeTempFile(t, []byte("x")), "relative/path")
	if err == nil || fake.sendCommandCallCount != 0 {
		t.Errorf("a relative destination is refused before anything is sent: err=%v calls=%d", err, fake.sendCommandCallCount)
	}
}

func TestSSMExecutor_Get_IsNotSupportedAndSaysWhy(t *testing.T) {
	err := NewSSMExecutor(&fakeSSMClient{}, "i-1").Get(context.Background(), "/etc/x", filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "S3") {
		t.Errorf("Get over SSM should say to use S3: %v", err)
	}
}
