package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// RemoveAMIParams is the resolved parameter set for removing an AMI --
// kept minimal (just the ID), matching TerminateInstanceParams's
// precedent (see DECISIONS.md, "Structure workflows for future
// record/replay").
type RemoveAMIParams struct {
	ImageID string
}

// DeregisterAMI calls ec2:DeregisterImage for a single AMI.
func DeregisterAMI(ctx context.Context, client awsclient.EC2API, imageID string) error {
	_, err := client.DeregisterImage(ctx, &ec2.DeregisterImageInput{ImageId: aws.String(imageID)})
	return err
}

// instancesUsingAMI finds instances whose ImageID matches imageID, from
// the already-fetched inventory listing -- no extra AWS call needed
// since Phase 2's ListInstances already carries each instance's ImageID.
func instancesUsingAMI(instances []inventory.Instance, imageID string) []inventory.Instance {
	var out []inventory.Instance
	for _, inst := range instances {
		if inst.ImageID == imageID {
			out = append(out, inst)
		}
	}
	return out
}

// RemoveAMI runs the full Remove AMI workflow (DESIGN.md, Feature 9):
// pick an owned AMI, dry-run (including any instance that still
// references it), an Environment=production warning if tagged,
// type-to-confirm (AMI ID or Name), then deregister. Same safety tier as
// Feature 6 (Terminate Instance). Returns nil (not an error) on
// cancellation or when there are no AMIs to pick from. Takes a
// per-region client map and resolves the one matching the picked AMI's
// region.
func RemoveAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, images []inventory.Image, instances []inventory.Instance) error {
	if len(images) == 0 {
		t.Println("No AMIs found.")
		t.Refresh()
		return nil
	}

	img, err := ui.PickList(t, le, images, imageLabel, "Select an AMI to remove")
	if err != nil {
		return cancelledIsNil(t, err)
	}
	client, err := resolveEC2(clients, img.Region)
	if err != nil {
		return err
	}
	params := RemoveAMIParams{ImageID: img.ImageID}

	dependents := instancesUsingAMI(instances, img.ImageID)
	ok, err := confirmRemoveAMI(t, le, img, dependents)
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if err := DeregisterAMI(ctx, client, params.ImageID); err != nil {
		return fmt.Errorf("removing AMI %s: %w", params.ImageID, err)
	}

	t.Printf("AMI %s removed.\n", params.ImageID)
	t.Refresh()
	return nil
}

// confirmRemoveAMI shows the dry-run display (what would be deleted,
// which instances depend on it), an Environment=production warning if
// applicable, then runs the type-to-confirm gate.
func confirmRemoveAMI(t *termlib.Terminal, le *termlib.LineEditor, img inventory.Image, dependents []inventory.Instance) (bool, error) {
	t.Printf("\n=== DRY RUN: removing AMI %s (%s) ===\n", img.ImageID, img.Name)
	if len(dependents) > 0 {
		labels := make([]string, 0, len(dependents))
		for _, inst := range dependents {
			labels = append(labels, fmt.Sprintf("%s (%s)", inst.InstanceID, inst.Name))
		}
		t.Printf("WARNING: %d instance(s) currently reference this AMI: %s\n", len(dependents), strings.Join(labels, ", "))
	}
	if img.Environment == "production" {
		t.Println("WARNING: this AMI is tagged Environment=production.")
	}
	t.Refresh()

	return ConfirmDestructive(t, le, img.ImageID, img.Name)
}
