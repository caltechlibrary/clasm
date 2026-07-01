package workflow

import (
	"context"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// ShowCloudInit runs the full Show/Export Cloud-Init workflow (DESIGN.md,
// Feature 10): pick an instance (free, instant) or an AMI (costs real
// time/money -- requires its own explicit confirmation), display the
// decoded cloud-init, then offer to export it to a local file for
// manual comparison against a local clone of
// caltechlibrary/cloud-init-examples. Takes per-region client maps and
// resolves the ones matching the picked instance/AMI's region.
func ShowCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image) error {
	kind, err := ui.PickList(t, le, []string{"Instance", "AMI"}, identity, "Show/export cloud-init for")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	var userData string
	switch kind {
	case "Instance":
		if len(instances) == 0 {
			t.Println("No instances found.")
			t.Refresh()
			return nil
		}
		inst, err := ui.PickList(t, le, instances, instanceLabel, "Select an instance")
		if err != nil {
			return cancelledIsNil(t, err)
		}
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
		userData = data

	case "AMI":
		if len(images) == 0 {
			t.Println("No AMIs found.")
			t.Refresh()
			return nil
		}
		img, err := ui.PickList(t, le, images, imageLabel, "Select an AMI")
		if err != nil {
			return cancelledIsNil(t, err)
		}
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
		data, err := ExtractCloudInitFromAMI(ctx, ec2Client, ssmClient, img.ImageID, DefaultCloudInitExtractionTimeout, DefaultSSMPollInterval)
		if err != nil {
			return err
		}
		userData = data
	}

	t.Println("\n--- cloud-init ---")
	t.Println(userData)
	t.Println("------------------")
	t.Refresh()

	return exportCloudInit(t, le, userData)
}
