package workflow

import (
	"fmt"
	"strings"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// LaunchInstanceParams is the resolved, typed parameter set for
// launching an EC2 instance, whether collected interactively (v1) or,
// eventually, from a Recorded Script (see DECISIONS.md, "Structure
// workflows for future record/replay"). Building this struct is kept
// separate from executing it (Launch) so that seam can be reused without
// reopening this code.
type LaunchInstanceParams struct {
	ImageID            string
	InstanceType       string
	KeyName            string
	SecurityGroupIDs   []string
	SubnetID           string
	IAMInstanceProfile string
	UserData           string
	Tags               map[string]string
}

// CollectLaunchInstanceParams interactively collects a LaunchInstanceParams
// by picking an AMI (Phase 3's PickList) and prompting for the remaining
// launch parameters -- mirrors ec2_ami_manager.bash's
// collect_instance_params(). Project defaults to the source AMI's
// Project tag if set; Environment is always an explicit, validated
// prompt with no default (see DECISIONS.md, "Introduce a light
// Project/Environment tagging convention").
func CollectLaunchInstanceParams(t *termlib.Terminal, le *termlib.LineEditor, images []inventory.Image) (LaunchInstanceParams, error) {
	image, err := ui.PickList(t, le, images, imageLabel, "Select an AMI")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	instanceType, err := ui.Prompt(t, le, "Instance type", ui.WithDefault("t3.micro"))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	keyName, err := ui.Prompt(t, le, "Key pair name", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	securityGroupsRaw, err := ui.Prompt(t, le, "Security group IDs (comma-separated)", ui.WithValidator(requireAtLeastOneSecurityGroup))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	subnetID, err := ui.Prompt(t, le, "Subnet ID", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	iamProfile, err := ui.Prompt(t, le, "IAM instance profile (optional)")
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	userDataInput, err := ui.Prompt(t, le, "User data (inline text, @file path, or blank)")
	if err != nil {
		return LaunchInstanceParams{}, err
	}
	userData, err := loadUserData(userDataInput)
	if err != nil {
		return LaunchInstanceParams{}, err
	}

	name, err := ui.Prompt(t, le, "Name tag", ui.WithValidator(requireNonEmpty))
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

func imageLabel(img inventory.Image) string {
	return fmt.Sprintf("%s - %s (%s) - %s", img.ImageID, img.Name, img.Region, img.CreationDate)
}

func splitCSV(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func requireAtLeastOneSecurityGroup(s string) error {
	if len(splitCSV(s)) == 0 {
		return fmt.Errorf("at least one security group ID is required")
	}
	return nil
}

// validateEnvironment enforces the Project/Environment tagging
// convention's fixed Environment vocabulary (see DECISIONS.md,
// "Introduce a light Project/Environment tagging convention").
func validateEnvironment(s string) error {
	switch s {
	case "production", "development", "test":
		return nil
	default:
		return fmt.Errorf("must be one of production, development, test")
	}
}
