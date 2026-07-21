package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// DefaultVolumeModificationPollInterval and
// DefaultVolumeModificationTimeout govern how long Resize Instance's
// Root Volume waits for ec2:ModifyVolume's change to become usable
// (DESIGN.md, "Configurable EBS Root Volume Size", Part 2) --
// "optimizing" or "completed", not just "modifying".
const (
	DefaultVolumeModificationPollInterval = 5 * time.Second
	DefaultVolumeModificationTimeout      = 10 * time.Minute
)

// rootVolumeInfo resolves instanceID's root EBS volume ID and its
// current size, via a fresh ec2:DescribeInstances (for the root device
// name and its VolumeId) followed by ec2:DescribeVolumes (for the
// current size) -- mirrors confirmTerminate's own fresh-fetch pattern
// (terminate_instance.go), since inventory.Instance (the list-tier
// struct) doesn't carry block device mappings.
func rootVolumeInfo(ctx context.Context, client awsclient.EC2API, instanceID string) (volumeID string, currentGB int32, err error) {
	describeCtx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstances(describeCtx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return "", 0, err
	}
	inst, found := findInstance(out, instanceID)
	if !found {
		return "", 0, fmt.Errorf("instance %s not found", instanceID)
	}

	rootDeviceName := aws.ToString(inst.RootDeviceName)
	for _, bdm := range inst.BlockDeviceMappings {
		if aws.ToString(bdm.DeviceName) == rootDeviceName && bdm.Ebs != nil {
			volumeID = aws.ToString(bdm.Ebs.VolumeId)
			break
		}
	}
	if volumeID == "" {
		return "", 0, fmt.Errorf("could not resolve the root EBS volume for instance %s", instanceID)
	}

	volCtx, cancel2 := withCallTimeout(ctx)
	defer cancel2()
	volOut, err := client.DescribeVolumes(volCtx, &ec2.DescribeVolumesInput{VolumeIds: []string{volumeID}})
	if err != nil {
		return "", 0, err
	}
	if len(volOut.Volumes) == 0 {
		return "", 0, fmt.Errorf("volume %s not found", volumeID)
	}
	return volumeID, aws.ToInt32(volOut.Volumes[0].Size), nil
}

// promptNewVolumeSizeGB prompts for a running instance's new root
// volume size, requiring it be strictly greater than currentGB -- EBS
// volumes can only grow, and a size equal to the current one would
// just waste one of the four modifications AWS allows per volume per
// rolling 24-hour period. No default (unlike promptRootVolumeSizeGB's
// creation-time prompt): the operator must explicitly choose a new
// size here.
func promptNewVolumeSizeGB(currentGB int32, input io.Reader, output io.Writer) (int32, error) {
	var size int32
	_, err := ui.Prompt(fmt.Sprintf("New root volume size in GB (currently %d)", currentGB), ui.WithValidator(func(s string) error {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n <= 0 {
			return errors.New("must be a positive integer")
		}
		if int32(n) <= currentGB {
			return fmt.Errorf("must be greater than the current size (%d GB) -- EBS volumes can only grow, not shrink", currentGB)
		}
		size = int32(n)
		return nil
	}), ui.WithIO(input, output))
	if err != nil {
		return 0, err
	}
	return size, nil
}

// modifyVolumeSize calls ec2:ModifyVolume to grow volumeID to
// newSizeGB.
func modifyVolumeSize(ctx context.Context, client awsclient.EC2API, volumeID string, newSizeGB int32) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	_, err := client.ModifyVolume(ctx, &ec2.ModifyVolumeInput{
		VolumeId: aws.String(volumeID),
		Size:     aws.Int32(newSizeGB),
	})
	return err
}

// waitUntilVolumeModificationUsable polls ec2:DescribeVolumesModifications
// until volumeID's most recent modification reaches "optimizing" or
// "completed" -- AWS's own guidance is that the volume is safely usable
// once it leaves "modifying", not only once fully "completed" (DESIGN.md,
// "Configurable EBS Root Volume Size", Part 2). Returns an error
// immediately on "failed", and on timeout.
func waitUntilVolumeModificationUsable(ctx context.Context, client awsclient.EC2API, volumeID string, timeout, pollInterval time.Duration) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := &ec2.DescribeVolumesModificationsInput{VolumeIds: []string{volumeID}}
	for {
		out, err := client.DescribeVolumesModifications(deadline, input)
		if err != nil {
			return err
		}
		if len(out.VolumesModifications) > 0 {
			mod := out.VolumesModifications[0]
			switch mod.ModificationState {
			case types.VolumeModificationStateOptimizing, types.VolumeModificationStateCompleted:
				return nil
			case types.VolumeModificationStateFailed:
				return fmt.Errorf("volume %s modification failed: %s", volumeID, aws.ToString(mod.StatusMessage))
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("timed out waiting for volume %s modification to become usable", volumeID)
		case <-time.After(pollInterval):
		}
	}
}

// ResizeInstanceRootVolume runs the full Resize Instance's Root
// Volume workflow (DESIGN.md, "Configurable EBS Root Volume Size",
// Part 2): pick an instance, resolve its root EBS volume and current
// size, prompt a new (larger) size, an Environment=production warning
// if tagged, the same type-to-confirm gate Feature 9 (Remove AMI)
// established, ec2:ModifyVolume, then wait for the modification to
// become usable. Returns nil (not an error) on cancellation or when
// there are no instances to pick from.
func ResizeInstanceRootVolume(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to resize its root volume", "", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return resizeInstanceRootVolume(ctx, w, ec2Clients, ssmClients, inst, nil, nil)
}

// resizeInstanceRootVolume is ResizeInstanceRootVolume's testable
// core, once an instance is resolved -- instance selection runs a real
// bubbletea Program (tui.RunPicker) that can't be pipe-tested, same
// limitation as every other Picker-tier conversion. input/output are
// nil in production and supplied by tests to drive the size prompt and
// type-to-confirm gate through their accessible-mode pipe path
// instead.
func resizeInstanceRootVolume(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, inst inventory.Instance, input io.Reader, output io.Writer) error {
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, inst.Region)
	if err != nil {
		return err
	}

	volumeID, currentGB, err := rootVolumeInfo(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Instance %s (%s): root volume %s is currently %d GB.\n", inst.InstanceID, inst.Name, volumeID, currentGB)

	newSizeGB, err := promptNewVolumeSizeGB(currentGB, input, output)
	if err != nil {
		return err
	}

	if inst.Environment == "production" {
		fmt.Fprintln(w, "WARNING: this instance is tagged Environment=production.")
	}
	ok, err := ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := modifyVolumeSize(ctx, ec2Client, volumeID, newSizeGB); err != nil {
		return fmt.Errorf("resizing volume %s: %w", volumeID, err)
	}
	fmt.Fprintf(w, "Resize requested for volume %s: %d GB -> %d GB. Waiting for the modification to take effect...\n", volumeID, currentGB, newSizeGB)

	if err := waitUntilVolumeModificationUsable(ctx, ec2Client, volumeID, DefaultVolumeModificationTimeout, DefaultVolumeModificationPollInterval); err != nil {
		return err
	}
	fmt.Fprintln(w, "Volume resize is usable.")

	growRootFilesystem(ctx, w, ssmClient, inst.InstanceID, newSizeGB, DefaultSSMOnlineTimeout, DefaultCloudInitTimeout, DefaultSSMPollInterval)
	return nil
}
