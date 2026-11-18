package workflow

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/executor"
)

// DefaultSSMPollInterval is the poll interval production callers should
// use for WaitForSSMOnline and RunShellCommand.
const DefaultSSMPollInterval = executor.DefaultSSMPollInterval

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

// minSSMCommandTimeoutSeconds is AWS's own floor for ssm:SendCommand's
// TimeoutSeconds -- values below this are rejected outright.
const minSSMCommandTimeoutSeconds = executor.MinSSMCommandTimeoutSeconds

// RunShellCommand runs command on instanceID via ssm:SendCommand
// (AWS-RunShellScript) and polls ssm:GetCommandInvocation until it
// reaches a terminal status or the timeout elapses, returning the
// captured stdout and terminal status. Unlike WaitForSSMOnline, a
// timeout here is an error -- once a command is actually running, it
// should finish in a bounded, predictable window (see DECISIONS.md,
// "Enhance Create Instance from AMI: cloud-init file input + completion
// check", on why an unbounded wait would mask a real hang).
//
// timeout also sets ssm:SendCommand's own TimeoutSeconds (floored at
// AWS's 30-second minimum) -- without it, AWS silently defaults the
// delivery window (how long AWS will wait for the command to *start*
// running) to 3600 seconds. That alone isn't sufficient, though: the
// AWS-RunShellScript document has its own, separate "executionTimeout"
// parameter (also defaulting to 3600 seconds) that bounds how long the
// command may keep *running* once started, entirely independent of
// TimeoutSeconds -- confirmed real 2026-08-19, a rebuild with only
// TimeoutSeconds fixed still died at exactly 1 hour. Both are set from
// the same timeout value here (see DECISIONS.md, "Real bug:
// RunShellCommand's SSM SendCommand never sets TimeoutSeconds..." and
// its same-day follow-up, PLAN.md Phase 20.61).
func RunShellCommand(ctx context.Context, client awsclient.SSMAPI, instanceID, command string, timeout, pollInterval time.Duration) (stdout string, status types.CommandInvocationStatus, err error) {
	return executor.RunShellCommand(ctx, client, instanceID, command, timeout, pollInterval)
}
