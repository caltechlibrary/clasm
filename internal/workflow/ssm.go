package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// DefaultSSMPollInterval is the poll interval production callers should
// use for WaitForSSMOnline and RunShellCommand.
const DefaultSSMPollInterval = 5 * time.Second

// WaitForSSMOnline polls ssm:DescribeInstanceInformation until the given
// instance reports PingStatus Online, or the timeout elapses. A timeout
// is reported as (false, nil), not an error -- not every AMI has SSM
// configured, and this must be a clean skip, not a failure (see
// DESIGN.md, "Enhance Create Instance from AMI: cloud-init file input +
// completion check").
func WaitForSSMOnline(ctx context.Context, client awsclient.SSMAPI, instanceID string, timeout, pollInterval time.Duration) (bool, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := &ssm.DescribeInstanceInformationInput{
		Filters: []types.InstanceInformationStringFilter{
			{Key: aws.String("InstanceIds"), Values: []string{instanceID}},
		},
	}

	for {
		out, err := client.DescribeInstanceInformation(deadline, input)
		if err != nil {
			return false, err
		}
		for _, info := range out.InstanceInformationList {
			if info.PingStatus == types.PingStatusOnline {
				return true, nil
			}
		}
		select {
		case <-deadline.Done():
			return false, nil
		case <-time.After(pollInterval):
		}
	}
}

// RunShellCommand runs command on instanceID via ssm:SendCommand
// (AWS-RunShellScript) and polls ssm:GetCommandInvocation until it
// reaches a terminal status or the timeout elapses, returning the
// captured stdout and terminal status. Unlike WaitForSSMOnline, a
// timeout here is an error -- once a command is actually running, it
// should finish in a bounded, predictable window (see DECISIONS.md,
// "Enhance Create Instance from AMI: cloud-init file input + completion
// check", on why an unbounded wait would mask a real hang).
func RunShellCommand(ctx context.Context, client awsclient.SSMAPI, instanceID, command string, timeout, pollInterval time.Duration) (stdout string, status types.CommandInvocationStatus, err error) {
	sendOut, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters:   map[string][]string{"commands": {command}},
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
		if err != nil {
			return "", "", err
		}
		if isTerminalCommandStatus(out.Status) {
			return aws.ToString(out.StandardOutputContent), out.Status, nil
		}
		select {
		case <-deadline.Done():
			return "", "", fmt.Errorf("timed out waiting for command %q to finish on %s", command, instanceID)
		case <-time.After(pollInterval):
		}
	}
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
