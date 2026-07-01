package workflow

import (
	"errors"
	"strings"

	"github.com/rsdoiel/termlib"

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
// primitive").
func CollectLaunchInstanceParamsFromCloudInit(t *termlib.Terminal, le *termlib.LineEditor, images []inventory.Image) (LaunchInstanceParams, error) {
	userDataInput, err := ui.Prompt(t, le, "Cloud-init YAML (inline text or @file path)", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return LaunchInstanceParams{}, err
	}
	userData, err := loadUserData(userDataInput)
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	image, err := ui.PickList(t, le, images, imageLabel, "Select a base AMI")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	instanceType, err := ui.Prompt(t, le, "Instance type", ui.WithDefault("t3.micro"))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	keyName, err := ui.Prompt(t, le, "Key pair name")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	securityGroupsRaw, err := ui.Prompt(t, le, "Security group IDs (comma-separated)")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	subnetID, err := ui.Prompt(t, le, "Subnet ID")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	iamProfile, err := ui.Prompt(t, le, "IAM instance profile (optional)")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	name, err := ui.Prompt(t, le, "Name tag")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	var projectOpts []ui.PromptOption
	if image.Project != "" {
		projectOpts = append(projectOpts, ui.WithDefault(image.Project))
	}
	project, err := ui.Prompt(t, le, "Project tag", projectOpts...)
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	environment, err := ui.Prompt(t, le, "Environment tag (production, development, or test)", ui.WithValidator(validateEnvironment))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	return LaunchInstanceParams{
		ImageID:            image.ImageID,
		InstanceType:       instanceType,
		KeyName:            keyName,
		SecurityGroupIDs:   splitCSV(securityGroupsRaw),
		SubnetID:           subnetID,
		IAMInstanceProfile: iamProfile,
		UserData:           userData,
		Tags: map[string]string{
			"Name":        name,
			"Project":     project,
			"Environment": environment,
		},
	}, nil
}

func requireNonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("cloud-init YAML is required for this workflow")
	}
	return nil
}
