package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// pickInstance runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over instances and returns the chosen one.
// Like pickBucket (Phase 20.4), this drives a real bubbletea Program
// that can't be pipe-tested -- every caller splits into a thin entry
// point (calls pickInstance) and a testable core taking the already-
// resolved instance directly.
func pickInstance(ctx context.Context, title string, instances []inventory.Instance) (inventory.Instance, error) {
	rows := make([]string, len(instances))
	for i, inst := range instances {
		rows[i] = instanceLabel(inst)
	}

	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.Instance{}, err
	}
	return instances[idx], nil
}

// StartInstance calls ec2:StartInstances for a single instance.
func StartInstance(ctx context.Context, client awsclient.EC2API, instanceID string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	_, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{instanceID}})
	return err
}

// StartEC2Instance runs the full Start EC2 Instance workflow (DESIGN.md,
// Feature 4): pick a stopped instance, confirm (a simple yes/no --
// starting is safe and reversible, the symmetric counterpart to Stop),
// start it, wait for running, and display connection info. Returns nil
// (not an error) on cancellation or when there are no stopped instances
// to pick from. Takes a per-region client map (Phase 2 aggregates
// instances across all four regions) and resolves the one matching the
// picked instance's region.
func StartEC2Instance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, instances []inventory.Instance) error {
	stopped := filterInstancesByState(instances, "stopped")
	if len(stopped) == 0 {
		t.Println("No stopped instances found.")
		t.Refresh()
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to start", stopped)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	return startEC2Instance(ctx, t, le, clients, inst)
}

// startEC2Instance is StartEC2Instance's testable core, once an instance
// is resolved -- instance selection runs a real bubbletea Program
// (tui.RunPicker, DESIGN.md's full conversion punch list) that can't be
// driven by a test's pipe input, same limitation as pickBucket (Phase
// 20.4).
func startEC2Instance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, inst inventory.Instance) error {
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
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

// StopInstance calls ec2:StopInstances for a single instance.
func StopInstance(ctx context.Context, client awsclient.EC2API, instanceID string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	_, err := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{instanceID}})
	return err
}

// WaitUntilStopped polls ec2:DescribeInstances until instanceID reaches
// the stopped state or the timeout elapses -- shares its polling
// mechanics with WaitUntilRunning via waitUntilState.
func WaitUntilStopped(ctx context.Context, client awsclient.EC2API, instanceID string, timeout, pollInterval time.Duration) (types.Instance, error) {
	return waitUntilState(ctx, client, instanceID, types.InstanceStateNameStopped, timeout, pollInterval)
}

// StopEC2Instance runs the full Stop EC2 Instance workflow (DESIGN.md,
// Feature 5): pick a running instance, confirm (a simple yes/no --
// stopping is reversible; data on EBS volumes persists and the instance
// can be started again), stop it, and wait for stopped. Returns nil
// (not an error) on cancellation or when there are no running instances
// to pick from. Takes a per-region client map and resolves the one
// matching the picked instance's region, same as StartEC2Instance.
func StopEC2Instance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, instances []inventory.Instance) error {
	running := filterInstancesByState(instances, "running")
	if len(running) == 0 {
		t.Println("No running instances found.")
		t.Refresh()
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to stop", running)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	return stopEC2Instance(ctx, t, le, clients, inst)
}

// stopEC2Instance is StopEC2Instance's testable core, once an instance
// is resolved -- same limitation as startEC2Instance above.
func stopEC2Instance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, inst inventory.Instance) error {
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}

	ok, err := Confirm(t, le, fmt.Sprintf("Stop instance %s (%s)?", inst.InstanceID, inst.Name))
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if err := StopInstance(ctx, client, inst.InstanceID); err != nil {
		return fmt.Errorf("stopping instance %s: %w", inst.InstanceID, err)
	}

	t.Printf("Stopping %s, waiting for it to reach stopped...\n", inst.InstanceID)
	t.Refresh()
	if _, err := WaitUntilStopped(ctx, client, inst.InstanceID, DefaultLaunchTimeout, DefaultLaunchPollInterval); err != nil {
		return err
	}

	t.Printf("Instance %s is now stopped.\n", inst.InstanceID)
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
