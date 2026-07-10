package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
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
// Project/Environment tagging convention"). Name tag is prompted right
// after the AMI pick, before any technical configuration, per user
// feedback during real-AWS testing. Security group IDs and subnet ID
// are each offered as a pick list fetched from the AMI's region
// (DESIGN.md, Feature 2: "list available security groups"/"subnets");
// key pair name stays a free-text prompt -- unlike opaque sg-xxxx/
// subnet-xxxx IDs, key pair names are already human-readable, and a
// flat list of every key pair in the account added noise without
// helping -- but it also offers "new" to create a fresh key pair on
// the spot (promptKeyPairNameOrCreate; see DECISIONS.md, "Support
// creating a new key pair from within awsops") for operators who don't
// want to reuse keys across instances. Resolving the region-specific
// client here, right after the
// AMI is picked, is why this takes ctx and the per-region client maps
// and returns the resolved clients alongside params, instead of just
// the AMI's picked Region.
func CollectLaunchInstanceParams(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) (LaunchInstanceParams, awsclient.EC2API, awsclient.SSMAPI, error) {
	image, err := pickImage(ctx, "Select an AMI", imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}
	return collectLaunchInstanceParams(ctx, t, le, ec2Clients, ssmClients, iamClient, image, nil, nil)
}

// collectLaunchInstanceParams is CollectLaunchInstanceParams's testable
// core, once an AMI is resolved -- AMI selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as every other
// Picker-tier conversion this session. menuInput/menuOutput are nil in
// production (the instance-type huh.Select and its ENA/AZ
// incompatibility-remediation huh.Selects run interactively on the real
// terminal) and are supplied by tests to drive them through their
// accessible-mode pipe path instead, separate from le, which still feeds
// every other prompt in this function. All three share one reader/
// writer pair, read in sequence one line at a time.
func collectLaunchInstanceParams(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, image inventory.Image, menuInput io.Reader, menuOutput io.Writer) (LaunchInstanceParams, awsclient.EC2API, awsclient.SSMAPI, error) {
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, image.Region)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	name, err := ui.Prompt(t, le, "Name tag", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err := promptInstanceType(t, le, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	instanceType, err = ensureInstanceTypeENACompatible(ctx, t, le, ec2Client, instanceType, image.EnaSupport, menuInput, menuOutput)
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

	instanceType, subnet, err = ensureInstanceTypeSupportedInSubnet(ctx, t, le, ec2Client, instanceType, subnet, menuInput, menuOutput)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	iamProfile, err := promptIAMInstanceProfileOrCreate(ctx, t, le, iamClient)
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}

	userDataInput, err := ui.Prompt(t, le, "User data (inline text, @file path, or blank)")
	if err != nil {
		return LaunchInstanceParams{}, nil, nil, err
	}
	userData, err := loadUserData(t, userDataInput)
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

func imageLabel(img inventory.Image) string {
	return fmt.Sprintf("%s - %s (%s) - %s", img.ImageID, img.Name, img.Region, img.CreationDate)
}

// pickImage runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over images and returns the chosen one. Like
// pickInstance (power_state.go) and pickBucket (Phase 20.4), this drives
// a real bubbletea Program that can't be pipe-tested -- every caller
// splits into a thin entry point (calls pickImage) and a testable core
// taking the already-resolved image directly.
func pickImage(ctx context.Context, title string, images []inventory.Image) (inventory.Image, error) {
	rows := make([]string, len(images))
	for i, img := range images {
		rows[i] = imageLabel(img)
	}

	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.Image{}, err
	}
	return images[idx], nil
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
