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

// TerminateInstanceParams is the resolved parameter set for terminating
// an instance -- kept minimal (just the ID) since the dry-run/warning
// data displayed during confirmation is fetched fresh, not carried in
// this struct (see DECISIONS.md, "Structure workflows for future
// record/replay").
type TerminateInstanceParams struct {
	InstanceID string
}

// TerminateInstance calls ec2:TerminateInstances for a single instance.
func TerminateInstance(ctx context.Context, client awsclient.EC2API, instanceID string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	_, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	return err
}

// TerminateEC2Instance runs the full Terminate EC2 Instance workflow
// (DESIGN.md, Feature 6): pick an instance, dry-run (including any
// DeleteOnTermination-flagged EBS volume), an Environment=production
// warning if tagged, type-to-confirm (instance ID or Name), then
// terminate. Same safety tier as Feature 9 (Remove AMI), since
// termination is permanent. Returns nil (not an error) on cancellation
// or when there are no instances to pick from. Takes a per-region client
// map and resolves the one matching the picked instance's region.
func TerminateEC2Instance(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to terminate", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return terminateEC2Instance(ctx, w, clients, inst, nil, nil)
}

// terminateEC2Instance is TerminateEC2Instance's testable core, once an
// instance is resolved -- instance selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as
// startEC2Instance/stopEC2Instance (power_state.go). input/output are
// nil in production and supplied by tests to drive the type-to-confirm
// gate through its accessible-mode pipe path instead.
func terminateEC2Instance(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, inst inventory.Instance, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}
	params := TerminateInstanceParams{InstanceID: inst.InstanceID}

	ok, err := confirmTerminate(ctx, w, client, params, inst, input, output)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := TerminateInstance(ctx, client, params.InstanceID); err != nil {
		return fmt.Errorf("terminating instance %s: %w", params.InstanceID, err)
	}

	fmt.Fprintf(w, "Instance %s termination initiated.\n", params.InstanceID)
	return nil
}

// confirmTerminate fetches the instance's current block device mappings
// for the dry-run display, shows an Environment=production warning if
// applicable, then runs the type-to-confirm gate.
func confirmTerminate(ctx context.Context, w io.Writer, client awsclient.EC2API, params TerminateInstanceParams, inst inventory.Instance, input io.Reader, output io.Writer) (bool, error) {
	describeCtx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstances(describeCtx, &ec2.DescribeInstancesInput{InstanceIds: []string{params.InstanceID}})
	if err != nil {
		return false, err
	}
	sdkInst, found := findInstance(out, params.InstanceID)
	if !found {
		return false, fmt.Errorf("instance %s not found", params.InstanceID)
	}

	fmt.Fprintf(w, "\n=== DRY RUN: terminating %s (%s) ===\n", params.InstanceID, inst.Name)
	var flaggedDevices []string
	for _, bdm := range sdkInst.BlockDeviceMappings {
		if bdm.Ebs != nil && aws.ToBool(bdm.Ebs.DeleteOnTermination) {
			flaggedDevices = append(flaggedDevices, aws.ToString(bdm.DeviceName))
		}
	}
	if len(flaggedDevices) > 0 {
		fmt.Fprintf(w, "WARNING: DeleteOnTermination is set on these volumes -- their data (potentially including not-yet-archived backups) will be destroyed along with the instance: %s\n",
			strings.Join(flaggedDevices, ", "))
	}
	if inst.Environment == "production" {
		fmt.Fprintln(w, "WARNING: this instance is tagged Environment=production.")
	}

	return ConfirmDestructive([]string{params.InstanceID, inst.Name}, WithConfirmIO(input, output))
}
