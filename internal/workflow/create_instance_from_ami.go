package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// CreateInstanceFromAMI runs the full Create EC2 Instance from AMI
// workflow (DESIGN.md, Feature 2): collect parameters, confirm, launch,
// wait for running, and -- if user data was provided -- wait for SSM and
// check cloud-init's completion status. Returns nil (not an error) on
// user cancellation, matching every other workflow's confirmation gate.
func CreateInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, images []inventory.Image) error {
	params, err := CollectLaunchInstanceParams(t, le, images)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			t.Println("Cancelled.")
			t.Refresh()
			return nil
		}
		return err
	}

	t.Printf("\nAbout to launch: image=%s type=%s key=%s subnet=%s tags=%v\n",
		params.ImageID, params.InstanceType, params.KeyName, params.SubnetID, params.Tags)
	t.Refresh()

	ok, err := Confirm(t, le, "Launch this instance?")
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	instanceID, err := Launch(ctx, ec2Client, params)
	if err != nil {
		return fmt.Errorf("launching instance: %w", err)
	}

	t.Printf("Launched %s, waiting for it to reach running...\n", instanceID)
	t.Refresh()
	inst, err := WaitUntilRunning(ctx, ec2Client, instanceID, DefaultLaunchTimeout, DefaultLaunchPollInterval)
	if err != nil {
		return err
	}

	if params.UserData != "" {
		t.Println("Waiting for SSM and checking cloud-init completion...")
		t.Refresh()
		result, err := checkCloudInitCompletion(ctx, ssmClient, instanceID, DefaultSSMOnlineTimeout, DefaultCloudInitTimeout, DefaultSSMPollInterval)
		if err != nil {
			return err
		}
		switch {
		case result.Skipped:
			t.Println("SSM never came online; skipping the cloud-init completion check.")
		case result.Status == "done":
			t.Println("cloud-init completed successfully.")
		default:
			t.Println("cloud-init reported an error -- check the instance before using it.")
		}
		t.Refresh()
	}

	t.Printf("\nInstance %s is running.\n", instanceID)
	t.Printf("  Public IP:  %s\n", displayOrNone(aws.ToString(inst.PublicIpAddress)))
	t.Printf("  Private IP: %s\n", displayOrNone(aws.ToString(inst.PrivateIpAddress)))
	if inst.PublicIpAddress != nil {
		t.Printf("  ssh ec2-user@%s\n", aws.ToString(inst.PublicIpAddress))
	}
	t.Refresh()
	return nil
}

func displayOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
