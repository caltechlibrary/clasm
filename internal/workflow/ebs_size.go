package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// describeImageRootVolume looks up imageID's root device name and its
// default root volume size in GB, straight from ec2:DescribeImages --
// needed because inventory.Image (the pick-list-tier struct) doesn't
// carry block device mappings (DESIGN.md, "Configurable EBS Root
// Volume Size": adding them there for a value only needed once, at the
// point of collecting launch params, would be scope creep for a value
// every other consumer of inventory.Image doesn't need).
func describeImageRootVolume(ctx context.Context, client awsclient.EC2API, imageID string) (deviceName string, defaultGB int32, err error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{imageID}})
	if err != nil {
		return "", 0, err
	}
	if len(out.Images) == 0 {
		return "", 0, fmt.Errorf("image %s not found", imageID)
	}

	img := out.Images[0]
	deviceName = aws.ToString(img.RootDeviceName)
	for _, bdm := range img.BlockDeviceMappings {
		if aws.ToString(bdm.DeviceName) == deviceName && bdm.Ebs != nil {
			return deviceName, aws.ToInt32(bdm.Ebs.VolumeSize), nil
		}
	}
	return deviceName, 0, nil
}

// promptRootVolumeSizeGB prompts for the root EBS volume size in GB,
// defaulting to defaultGB (the AMI's own default, from
// describeImageRootVolume) but editable -- TODO.md's confirmed
// production bug (DESIGN.md, "Configurable EBS Root Volume Size"):
// every instance/launch template clasm created silently inherited the
// AMI's default (8GB for stock Ubuntu) with no way to ask for more.
// Rejects anything smaller than defaultGB when defaultGB is known
// (>0) -- AWS itself rejects shrinking below the source snapshot's
// size, but failing fast here beats a RunInstances/
// CreateLaunchTemplate API error after every other launch param has
// already been collected.
func promptRootVolumeSizeGB(defaultGB int32, input io.Reader, output io.Writer) (int32, error) {
	s, err := ui.Prompt("Root EBS volume size in GB",
		ui.WithDefault(strconv.Itoa(int(defaultGB))),
		ui.WithValidator(func(s string) error {
			n, convErr := strconv.Atoi(strings.TrimSpace(s))
			if convErr != nil || n <= 0 {
				return errors.New("must be a positive integer")
			}
			if defaultGB > 0 && int32(n) < defaultGB {
				return fmt.Errorf("must be at least %d GB (the AMI's own default)", defaultGB)
			}
			return nil
		}),
		ui.WithIO(input, output),
	)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("parsing root volume size %q: %w", s, err)
	}
	return int32(n), nil
}

// rootVolumeSizeDisplay formats a LaunchTemplateVersionDetail's
// RootVolumeSizeGB for Show Launch Template -- 0 means the version
// never set an override, so the AMI's own default applies.
func rootVolumeSizeDisplay(gb int32) string {
	if gb == 0 {
		return "(AMI default)"
	}
	return fmt.Sprintf("%d GB", gb)
}
