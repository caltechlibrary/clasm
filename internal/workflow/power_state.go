package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// StartInstance calls ec2:StartInstances for a single instance.
func StartInstance(ctx context.Context, client awsclient.EC2API, instanceID string) error {
	_, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{instanceID}})
	return err
}

// StartEC2Instance runs the full Start EC2 Instance workflow (DESIGN.md,
// Feature 4): pick a stopped instance, confirm (a simple yes/no --
// starting is safe and reversible, the symmetric counterpart to Stop),
// start it, wait for running, and display connection info. Returns nil
// (not an error) on cancellation or when there are no stopped instances
// to pick from.
func StartEC2Instance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, instances []inventory.Instance) error {
	stopped := filterInstancesByState(instances, "stopped")
	if len(stopped) == 0 {
		t.Println("No stopped instances found.")
		t.Refresh()
		return nil
	}

	inst, err := ui.PickList(t, le, stopped, instanceLabel, "Select an instance to start")
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			t.Println("Cancelled.")
			t.Refresh()
			return nil
		}
		return err
	}

	ok, err := Confirm(t, le, fmt.Sprintf("Start instance %s (%s)?", inst.InstanceID, inst.Name))
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if err := StartInstance(ctx, client, inst.InstanceID); err != nil {
		return fmt.Errorf("starting instance %s: %w", inst.InstanceID, err)
	}

	t.Printf("Starting %s, waiting for it to reach running...\n", inst.InstanceID)
	t.Refresh()
	running, err := WaitUntilRunning(ctx, client, inst.InstanceID, DefaultLaunchTimeout, DefaultLaunchPollInterval)
	if err != nil {
		return err
	}

	displayConnectionInfo(t, inst.InstanceID, running)
	t.Println("Note: the public IP may have changed since this instance was last running, unless it uses an Elastic IP.")
	t.Refresh()
	return nil
}

func filterInstancesByState(instances []inventory.Instance, state string) []inventory.Instance {
	var out []inventory.Instance
	for _, inst := range instances {
		if inst.State == state {
			out = append(out, inst)
		}
	}
	return out
}

func instanceLabel(inst inventory.Instance) string {
	name := inst.Name
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%s - %s (%s, %s)", inst.InstanceID, name, inst.State, inst.Region)
}
