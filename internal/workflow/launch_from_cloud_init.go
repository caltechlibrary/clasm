package workflow

import (
	"context"
	"errors"
	"strings"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
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
func CollectLaunchInstanceParamsFromCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) (LaunchInstanceParams, awsclient.EC2API, awsclient.SSMAPI, error) {
	userData, err := promptCloudInitYAMLFile(t, le)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	image, err := ui.PickList(t, le, imagesWithOfficialUbuntu(ctx, ec2Clients, images), imageLabel, "Select a base AMI")
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, image.Region)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	name, err := ui.Prompt(t, le, "Name tag", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err := promptInstanceType(t, le)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err = ensureInstanceTypeENACompatible(ctx, t, le, ec2Client, instanceType, image.EnaSupport)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	keyName, err := promptKeyPairNameOrCreate(ctx, t, le, ec2Client, sshKeyDir())
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	securityGroupIDs, err := promptSecurityGroupIDs(ctx, t, le, ec2Client)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	subnet, err := promptSubnetID(ctx, t, le, ec2Client, instanceType)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, subnet, err = ensureInstanceTypeSupportedInSubnet(ctx, t, le, ec2Client, instanceType, subnet)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	iamProfile, err := promptIAMInstanceProfileOrCreate(ctx, t, le, iamClient)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	var projectOpts []ui.PromptOption
	if image.Project != "" {
		projectOpts = append(projectOpts, ui.WithDefault(image.Project))
	}
	project, err := ui.Prompt(t, le, "Project tag", projectOpts...)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	environment, err := ui.Prompt(t, le, "Environment tag (production, development, or test)", ui.WithValidator(validateEnvironment))
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
