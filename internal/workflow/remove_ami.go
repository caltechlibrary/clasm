package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
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
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
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
func RemoveAMI(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, images []inventory.Image, instances []inventory.Instance) error {
	if len(images) == 0 {
		fmt.Fprintln(w, "No AMIs found.")
		return nil
	}

	img, err := pickImage(ctx, "Select an AMI to remove", images)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return removeAMI(ctx, w, clients, img, instances, nil, nil)
}

// removeAMI is RemoveAMI's testable core, once an AMI is resolved -- AMI
// selection runs a real bubbletea Program (tui.RunPicker, DESIGN.md's
// full conversion punch list) that can't be driven by a test's pipe
// input, same limitation as createAMIFromInstance
// (create_ami_from_instance.go). input/output are nil in production and
// supplied by tests to drive the type-to-confirm gate through its
// accessible-mode pipe path instead.
func removeAMI(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, img inventory.Image, instances []inventory.Instance, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, img.Region)
	if err != nil {
		return err
	}
	params := RemoveAMIParams{ImageID: img.ImageID}

	dependents := instancesUsingAMI(instances, img.ImageID)
	ok, err := confirmRemoveAMI(w, img, dependents, input, output)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := DeregisterAMI(ctx, client, params.ImageID); err != nil {
		return fmt.Errorf("removing AMI %s: %w", params.ImageID, err)
	}

	fmt.Fprintf(w, "AMI %s removed.\n", params.ImageID)
	return nil
}

// confirmRemoveAMI shows the dry-run display (what would be deleted,
// which instances depend on it), an Environment=production warning if
// applicable, then runs the type-to-confirm gate.
func confirmRemoveAMI(w io.Writer, img inventory.Image, dependents []inventory.Instance, input io.Reader, output io.Writer) (bool, error) {
	fmt.Fprintf(w, "\n=== DRY RUN: removing AMI %s (%s) ===\n", img.ImageID, img.Name)
	if len(dependents) > 0 {
		labels := make([]string, 0, len(dependents))
		for _, inst := range dependents {
			labels = append(labels, fmt.Sprintf("%s (%s)", inst.InstanceID, inst.Name))
		}
		fmt.Fprintf(w, "WARNING: %d instance(s) currently reference this AMI: %s\n", len(dependents), strings.Join(labels, ", "))
	}
	if img.Environment == "production" {
		fmt.Fprintln(w, "WARNING: this AMI is tagged Environment=production.")
	}

	return ConfirmDestructive([]string{img.ImageID, img.Name}, WithConfirmIO(input, output))
}
