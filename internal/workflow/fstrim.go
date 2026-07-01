package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// isSSMOnline is a single-shot check of an instance's current SSM ping
// status -- unlike WaitForSSMOnline, this does not poll/wait for a
// transition, matching ec2_ami_manager.bash's check_ssm_availability
// (the instance here has presumably been running for a while, so
// polling for a transition doesn't apply).
func isSSMOnline(ctx context.Context, client awsclient.SSMAPI, instanceID string) (bool, error) {
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
func offerFstrimIfAvailable(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.SSMAPI, instanceID string) error {
	online, err := isSSMOnline(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if !online {
		t.Println("SSM is not available on this instance; skipping fstrim.")
		t.Refresh()
		return nil
	}

	run, err := Confirm(t, le, "Run fstrim via SSM before snapshotting?")
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
		t.Printf("fstrim did not complete (status: %s)\n", status)
	} else {
		t.Printf("fstrim output:\n%s\n", stdout)
	}
	t.Refresh()
	return nil
}
