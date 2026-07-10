package workflow

import (
	"context"
	"io"
	"time"

	"github.com/rsdoiel/termlib"

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
func ShowCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image) error {
	return showCloudInit(ctx, t, le, ec2Clients, ssmClients, instances, images, nil, nil)
}

// showCloudInit is ShowCloudInit's testable core: kindInput/kindOutput
// are nil in production (the Instance-vs-AMI kind picker runs
// interactively on the real terminal, DESIGN.md's full conversion
// punch list) and are supplied by tests to drive it through its
// accessible-mode pipe path instead (DECISIONS.md, "huh fields are
// pipe-testable..."), separate from le, which still feeds every other
// prompt in this function.
func showCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image, kindInput io.Reader, kindOutput io.Writer) error {
	kind, err := pickString(t, "Show/export cloud-init for", "(q to cancel)", []string{"Instance", "AMI"}, kindInput, kindOutput)
	if err != nil {
		return cancelledIsNil(t, err)
	}

	switch kind {
	case "Instance":
		if len(instances) == 0 {
			t.Println("No instances found.")
			t.Refresh()
			return nil
		}
		inst, err := pickInstance(ctx, "Select an instance", instances)
		if err != nil {
			return cancelledIsNil(t, err)
		}
		return showCloudInitForInstance(ctx, t, le, ec2Clients, inst)

	case "AMI":
		if len(images) == 0 {
			t.Println("No AMIs found.")
			t.Refresh()
			return nil
		}
		img, err := pickImage(ctx, "Select an AMI", images)
		if err != nil {
			return cancelledIsNil(t, err)
		}
		return showCloudInitForAMI(ctx, t, le, ec2Clients, ssmClients, img)
	}
	return nil
}

// showCloudInitForInstance is showCloudInit's testable core for the
// "Instance" branch, once an instance is resolved -- instance selection
// runs a real bubbletea Program (tui.RunPicker, DESIGN.md's full
// conversion punch list) that can't be driven by a test's pipe input,
// same limitation as every other Picker-tier conversion this session.
func showCloudInitForInstance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, inst inventory.Instance) error {
	ec2Client, err := resolveEC2(ec2Clients, inst.Region)
	if err != nil {
		return err
	}
	data, set, err := ShowCloudInitFromInstance(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return err
	}
	if !set {
		t.Printf("No user-data was set at launch for %s.\n", inst.InstanceID)
		t.Refresh()
		return nil
	}
	return displayAndExportCloudInit(t, le, data)
}

// showCloudInitForAMI is showCloudInit's testable core for the "AMI"
// branch, once an AMI is resolved -- same limitation as
// showCloudInitForInstance above.
func showCloudInitForAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, img inventory.Image) error {
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, img.Region)
	if err != nil {
		return err
	}
	ok, err := Confirm(t, le, "Extracting cloud-init from an AMI launches a temporary billable instance. Proceed?")
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}
	stopTicker := startProgressTicker(t, 30*time.Second, "extracting cloud-init from a temporary instance")
	data, err := ExtractCloudInitFromAMI(ctx, ec2Client, ssmClient, img.ImageID, DefaultCloudInitExtractionTimeout, DefaultSSMPollInterval)
	stopTicker()
	if err != nil {
		return err
	}
	return displayAndExportCloudInit(t, le, data)
}

func displayAndExportCloudInit(t *termlib.Terminal, le *termlib.LineEditor, userData string) error {
	t.Println("\n--- cloud-init ---")
	t.Println(userData)
	t.Println("------------------")
	t.Refresh()

	return exportCloudInit(t, le, userData)
}
