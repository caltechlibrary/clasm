package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// ShowCloudInit runs the full Show/Export Cloud-Init workflow (DESIGN.md,
// Feature 10): pick an instance (free, instant) or an AMI (costs real
// time/money -- requires its own explicit confirmation), display the
// decoded cloud-init, then offer to export it to a local file for
// manual comparison against a local clone of
// caltechlibrary/cloud-init-examples. Takes per-region client maps and
// resolves the ones matching the picked instance/AMI's region.
func ShowCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image) error {
	return showCloudInit(ctx, w, ec2Clients, ssmClients, instances, images, nil, nil)
}

// showCloudInit is ShowCloudInit's testable core: kindInput/kindOutput
// are nil in production (the Instance-vs-AMI kind picker runs
// interactively on the real terminal, DESIGN.md's full conversion
// punch list) and are supplied by tests to drive it through its
// accessible-mode pipe path instead (DECISIONS.md, "huh fields are
// pipe-testable...").
func showCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image, kindInput io.Reader, kindOutput io.Writer) error {
	kind, err := pickString(w, "Show/export cloud-init for", "An instance's cloud-init is free to read; an AMI's requires launching a temporary billable instance to extract it.", hintCancel, []string{"Instance", "AMI"}, kindInput, kindOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	switch kind {
	case "Instance":
		if len(instances) == 0 {
			fmt.Fprintln(w, "No instances found.")
			return nil
		}
		inst, err := pickInstance(ctx, "Select an instance", "Reading an instance's cloud-init is free and instant.", instances)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		return showCloudInitForInstance(ctx, w, ec2Clients, inst, kindInput, kindOutput)

	case "AMI":
		if len(images) == 0 {
			fmt.Fprintln(w, "No AMIs found.")
			return nil
		}
		img, err := pickImage(ctx, "Select an AMI", "Extracting cloud-init from an AMI launches a temporary billable instance to read it.", images)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		return showCloudInitForAMI(ctx, w, ec2Clients, ssmClients, img, kindInput, kindOutput)
	}
	return nil
}

// showCloudInitForInstance is showCloudInit's testable core for the
// "Instance" branch, once an instance is resolved -- instance selection
// runs a real bubbletea Program (tui.RunPicker, DESIGN.md's full
// conversion punch list) that can't be driven by a test's pipe input,
// same limitation as every other Picker-tier conversion this session.
func showCloudInitForInstance(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, inst inventory.Instance, input io.Reader, output io.Writer) error {
	ec2Client, err := resolveEC2(ec2Clients, inst.Region)
	if err != nil {
		return err
	}
	data, set, err := ShowCloudInitFromInstance(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return err
	}
	if !set {
		fmt.Fprintf(w, "No user-data was set at launch for %s.\n", inst.InstanceID)
		return nil
	}
	return displayAndExportCloudInit(w, data, input, output)
}

// showCloudInitForAMI is showCloudInit's testable core for the "AMI"
// branch, once an AMI is resolved -- same limitation as
// showCloudInitForInstance above.
func showCloudInitForAMI(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, img inventory.Image, input io.Reader, output io.Writer) error {
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, img.Region)
	if err != nil {
		return err
	}
	ok, err := Confirm("Extracting cloud-init from an AMI launches a temporary billable instance. Proceed?", WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}
	stopTicker := startProgressTicker(w, "extracting cloud-init from a temporary instance")
	data, err := ExtractCloudInitFromAMI(ctx, ec2Client, ssmClient, img.ImageID, DefaultCloudInitExtractionTimeout, DefaultSSMPollInterval)
	stopTicker()
	if err != nil {
		return err
	}
	return displayAndExportCloudInit(w, data, input, output)
}

func displayAndExportCloudInit(w io.Writer, userData string, input io.Reader, output io.Writer) error {
	fmt.Fprintln(w, "\n--- cloud-init ---")
	fmt.Fprintln(w, userData)
	fmt.Fprintln(w, "------------------")

	return exportCloudInit(w, userData, input, output)
}
