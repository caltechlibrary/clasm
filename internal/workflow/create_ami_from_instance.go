package workflow

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
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
func CollectCreateAMIParams(w io.Writer, inst inventory.Instance, now time.Time, input io.Reader, output io.Writer) (CreateAMIParams, error) {
	nameOrID := inst.Name
	if nameOrID == "" {
		nameOrID = inst.InstanceID
	}

	name, err := ui.Prompt("AMI name", ui.WithDefault(defaultAMIName(nameOrID, now)), ui.WithValidator(validateAMIName), ui.WithIO(input, output))
	if err != nil {
		return CreateAMIParams{}, err
	}

	description, err := ui.Prompt("AMI description (optional)", ui.WithIO(input, output))
	if err != nil {
		return CreateAMIParams{}, err
	}

	var noReboot bool
	if inst.State == "running" {
		noReboot, err = Confirm("Skip reboot before snapshotting (faster, but crash-consistent only)?", WithConfirmIO(input, output))
		if err != nil {
			return CreateAMIParams{}, err
		}
	}

	var projectOpts []ui.PromptOption
	if inst.Project != "" {
		projectOpts = append(projectOpts, ui.WithDefault(inst.Project))
	}
	projectOpts = append(projectOpts, ui.WithIO(input, output))
	project, err := ui.Prompt("Project tag", projectOpts...)
	if err != nil {
		return CreateAMIParams{}, err
	}

	environment, err := ui.Prompt("Environment tag (production, development, or test)", ui.WithValidator(validateEnvironment), ui.WithIO(input, output))
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
func CreateAMIFromInstance(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to create an AMI from", "The instance can be running or stopped; a running instance is briefly rebooted unless you skip that step.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return createAMIFromInstance(ctx, w, ec2Clients, ssmClients, inst, nil, nil)
}

// createAMIFromInstance is CreateAMIFromInstance's testable core, once
// an instance is resolved -- instance selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as
// backupArchiveAndTrim (backup_archive.go). input/output are nil in
// production (interactive, real terminal) and supplied by tests to
// drive every prompt/confirm in this function through its accessible-
// mode pipe path instead.
func createAMIFromInstance(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, inst inventory.Instance, input io.Reader, output io.Writer) error {
	client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, inst.Region)
	if err != nil {
		return err
	}

	volumes, totalGB, hasPriorSnapshot, err := GatherVolumeInfo(ctx, client, inst.InstanceID)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nAttached volumes: %d, total %d GiB. Estimated creation time: %s\n", len(volumes), totalGB, EstimateAMICreationTime(totalGB))
	if hasPriorSnapshot {
		fmt.Fprintln(w, "Note: one or more volumes have a prior snapshot -- only changed blocks will be copied. Actual time may be significantly shorter.")
	}

	if inst.State == "running" {
		fmt.Fprintln(w, crashConsistencyGuidance)
		if err := offerFstrimIfAvailable(ctx, w, ssmClient, inst.InstanceID, input, output); err != nil {
			return err
		}
	}

	params, err := CollectCreateAMIParams(w, inst, time.Now(), input, output)
	if err != nil {
		return err
	}

	ok, err := Confirm(fmt.Sprintf("Create AMI %q from %s?", params.Name, inst.InstanceID), WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	imageID, err := CreateAMI(ctx, client, params)
	if err != nil {
		return fmt.Errorf("creating AMI: %w", err)
	}

	fmt.Fprintf(w, "AMI creation started: %s. Waiting (estimated %s)...\n", imageID, EstimateAMICreationTime(totalGB))

	start := time.Now()
	stopTicker := startProgressTicker(w, "waiting for AMI to become available")
	state, err := WaitForAMIAvailable(ctx, client, imageID, DefaultAMIPollInterval)
	stopTicker()
	if err != nil {
		return err
	}
	elapsed := formatDuration(time.Since(start))

	if state == types.ImageStateAvailable {
		fmt.Fprintf(w, "AMI %s is now available (took %s).\n", imageID, elapsed)
	} else {
		fmt.Fprintf(w, "AMI %s creation failed after %s.\n", imageID, elapsed)
	}
	return nil
}
