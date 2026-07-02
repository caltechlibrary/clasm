package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// fakeSSMClient embeds the (nil) SSMAPI interface so it satisfies
// awsclient.SSMAPI without stubbing every method.
type fakeSSMClient struct {
	awsclient.SSMAPI

	// DescribeInstanceInformation
	onlineAfterCalls int // reports Online starting at this call number; 0 = never
	describeCalls    int
	describeErr      error

	// SendCommand
	commandID            string
	sendCommandErr       error
	sendCommandCallCount int

	// GetCommandInvocation
	pendingCalls    int // number of leading calls that report InProgress
	invocationCalls int
	finalStatus     types.CommandInvocationStatus
	stdout          string
	invocationErr   error
	// invocationNotFoundForCalls, if > 0, makes the first N
	// GetCommandInvocation calls return InvocationDoesNotExist --
	// simulates the real eventual-consistency window right after
	// ssm:SendCommand returns a command ID that isn't immediately
	// visible to ssm:GetCommandInvocation (see ssm.go's
	// isInvocationNotYetVisible).
	invocationNotFoundForCalls int

	// responses lets a single fake distinguish between different remote
	// commands within one test (e.g. Backup Archive & Trim's list/upload/
	// delete/fstrim steps) by matching a substring against the most
	// recently sent command text; first match wins. Falls back to
	// finalStatus/stdout above when empty or no substring matches, so
	// every pre-existing test (single command per fake) is unaffected.
	responses       []ssmCommandResponse
	lastCommandText string
	sentCommands    []string
}

type ssmCommandResponse struct {
	substring string
	stdout    string
	status    types.CommandInvocationStatus
}

func (f *fakeSSMClient) DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	f.describeCalls++
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if f.onlineAfterCalls > 0 && f.describeCalls >= f.onlineAfterCalls {
		return &ssm.DescribeInstanceInformationOutput{InstanceInformationList: []types.InstanceInformation{
			{PingStatus: types.PingStatusOnline},
		}}, nil
	}
	return &ssm.DescribeInstanceInformationOutput{}, nil
}

func (f *fakeSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.sendCommandCallCount++
	if len(params.Parameters["commands"]) > 0 {
		f.lastCommandText = params.Parameters["commands"][0]
		f.sentCommands = append(f.sentCommands, f.lastCommandText)
	}
	if f.sendCommandErr != nil {
		return nil, f.sendCommandErr
	}
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String(f.commandID)}}, nil
}

func (f *fakeSSMClient) sendCommandCalls() int { return f.sendCommandCallCount }

func (f *fakeSSMClient) GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	f.invocationCalls++
	if f.invocationNotFoundForCalls > 0 && f.invocationCalls <= f.invocationNotFoundForCalls {
		return nil, &smithy.GenericAPIError{Code: "InvocationDoesNotExist", Message: ""}
	}
	if f.invocationErr != nil {
		return nil, f.invocationErr
	}
	if f.invocationCalls <= f.pendingCalls {
		return &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusInProgress}, nil
	}
	for _, r := range f.responses {
		if strings.Contains(f.lastCommandText, r.substring) {
			return &ssm.GetCommandInvocationOutput{Status: r.status, StandardOutputContent: aws.String(r.stdout)}, nil
		}
	}
	return &ssm.GetCommandInvocationOutput{Status: f.finalStatus, StandardOutputContent: aws.String(f.stdout)}, nil
}

const testPollInterval = 5 * time.Millisecond

func TestWaitForSSMOnline_AlreadyOnline(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1}
	online, err := WaitForSSMOnline(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !online {
		t.Error("got false, want true")
	}
}

func TestWaitForSSMOnline_BecomesOnlineAfterPolling(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 3}
	online, err := WaitForSSMOnline(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !online {
		t.Error("got false, want true")
	}
	if fake.describeCalls < 3 {
		t.Errorf("describeCalls = %d, want at least 3", fake.describeCalls)
	}
}

func TestWaitForSSMOnline_NeverOnline_TimesOutCleanly(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 0}
	online, err := WaitForSSMOnline(context.Background(), fake, "i-1", 30*time.Millisecond, testPollInterval)
	if err != nil {
		t.Fatalf("expected a clean (non-error) timeout, got: %v", err)
	}
	if online {
		t.Error("got true, want false")
	}
}

func TestWaitForSSMOnline_PropagatesError(t *testing.T) {
	fake := &fakeSSMClient{describeErr: errors.New("boom")}
	_, err := WaitForSSMOnline(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunShellCommand_SucceedsAfterPolling(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", pendingCalls: 2, finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}
	stdout, status, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != types.CommandInvocationStatusSuccess {
		t.Errorf("status = %v, want Success", status)
	}
	if stdout != "status: done\n" {
		t.Errorf("stdout = %q, want %q", stdout, "status: done\n")
	}
}

func TestRunShellCommand_ReportsFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "status: error\n"}
	_, status, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != types.CommandInvocationStatusFailed {
		t.Errorf("status = %v, want Failed", status)
	}
}

func TestRunShellCommand_PropagatesSendCommandError(t *testing.T) {
	fake := &fakeSSMClient{sendCommandErr: errors.New("boom")}
	_, _, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunShellCommand_TimesOutWithError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", pendingCalls: 1000}
	_, _, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

// Regression: a real launch failed immediately after ssm:SendCommand
// succeeded, at the very first ssm:GetCommandInvocation call, with AWS's
// own InvocationDoesNotExist -- the SSM-side analog of
// InvalidInstanceID.NotFound/InvalidAMIID.NotFound (see DECISIONS.md,
// "Tolerate GetCommandInvocation's post-SendCommand eventual-consistency
// window"). This must be tolerated like "not visible yet", not treated
// as a hard failure.
func TestRunShellCommand_TreatsPostSendInvocationNotFoundAsNotYetVisible(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", invocationNotFoundForCalls: 2, finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}
	stdout, status, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != types.CommandInvocationStatusSuccess {
		t.Errorf("status = %v, want Success", status)
	}
	if stdout != "status: done\n" {
		t.Errorf("stdout = %q, want %q", stdout, "status: done\n")
	}
}

func TestRunShellCommand_TimesOutIfInvocationNeverVisible(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", invocationNotFoundForCalls: 1000}
	_, _, err := RunShellCommand(context.Background(), fake, "i-1", "cloud-init status --wait", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}
