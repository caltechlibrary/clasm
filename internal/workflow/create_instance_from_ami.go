package workflow

import (
	"context"
	"io"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// CreateInstanceFromAMI runs the full Create EC2 Instance from AMI
// workflow (DESIGN.md, Feature 2): collect parameters AMI-first, then
// confirm/launch/wait/cloud-init-check via runLaunch, shared with
// CreateInstanceFromCloudInit (Feature 3). Returns nil (not an error) on
// user cancellation, matching every other workflow's confirmation gate.
// Takes per-region client maps (Phase 2 aggregates instances/AMIs across
// all four regions, so the specific client needed isn't known until
// after the AMI is picked) -- CollectLaunchInstanceParams resolves and
// returns the region-specific clients itself, since it also needs them
// to list key pairs/security groups/subnets.
func CreateInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) error {
	image, err := pickImage(ctx, "Select an AMI", imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return cancelledIsNil(t, err)
	}
	return createInstanceFromAMI(ctx, t, le, ec2Clients, ssmClients, iamClient, image, nil, nil)
}

// createInstanceFromAMI is CreateInstanceFromAMI's testable core, once
// an AMI is resolved -- AMI selection runs a real bubbletea Program
// (tui.RunPicker, DESIGN.md's full conversion punch list) that can't be
// driven by a test's pipe input, same limitation as every other
// Picker-tier conversion this session. menuInput/menuOutput are nil in
// production (the instance-type huh.Select and its ENA/AZ
// incompatibility-remediation huh.Selects run interactively on the real
// terminal) and are supplied by tests to drive them through their
// accessible-mode pipe path instead, separate from le, which still feeds
// every other prompt in this function.
func createInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, image inventory.Image, menuInput io.Reader, menuOutput io.Writer) error {
	params, ec2Client, ssmClient, err := collectLaunchInstanceParams(ctx, t, le, ec2Clients, ssmClients, iamClient, image, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	return runLaunch(ctx, t, le, ec2Client, ssmClient, params)
}
