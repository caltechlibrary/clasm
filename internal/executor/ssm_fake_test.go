package executor

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

const testPollInterval = 5 * time.Millisecond

// fakeSSMClient embeds the (nil) SSMAPI interface so it satisfies
// awsclient.SSMAPI without stubbing every method. It models only what the
// executor tests need: the command sent, and the invocation's outcome.
type fakeSSMClient struct {
	awsclient.SSMAPI

	commandID                  string
	sendCommandErr             error
	sendCommandCallCount       int
	lastInstanceIDs            []string
	lastTimeoutSeconds         *int32
	lastExecutionTimeout       string
	sawExecutionTimeoutKey     bool
	lastCommandText            string
	pendingCalls               int // leading GetCommandInvocation calls that report InProgress
	invocationCalls            int
	invocationNotFoundForCalls int // leading calls that report InvocationDoesNotExist
	invocationErr              error
	finalStatus                types.CommandInvocationStatus
	stdout                     string
}

func (f *fakeSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.sendCommandCallCount++
	f.lastInstanceIDs = params.InstanceIds
	f.lastTimeoutSeconds = params.TimeoutSeconds
	if et, ok := params.Parameters["executionTimeout"]; ok && len(et) > 0 {
		f.sawExecutionTimeoutKey = true
		f.lastExecutionTimeout = et[0]
	} else {
		f.sawExecutionTimeoutKey = false
		f.lastExecutionTimeout = ""
	}
	if len(params.Parameters["commands"]) > 0 {
		f.lastCommandText = params.Parameters["commands"][0]
	}
	if f.sendCommandErr != nil {
		return nil, f.sendCommandErr
	}
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String(f.commandID)}}, nil
}

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
	return &ssm.GetCommandInvocationOutput{Status: f.finalStatus, StandardOutputContent: aws.String(f.stdout)}, nil
}
