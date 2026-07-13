package workflow

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// CollectLaunchInstanceParamsFromCloudInit interactively collects a
// LaunchInstanceParams leading with the cloud-init YAML, then picking a
// base AMI second -- the opposite order from CollectLaunchInstanceParams
// (Phase 4), which treats user data as one optional parameter among
// several. Both share the exact same remaining parameter set and
// execution path; only this front-end prompt order differs (see
// DECISIONS.md, "Add Create EC2 Instance from Cloud-Init YAML as a v1
// primitive"). Resolves and returns the region-specific clients itself,
// same as CollectLaunchInstanceParams, since security group/subnet
// listing needs them too.
func CollectLaunchInstanceParamsFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) (LaunchInstanceParams, awsclient.EC2API, awsclient.SSMAPI, error) {
	userData, err := promptCloudInitYAMLFile(w, nil, nil)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	image, err := pickImage(ctx, "Select a base AMI", imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	return collectLaunchInstanceParamsFromCloudInit(ctx, w, ec2Clients, ssmClients, iamClient, userData, image, nil, nil)
}

// collectLaunchInstanceParamsFromCloudInit is
// CollectLaunchInstanceParamsFromCloudInit's testable core, once the
// cloud-init YAML is read and an AMI is resolved -- AMI selection runs a
// real bubbletea Program (tui.RunPicker, DESIGN.md's full conversion
// punch list) that can't be driven by a test's pipe input, same
// limitation as every other Picker-tier conversion this session.
// menuInput/menuOutput are nil in production (the instance-type
// huh.Select and its ENA/AZ incompatibility-remediation huh.Selects run
// interactively on the real terminal) and are supplied by tests to drive
// them through their accessible-mode pipe path instead. All three share
// one reader/writer pair, read in sequence one line at a time.
func collectLaunchInstanceParamsFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, userData string, image inventory.Image, menuInput io.Reader, menuOutput io.Writer) (LaunchInstanceParams, awsclient.EC2API, awsclient.SSMAPI, error) {
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, image.Region)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	name, err := ui.Prompt("Name tag", ui.WithValidator(requireNonEmpty), ui.WithIO(menuInput, menuOutput))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err := promptInstanceType(w, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err = ensureInstanceTypeENACompatible(ctx, w, ec2Client, instanceType, image.EnaSupport, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	keyName, err := promptKeyPairNameOrCreate(ctx, w, ec2Client, sshKeyDir(), menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	securityGroupIDs, err := promptSecurityGroupIDs(ctx, w, ec2Client, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	subnet, err := promptSubnetID(ctx, w, ec2Client, instanceType, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, subnet, err = ensureInstanceTypeSupportedInSubnet(ctx, w, ec2Client, instanceType, subnet, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	iamProfile, err := promptIAMInstanceProfileOrCreate(ctx, w, iamClient, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	var projectOpts []ui.PromptOption
	if image.Project != "" {
		projectOpts = append(projectOpts, ui.WithDefault(image.Project))
	}
	projectOpts = append(projectOpts, ui.WithIO(menuInput, menuOutput))
	project, err := ui.Prompt("Project tag", projectOpts...)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	environment, err := ui.Prompt("Environment tag (production, development, or test)", ui.WithValidator(validateEnvironment), ui.WithIO(menuInput, menuOutput))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	return LaunchInstanceParams{
		ImageID:            image.ImageID,
		InstanceType:       instanceType,
		KeyName:            keyName,
		SecurityGroupIDs:   securityGroupIDs,
		SubnetID:           subnet.SubnetID,
		IAMInstanceProfile: iamProfile,
		UserData:           userData,
		Tags: map[string]string{
			"Name":        name,
			"Project":     project,
			"Environment": environment,
		},
	}, ec2Client, ssmClient, nil
}

// requireNonEmpty is a generic ui.WithValidator for any required
// free-text prompt (Key pair name, Subnet ID, Name tag, cloud-init YAML,
// backup directory, S3 bucket, tag key, ...). The message must stay
// field-agnostic -- it was originally written just for the cloud-init
// YAML prompt with a field-specific message and later reused verbatim
// across unrelated prompts, which showed a confusing "cloud-init YAML is
// required" error while validating e.g. a blank Subnet ID.
func requireNonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required and cannot be blank")
	}
	return nil
}
