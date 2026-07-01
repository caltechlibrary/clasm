package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestOfferFstrim_SkipsCleanlyWhenSSMUnavailable(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 0}
	term, le, buf := newPipeEditor(t, "")

	err := offerFstrimIfAvailable(context.Background(), term, le, fake, "i-1")
	if err != nil {
		t.Fatalf("expected a clean skip, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "not available") {
		t.Errorf("expected an SSM-unavailable message, got:\n%s", buf.String())
	}
}

func TestOfferFstrim_DeclinedDoesNotRun(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1}
	term, le, _ := newPipeEditor(t, "n\n")

	err := offerFstrimIfAvailable(context.Background(), term, le, fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 0 {
		t.Error("SendCommand was called despite declining fstrim")
	}
}

func TestOfferFstrim_AcceptedRunsSuccessfully(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "/dev/root: 1.2 GiB (1234567890 bytes) trimmed\n"}
	term, le, buf := newPipeEditor(t, "y\n")

	err := offerFstrimIfAvailable(context.Background(), term, le, fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 1 {
		t.Errorf("SendCommand called %d times, want 1", fake.sendCommandCalls())
	}
	if !strings.Contains(buf.String(), "trimmed") {
		t.Errorf("expected fstrim output in the transcript, got:\n%s", buf.String())
	}
}

func TestOfferFstrim_AcceptedButCommandFails(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	term, le, buf := newPipeEditor(t, "y\n")

	err := offerFstrimIfAvailable(context.Background(), term, le, fake, "i-1")
	if err != nil {
		t.Fatalf("expected a warning, not an error, when fstrim itself fails: %v", err)
	}
	if !strings.Contains(buf.String(), "did not complete") {
		t.Errorf("expected a did-not-complete warning, got:\n%s", buf.String())
	}
}
