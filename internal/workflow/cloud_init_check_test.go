package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestCheckCloudInitCompletion_Done(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Skipped {
		t.Error("got Skipped=true, want false")
	}
	if got.Status != "done" {
		t.Errorf("Status = %q, want %q", got.Status, "done")
	}
}

func TestCheckCloudInitCompletion_Error(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: error\n"}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("Status = %q, want %q", got.Status, "error")
	}
}

func TestCheckCloudInitCompletion_CommandFailedStatusIsError(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: ""}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("Status = %q, want %q", got.Status, "error")
	}
}

func TestCheckCloudInitCompletion_SkipsWhenSSMUnavailable(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 0}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", 20*time.Millisecond, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("expected a clean skip, got error: %v", err)
	}
	if !got.Skipped {
		t.Error("got Skipped=false, want true")
	}
}
