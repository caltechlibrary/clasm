package workflow

import (
	"context"
	"strings"
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

// degradedLong is real `cloud-init status --long` output for the false alarm
// found 2026-08-18: two harmless groups warnings, errors empty.
const degradedLong = "status: degraded\n" +
	"extended_status: degraded done\n" +
	"boot_status_code: enabled-by-generator\n" +
	"detail: DataSourceEc2Local\n" +
	"errors: []\n" +
	"recoverable_errors:\n" +
	"WARNING:\n" +
	"\t- Skipping creation of existing group 'staff'\n" +
	"\t- Skipping creation of existing group 'www-data'\n"

func TestCheckCloudInitCompletion_DegradedIsNotAnError(t *testing.T) {
	// cloud-init exits 2 when degraded, which SSM reports as Failed; the
	// stdout, not the invocation status, has to decide.
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: degradedLong}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "degraded" {
		t.Errorf("Status = %q, want %q", got.Status, "degraded")
	}
	for _, want := range []string{"recoverable_errors", "Skipping creation of existing group 'staff'"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("Detail missing %q:\n%s", want, got.Detail)
		}
	}
}

func TestCheckCloudInitCompletion_DegradedWithRealErrorsIsAnError(t *testing.T) {
	out := strings.Replace(degradedLong, "errors: []", "errors:\n\t- failed to run module scripts-user", 1)
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: out}
	got, err := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("Status = %q, want %q", got.Status, "error")
	}
	if !strings.Contains(got.Detail, "failed to run module scripts-user") {
		t.Errorf("Detail should carry the real error text:\n%s", got.Detail)
	}
}

func TestCheckCloudInitCompletion_ErrorCarriesDetail(t *testing.T) {
	out := "status: error\nerrors:\n\t- Failed to install packages\nrecoverable_errors: {}\n"
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: out}
	got, _ := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if got.Status != "error" || !strings.Contains(got.Detail, "Failed to install packages") {
		t.Errorf("got %+v, want error with the error text", got)
	}
}

func TestCheckCloudInitCompletion_DoneHasNoDetail(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\nerrors: []\nrecoverable_errors: {}\n"}
	got, _ := checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if got.Status != "done" || got.Detail != "" {
		t.Errorf("got %+v, want done with no detail", got)
	}
}

func TestCheckCloudInitCompletion_AsksForLongStatus(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}
	checkCloudInitCompletion(context.Background(), fake, "i-1", time.Second, time.Second, testPollInterval)
	if !strings.Contains(fake.lastCommandText, "cloud-init status --wait --long") {
		t.Errorf("command = %q, want --long so the error text is returned", fake.lastCommandText)
	}
}

func TestReportCloudInit_Wording(t *testing.T) {
	cases := []struct {
		name   string
		result CloudInitCheckResult
		want   []string
		not    []string
	}{
		{"done", CloudInitCheckResult{Status: "done"}, []string{"cloud-init completed successfully."}, nil},
		{"skipped", CloudInitCheckResult{Skipped: true}, []string{"SSM never came online"}, nil},
		{"degraded", CloudInitCheckResult{Status: "degraded", Detail: "recoverable_errors:\n\t- Skipping creation of existing group 'staff'"},
			[]string{"degraded", "not a failure", "Skipping creation of existing group 'staff'"}, []string{"reported an error"}},
		{"error", CloudInitCheckResult{Status: "error", Detail: "errors:\n\t- Failed to install packages"},
			[]string{"cloud-init reported an error", "Failed to install packages", "Check the instance before using it"}, nil},
		{"error without detail", CloudInitCheckResult{Status: "error"}, []string{"cloud-init reported an error", "Check the instance before using it"}, nil},
	}
	for _, c := range cases {
		var out strings.Builder
		reportCloudInit(&out, c.result)
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: output missing %q:\n%s", c.name, w, out.String())
			}
		}
		for _, n := range c.not {
			if strings.Contains(out.String(), n) {
				t.Errorf("%s: output should not contain %q:\n%s", c.name, n, out.String())
			}
		}
	}
}
