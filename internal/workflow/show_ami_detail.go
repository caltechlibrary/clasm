package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// ShowAMIDetail runs the Show AMI Detail workflow (DESIGN.md,
// "Instance/AMI Detail Views"): pick an AMI, then display its curated
// detail fields plus block device mappings.
func ShowAMIDetail(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, images []inventory.Image) error {
	if len(images) == 0 {
		fmt.Fprintln(w, "No AMIs found.")
		return nil
	}
	img, err := pickImage(ctx, "Select an AMI", "", images)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return showAMIDetail(ctx, w, clients, img)
}

// showAMIDetail is ShowAMIDetail's testable core, once an AMI is
// resolved -- AMI selection runs a real bubbletea Program
// (tui.RunPicker) that can't be pipe-tested, same limitation as every
// other Picker-tier conversion in this package.
func showAMIDetail(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, img inventory.Image) error {
	client, err := resolveEC2(clients, img.Region)
	if err != nil {
		return err
	}
	detail, err := inventory.DescribeImageDetail(ctx, client, img.Region, img.ImageID)
	if err != nil {
		return err
	}
	displayAMIDetail(w, detail)
	return nil
}

// displayAMIDetail prints an AMI's curated detail fields plus block
// device mappings, matching displayLaunchTemplateVersion's curated-field
// style (show_launch_template.go).
func displayAMIDetail(w io.Writer, detail inventory.ImageDetail) {
	fmt.Fprintf(w, "\nAMI %s (%s), region %s\n", detail.ImageID, displayOrNone(detail.Name), detail.Region)
	fmt.Fprintf(w, "  Creation date:    %s\n", displayOrNone(detail.CreationDate))
	fmt.Fprintf(w, "  Architecture:     %s\n", displayOrNone(detail.Architecture))
	if detail.EnaSupport {
		fmt.Fprintln(w, "  ENA support:      yes")
	} else {
		fmt.Fprintln(w, "  ENA support:      no")
	}
	fmt.Fprintf(w, "  Root device:      %s\n", displayOrNone(detail.RootDeviceName))
	fmt.Fprintf(w, "  Project:          %s\n", displayOrNone(detail.Project))
	fmt.Fprintf(w, "  Environment:      %s\n", displayOrNone(detail.Environment))
	if len(detail.BlockDeviceMappings) == 0 {
		fmt.Fprintln(w, "  Block devices:    none")
		return
	}
	fmt.Fprintln(w, "  Block devices:")
	for _, bdm := range detail.BlockDeviceMappings {
		fmt.Fprintf(w, "    - %s: %d GiB (snapshot %s)\n", bdm.DeviceName, bdm.VolumeSizeGB, displayOrNone(bdm.SnapshotID))
	}
}
