package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// crashConsistencyGuidance is shown before offering fstrim on a running
// instance -- carried forward from ami_copy_basic_steps.md via
// DESIGN.md's "Domain Knowledge Carried Forward" section.
const crashConsistencyGuidance = `Crash-consistency for a running-instance snapshot (--no-reboot):
  - PostgreSQL and OpenSearch replay their logs on first boot and recover cleanly
  - Redis session/cache data may be lost (ephemeral by design)
  - Docker container images on disk are unaffected`

// CollectCreateAMIParams interactively collects a CreateAMIParams: AMI
// name (defaulted to "<instance-name-or-id>-copy-<date>"), description,
// no-reboot (only offered for a running instance), and tags -- Project
// defaults to the source instance's Project tag if set; Environment is
// always an explicit, validated prompt with no default.
func CollectCreateAMIParams(t *termlib.Terminal, le *termlib.LineEditor, inst inventory.Instance, now time.Time) (CreateAMIParams, error) {
	nameOrID := inst.Name
	if nameOrID == "" {
		nameOrID = inst.InstanceID
	}

	name, err := ui.Prompt(t, le, "AMI name", ui.WithDefault(defaultAMIName(nameOrID, now)), ui.WithValidator(validateAMIName))
	if err != nil {
		return CreateAMIParams{}, err
	}

	description, err := ui.Prompt(t, le, "AMI description (optional)")
	if err != nil {
		return CreateAMIParams{}, err
	}

	var noReboot bool
	if inst.State == "running" {
		noReboot, err = Confirm(t, le, "Skip reboot before snapshotting (faster, but crash-consistent only)?")
		if err != nil {
			return CreateAMIParams{}, err
		}
	}

	var projectOpts []ui.PromptOption
	if inst.Project != "" {
		projectOpts = append(projectOpts, ui.WithDefault(inst.Project))
	}
	project, err := ui.Prompt(t, le, "Project tag", projectOpts...)
	if err != nil {
		return CreateAMIParams{}, err
	}

	environment, err := ui.Prompt(t, le, "Environment tag (production, development, or test)", ui.WithValidator(validateEnvironment))
	if err != nil {
		return CreateAMIParams{}, err
	}

	return CreateAMIParams{
		InstanceID:  inst.InstanceID,
		Name:        name,
		Description: description,
		NoReboot:    noReboot,
		Tags: map[string]string{
			"Name":        name,
			"Project":     project,
			"Environment": environment,
		},
	}, nil
}

// CreateAMIFromInstance runs the full Create AMI from EC2 Instance
// workflow (DESIGN.md, Feature 8): pick an instance (running or
// stopped), gather volume info and show the time estimate, offer fstrim
// and crash-consistency guidance if running, collect AMI params,
// confirm, create, and wait (unboundedly) for available/failed. Takes
// per-region client maps and resolves the ones matching the picked
// instance's region.
func CreateAMIFromInstance(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		t.Println("No instances found.")
		t.Refresh()
		return nil
	}

	inst, err := ui.PickList(t, le, instances, instanceLabel, "Select an instance to create an AMI from")
	if err != nil {
		return cancelledIsNil(t, err)
	}
	client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, inst.Region)
	if err != nil {
		return err
	}

	volumes, totalGB, hasPriorSnapshot, err := GatherVolumeInfo(ctx, client, inst.InstanceID)
	if err != nil {
		return err
	}
	t.Printf("\nAttached volumes: %d, total %d GiB. Estimated creation time: %s\n", len(volumes), totalGB, EstimateAMICreationTime(totalGB))
	if hasPriorSnapshot {
		t.Println("Note: one or more volumes have a prior snapshot -- only changed blocks will be copied. Actual time may be significantly shorter.")
	}
	t.Refresh()

	if inst.State == "running" {
		t.Println(crashConsistencyGuidance)
		t.Refresh()
		if err := offerFstrimIfAvailable(ctx, t, le, ssmClient, inst.InstanceID); err != nil {
			return err
		}
	}

	params, err := CollectCreateAMIParams(t, le, inst, time.Now())
	if err != nil {
		return err
	}

	ok, err := Confirm(t, le, fmt.Sprintf("Create AMI %q from %s?", params.Name, inst.InstanceID))
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	imageID, err := CreateAMI(ctx, client, params)
	if err != nil {
		return fmt.Errorf("creating AMI: %w", err)
	}

	t.Printf("AMI creation started: %s. Waiting (estimated %s)...\n", imageID, EstimateAMICreationTime(totalGB))
	t.Refresh()

	start := time.Now()
	stopTicker := startProgressTicker(t, 30*time.Second, "waiting for AMI to become available")
	state, err := WaitForAMIAvailable(ctx, client, imageID, DefaultAMIPollInterval)
	stopTicker()
	if err != nil {
		return err
	}
	elapsed := termlib.FormatDuration(time.Since(start))

	if state == types.ImageStateAvailable {
		t.Printf("AMI %s is now available (took %s).\n", imageID, elapsed)
	} else {
		t.Printf("AMI %s creation failed after %s.\n", imageID, elapsed)
	}
	t.Refresh()
	return nil
}
