package workflow

import (
	"context"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// CreateInstanceFromCloudInit runs the full Create EC2 Instance from
// Cloud-Init YAML workflow (DESIGN.md, Feature 3): collect parameters
// cloud-init-first (then pick a base AMI), then confirm/launch/wait/
// cloud-init-check via runLaunch, shared with CreateInstanceFromAMI
// (Feature 2). Returns nil (not an error) on user cancellation, matching
// every other workflow's confirmation gate. Takes per-region client maps;
// CollectLaunchInstanceParamsFromCloudInit resolves and returns the
// region-specific clients itself, same as CreateInstanceFromAMI.
func CreateInstanceFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) error {
	userData, err := promptCloudInitYAMLFile(w, nil, nil)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	image, err := pickImage(ctx, "Select a base AMI", imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return createInstanceFromCloudInit(ctx, w, ec2Clients, ssmClients, iamClient, userData, image, nil, nil)
}

// createInstanceFromCloudInit is CreateInstanceFromCloudInit's testable
// core, once the cloud-init YAML is read and an AMI is resolved -- AMI
// selection runs a real bubbletea Program (tui.RunPicker, DESIGN.md's
// full conversion punch list) that can't be driven by a test's pipe
// input, same limitation as every other Picker-tier conversion this
// session. menuInput/menuOutput are nil in production (the instance-type
// huh.Select and its ENA/AZ incompatibility-remediation huh.Selects run
// interactively on the real terminal) and are supplied by tests to drive
// them through their accessible-mode pipe path instead.
func createInstanceFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, userData string, image inventory.Image, menuInput io.Reader, menuOutput io.Writer) error {
	params, ec2Client, ssmClient, err := collectLaunchInstanceParamsFromCloudInit(ctx, w, ec2Clients, ssmClients, iamClient, userData, image, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return runLaunch(ctx, w, ec2Client, ssmClient, params, menuInput, menuOutput)
}
