package workflow

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// DefaultLaunchPollInterval and DefaultLaunchTimeout are the production
// poll interval/timeout for WaitUntilRunning, matching the current Bash
// behavior (see PLAN.md, Phase 4).
const (
	DefaultLaunchPollInterval = 5 * time.Second
	DefaultLaunchTimeout      = 5 * time.Minute
)

// buildTagSpecification converts a plain tag map into a typed SDK
// TagSpecification -- this replaces ec2_ami_manager.bash's hand-built
// --tag-specifications shorthand string, the exact bug class (malformed
// AWS CLI shorthand that silently failed create-image) that motivated
// retargeting this project to Go (see DECISIONS.md, "Retarget
// implementation from Bash to Go"). Empty tag values are omitted.
func buildTagSpecification(resourceType types.ResourceType, tags map[string]string) types.TagSpecification {
	spec := types.TagSpecification{ResourceType: resourceType}
	for k, v := range tags {
		if v == "" {
			continue
		}
		spec.Tags = append(spec.Tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return spec
}

// Launch calls ec2:RunInstances for a single instance from params,
// returning the new instance's ID. Executing against AWS is kept
// separate from CollectLaunchInstanceParams so a future Recorded Script
// can produce the same LaunchInstanceParams without this code knowing
// the difference (see DECISIONS.md, "Structure workflows for future
// record/replay").
func Launch(ctx context.Context, client awsclient.EC2API, params LaunchInstanceParams) (string, error) {
	input := &ec2.RunInstancesInput{
		ImageId:           aws.String(params.ImageID),
		InstanceType:      types.InstanceType(params.InstanceType),
		KeyName:           aws.String(params.KeyName),
		SecurityGroupIds:  params.SecurityGroupIDs,
		SubnetId:          aws.String(params.SubnetID),
		MinCount:          aws.Int32(1),
		MaxCount:          aws.Int32(1),
		TagSpecifications: []types.TagSpecification{buildTagSpecification(types.ResourceTypeInstance, params.Tags)},
	}
	if params.UserData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(params.UserData)))
	}
	if params.IAMInstanceProfile != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{Name: aws.String(params.IAMInstanceProfile)}
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	if len(out.Instances) == 0 {
		return "", errors.New("RunInstances returned no instances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// WaitUntilRunning polls ec2:DescribeInstances until instanceID reaches
// the running state or the timeout elapses, returning the instance (so
// callers can read its connection info) or a timeout error -- unlike
// WaitForSSMOnline, a timeout here is a real error: an instance that
// never reaches running needs the operator's attention.
func WaitUntilRunning(ctx context.Context, client awsclient.EC2API, instanceID string, timeout, pollInterval time.Duration) (types.Instance, error) {
	return waitUntilState(ctx, client, instanceID, types.InstanceStateNameRunning, timeout, pollInterval)
}

// waitUntilState polls ec2:DescribeInstances until instanceID reaches
// want or the timeout elapses. Shared by WaitUntilRunning and Phase 7's
// WaitUntilStopped -- the polling mechanics are identical, only the
// target state differs. Tolerates InvalidInstanceID.NotFound as "not
// visible yet" rather than a hard failure -- see isInstanceNotYetVisible.
func waitUntilState(ctx context.Context, client awsclient.EC2API, instanceID string, want types.InstanceStateName, timeout, pollInterval time.Duration) (types.Instance, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}
	for {
		out, err := client.DescribeInstances(deadline, input)
		switch {
		case err != nil && !isInstanceNotYetVisible(err):
			return types.Instance{}, err
		case err == nil:
			if inst, found := findInstance(out, instanceID); found && inst.State != nil && inst.State.Name == want {
				return inst, nil
			}
		}
		select {
		case <-deadline.Done():
			return types.Instance{}, fmt.Errorf("timed out waiting for instance %s to reach %s", instanceID, want)
		case <-time.After(pollInterval):
		}
	}
}

// isInstanceNotYetVisible reports whether err is AWS's own
// InvalidInstanceID.NotFound -- expected for the first few seconds
// after ec2:RunInstances returns a new instance ID, before that ID is
// visible to ec2:DescribeInstances (a well-known eventual-consistency
// window, not a real failure). Without this, WaitUntilRunning could
// fail immediately after a successful launch with "the instance ID ...
// does not exist" -- confusing given the instance did, in fact, just
// launch. See DECISIONS.md, "Tolerate DescribeInstances' post-
// RunInstances eventual-consistency window".
func isInstanceNotYetVisible(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == "InvalidInstanceID.NotFound"
}

func findInstance(out *ec2.DescribeInstancesOutput, instanceID string) (types.Instance, bool) {
	for _, reservation := range out.Reservations {
		for _, inst := range reservation.Instances {
			if aws.ToString(inst.InstanceId) == instanceID {
				return inst, true
			}
		}
	}
	return types.Instance{}, false
}

// runLaunch confirms, launches, waits for running, and -- if user data
// was provided -- checks cloud-init completion, then displays connection
// info. Shared by CreateInstanceFromAMI and CreateInstanceFromCloudInit,
// which differ only in how params is collected (see DECISIONS.md, "Add
// Create EC2 Instance from Cloud-Init YAML as a v1 primitive").
func runLaunch(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, params LaunchInstanceParams) error {
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

	displayConnectionInfo(t, instanceID, inst)
	return nil
}

// displayConnectionInfo prints an instance's public/private IP and, if it
// has a public IP, a ready-to-copy ssh command -- shared by every
// workflow that ends with a running instance (Create Instance from AMI/
// Cloud-Init YAML, Start Instance).
func displayConnectionInfo(t *termlib.Terminal, instanceID string, inst types.Instance) {
	t.Printf("\nInstance %s is running.\n", instanceID)
	t.Printf("  Public IP:  %s\n", displayOrNone(aws.ToString(inst.PublicIpAddress)))
	t.Printf("  Private IP: %s\n", displayOrNone(aws.ToString(inst.PrivateIpAddress)))
	if inst.PublicIpAddress != nil {
		t.Printf("  ssh ec2-user@%s\n", aws.ToString(inst.PublicIpAddress))
	}
	t.Refresh()
}

func displayOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
