package executor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// DefaultSSMPollInterval is the poll interval production callers should
// use for RunShellCommand.
const DefaultSSMPollInterval = 5 * time.Second

// MinSSMCommandTimeoutSeconds is AWS's own floor for ssm:SendCommand's
// TimeoutSeconds -- values below this are rejected outright.
const MinSSMCommandTimeoutSeconds = 30

// RunShellCommand runs command on instanceID via ssm:SendCommand
// (AWS-RunShellScript) and polls ssm:GetCommandInvocation until it
// reaches a terminal status or the timeout elapses, returning the
// captured stdout and terminal status. A timeout here is an error: once
// a command is actually running, it should finish in a bounded window.
//
// timeout also sets ssm:SendCommand's own TimeoutSeconds (floored at
// AWS's 30-second minimum) -- without it, AWS silently defaults the
// delivery window to 3600 seconds. The AWS-RunShellScript document has
// its own, separate "executionTimeout" parameter (also defaulting to 3600
// seconds) that bounds how long the command may keep running once
// started, independent of TimeoutSeconds -- confirmed real 2026-08-19.
// Both are set from the same timeout value here (PLAN.md Phase 20.61).
//
// @param ctx {context.Context} cancels the call
// @param client {awsclient.SSMAPI} the SSM client in the instance's region
// @param instanceID {string} the EC2 instance id
// @param command {string} the shell text to run
// @param timeout {time.Duration} how long the command may take
// @param pollInterval {time.Duration} how often to poll for completion
// @returns {string} the command's standard output
// @returns {types.CommandInvocationStatus} SSM's terminal status
// @returns {error} why it could not be run or did not finish, or nil
// @example
//
//	out, status, err := RunShellCommand(ctx, client, "i-0123", "uname -a", time.Minute, DefaultSSMPollInterval)
func RunShellCommand(ctx context.Context, client awsclient.SSMAPI, instanceID, command string, timeout, pollInterval time.Duration) (stdout string, status types.CommandInvocationStatus, err error) {
	timeoutSeconds := max(int32(timeout/time.Second), MinSSMCommandTimeoutSeconds)
	sendOut, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands":         {command},
			"executionTimeout": {strconv.Itoa(int(timeoutSeconds))},
		},
		TimeoutSeconds: aws.Int32(timeoutSeconds),
	})
	if err != nil {
		return "", "", err
	}
	commandID := aws.ToString(sendOut.Command.CommandId)

	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := &ssm.GetCommandInvocationInput{CommandId: aws.String(commandID), InstanceId: aws.String(instanceID)}
	for {
		out, err := client.GetCommandInvocation(deadline, input)
		switch {
		case err != nil && !isInvocationNotYetVisible(err):
			return "", "", err
		case err == nil && isTerminalCommandStatus(out.Status):
			return aws.ToString(out.StandardOutputContent), out.Status, nil
		}
		select {
		case <-deadline.Done():
			return "", "", fmt.Errorf("timed out waiting for command %q to finish on %s", command, instanceID)
		case <-time.After(pollInterval):
		}
	}
}

// isInvocationNotYetVisible reports whether err is AWS's own
// InvocationDoesNotExist -- expected for the first few seconds after
// ssm:SendCommand returns a new command ID, before that invocation is
// visible to ssm:GetCommandInvocation.
func isInvocationNotYetVisible(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == "InvocationDoesNotExist"
}

func isTerminalCommandStatus(s types.CommandInvocationStatus) bool {
	switch s {
	case types.CommandInvocationStatusSuccess,
		types.CommandInvocationStatusFailed,
		types.CommandInvocationStatusCancelled,
		types.CommandInvocationStatusTimedOut:
		return true
	default:
		return false
	}
}
