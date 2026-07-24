package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// ShowInstanceDetail runs the Show Instance Detail workflow (DESIGN.md,
// "Instance/AMI Detail Views"): pick an instance, then display its
// curated detail fields plus attached EBS volume sizes.
func ShowInstanceDetail(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}
	inst, err := pickInstance(ctx, "Select an instance", "", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return showInstanceDetail(ctx, w, clients, inst)
}

// showInstanceDetail is ShowInstanceDetail's testable core, once an
// instance is resolved -- instance selection runs a real bubbletea
// Program (tui.RunPicker) that can't be pipe-tested, same limitation as
// every other Picker-tier conversion in this package.
func showInstanceDetail(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, inst inventory.Instance) error {
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}
	detail, err := inventory.DescribeInstanceDetail(ctx, client, inst.Region, inst.InstanceID)
	if err != nil {
		return err
	}
	volumes, totalGB, _, err := GatherVolumeInfo(ctx, client, inst.InstanceID)
	if err != nil {
		return err
	}
	displayInstanceDetail(w, detail, volumes, totalGB)
	return nil
}

// displayInstanceDetail prints an instance's curated detail fields plus
// attached EBS volume sizes, matching displayLaunchTemplateVersion's
// curated-field style (show_launch_template.go).
func displayInstanceDetail(w io.Writer, detail inventory.InstanceDetail, volumes []VolumeInfo, totalVolumeGB int32) {
	fmt.Fprintf(w, "\nInstance %s (%s), region %s\n", detail.InstanceID, displayOrNone(detail.Name), detail.Region)
	fmt.Fprintf(w, "  State:                %s\n", displayOrNone(detail.State))
	fmt.Fprintf(w, "  Instance type:        %s\n", displayOrNone(detail.InstanceType))
	fmt.Fprintf(w, "  AMI:                  %s\n", displayOrNone(detail.ImageID))
	fmt.Fprintf(w, "  VPC:                  %s\n", displayOrNone(detail.VPCID))
	fmt.Fprintf(w, "  Subnet:               %s\n", displayOrNone(detail.SubnetID))
	fmt.Fprintf(w, "  Security groups:      %s\n", displayOrNone(strings.Join(detail.SecurityGroupIDs, ", ")))
	fmt.Fprintf(w, "  IAM instance profile: %s\n", displayOrNone(detail.IAMInstanceProfile))
	fmt.Fprintf(w, "  Key pair:             %s\n", displayOrNone(detail.KeyName))
	fmt.Fprintf(w, "  Public IP:            %s\n", displayOrNone(detail.PublicIP))
	fmt.Fprintf(w, "  Private IP:           %s\n", displayOrNone(detail.PrivateIP))
	fmt.Fprintf(w, "  Project:              %s\n", displayOrNone(detail.Project))
	fmt.Fprintf(w, "  Environment:          %s\n", displayOrNone(detail.Environment))
	if len(volumes) == 0 {
		fmt.Fprintln(w, "  EBS volumes:          none")
		return
	}
	fmt.Fprintf(w, "  EBS volumes:          %d GiB total across %d volume(s)\n", totalVolumeGB, len(volumes))
	for _, v := range volumes {
		fmt.Fprintf(w, "    - %s: %d GiB\n", v.VolumeID, v.SizeGB)
	}
}
