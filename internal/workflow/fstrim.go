package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// isSSMOnline is a single-shot check of an instance's current SSM ping
// status -- unlike WaitForSSMOnline, this does not poll/wait for a
// transition, matching ec2_ami_manager.bash's check_ssm_availability
// (the instance here has presumably been running for a while, so
// polling for a transition doesn't apply).
func isSSMOnline(ctx context.Context, client awsclient.SSMAPI, instanceID string) (bool, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []types.InstanceInformationStringFilter{
			{Key: aws.String("InstanceIds"), Values: []string{instanceID}},
		},
	})
	if err != nil {
		return false, err
	}
	for _, info := range out.InstanceInformationList {
		if info.PingStatus == types.PingStatusOnline {
			return true, nil
		}
	}
	return false, nil
}

// offerFstrimIfAvailable offers to run `sudo fstrim -av` via SSM before
// snapshotting, to reduce copy time by skipping already-freed blocks. If
// SSM is unavailable, or the user declines, or the command itself fails
// to complete, this returns cleanly (nil) -- fstrim is an optimization,
// never a precondition for AMI creation (see DESIGN.md, "Domain
// Knowledge Carried Forward").
func offerFstrimIfAvailable(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID string, input io.Reader, output io.Writer) error {
	online, err := isSSMOnline(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if !online {
		fmt.Fprintln(w, "SSM is not available on this instance; skipping fstrim.")
		return nil
	}

	run, err := Confirm("Run fstrim via SSM before snapshotting?", WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !run {
		return nil
	}

	stdout, status, err := RunShellCommand(ctx, client, instanceID, "sudo fstrim -av", DefaultCloudInitTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if status != types.CommandInvocationStatusSuccess {
		fmt.Fprintf(w, "fstrim did not complete (status: %s)\n", status)
	} else {
		fmt.Fprintf(w, "fstrim output:\n%s\n", stdout)
	}
	return nil
}
