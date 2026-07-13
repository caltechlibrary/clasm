package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// errNoKeyPairsConfigured forces promptKeyPairNameOrCreate's free-text
// fallback in tests below -- the key pair (+ create-new) picker
// converted to tui.RunPicker (DESIGN.md's full conversion punch list,
// Picker tier) is a real bubbletea Program that can't be pipe-tested,
// and a bare &fakeEC2Client{} succeeds with an empty (but non-error) key
// pair list, which still reaches the picker.
var errNoKeyPairsConfigured = errors.New("no key pairs configured for this test")

// The curated-instance-type picker converted to huh.Select (DESIGN.md's
// full conversion punch list): its selection is fed via a separate
// newHuhAccessibleInput reader (menuInput), not le, which still feeds
// every other prompt in this function. The AMI picker also converted to
// tui.RunPicker (Picker tier) -- a real bubbletea Program that can't be
// pipe-tested -- so collectLaunchInstanceParams now takes an
// already-resolved image directly instead of the full images list;
// CollectLaunchInstanceParams's own AMI-selection step is covered only
// by manual/interactive verification, the same accepted limitation this
// session's other Picker-tier conversions already have.

func TestCollectLaunchInstanceParams(t *testing.T) {
	image := inventory.Image{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", CreationDate: "2026-02-01", Project: "caltechauthors"}

	input := "authorstest\n" + // Name tag
		"4\n" + // instance type: t3.large
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-keypair\n" + // New key pair name
		"sg-1, sg-2\n" + // security groups (no groups fetched -> free-text fallback)
		"subnet-abc\n" + // subnet (no subnets fetched -> free-text fallback)
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"#cloud-config\n" + // user data (inline)
		"\n" + // Project tag (blank -> default from ami-2)
		"test\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	fake := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ImageID != "ami-2" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-2")
	}
	if got.InstanceType != "t3.large" {
		t.Errorf("InstanceType = %q, want %q", got.InstanceType, "t3.large")
	}
	if got.KeyName != "my-keypair" {
		t.Errorf("KeyName = %q, want %q", got.KeyName, "my-keypair")
	}
	wantSGs := []string{"sg-1", "sg-2"}
	if len(got.SecurityGroupIDs) != 2 || got.SecurityGroupIDs[0] != wantSGs[0] || got.SecurityGroupIDs[1] != wantSGs[1] {
		t.Errorf("SecurityGroupIDs = %v, want %v", got.SecurityGroupIDs, wantSGs)
	}
	if got.SubnetID != "subnet-abc" {
		t.Errorf("SubnetID = %q, want %q", got.SubnetID, "subnet-abc")
	}
	if got.IAMInstanceProfile != "" {
		t.Errorf("IAMInstanceProfile = %q, want empty", got.IAMInstanceProfile)
	}
	if got.UserData != "#cloud-config" {
		t.Errorf("UserData = %q, want %q", got.UserData, "#cloud-config")
	}
	if got.Tags["Name"] != "authorstest" {
		t.Errorf("Tags[Name] = %q, want %q", got.Tags["Name"], "authorstest")
	}
	if got.Tags["Project"] != "caltechauthors" {
		t.Errorf("Tags[Project] = %q, want default %q", got.Tags["Project"], "caltechauthors")
	}
	if got.Tags["Environment"] != "test" {
		t.Errorf("Tags[Environment] = %q, want %q", got.Tags["Environment"], "test")
	}
}

func TestCollectLaunchInstanceParams_NamePromptedRightAfterAMIPick(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}
	input := "web\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-1\n" + // subnet
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Name"] != "web" {
		t.Errorf("Tags[Name] = %q, want %q", got.Tags["Name"], "web")
	}

	out := buf.String()
	nameIdx := strings.Index(out, "Name tag")
	instanceTypeIdx := strings.Index(out, "Select an instance type")
	if nameIdx < 0 || instanceTypeIdx < 0 || nameIdx > instanceTypeIdx {
		t.Errorf("expected the Name tag prompt to precede the Instance type prompt in output:\n%s", out)
	}
}

func TestCollectLaunchInstanceParams_PicksSecurityGroupsFromList(t *testing.T) {
	// Subnet selection converted to tui.RunPicker (DESIGN.md's full
	// conversion punch list, Picker tier) -- a real bubbletea Program
	// that can't be pipe-tested, so this fake configures zero subnets,
	// taking promptSubnetID's free-text fallback path instead (see
	// launch_prompts_test.go's own note on the subnet list-picker's
	// retired tests).
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}
	fake := &fakeEC2Client{
		securityGroups: []types.SecurityGroup{
			{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
			{GroupId: aws.String("sg-2"), GroupName: aws.String("db")},
		},
		describeKeyPairsErr: errNoKeyPairsConfigured,
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	input := "web\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"1,2\n" + // pick both security groups by number
		"subnet-1\n" + // subnet (free-text fallback -- no subnets configured)
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.KeyName != "my-key" {
		t.Errorf("KeyName = %q, want %q", got.KeyName, "my-key")
	}
	if len(got.SecurityGroupIDs) != 2 || got.SecurityGroupIDs[0] != "sg-1" || got.SecurityGroupIDs[1] != "sg-2" {
		t.Errorf("SecurityGroupIDs = %v, want [sg-1 sg-2]", got.SecurityGroupIDs)
	}
	if got.SubnetID != "subnet-1" {
		t.Errorf("SubnetID = %q, want %q", got.SubnetID, "subnet-1")
	}
}

func TestCollectLaunchInstanceParams_RejectsInvalidEnvironment(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}

	input := "web\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-keypair\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"prod\n" + // Environment tag (invalid)
		"production\n" // Environment tag (retry, valid)

	term, menuInput, buf := newPipeEditor(input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Environment"] != "production" {
		t.Errorf("Tags[Environment] = %q, want %q", got.Tags["Environment"], "production")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message in output, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParams_RejectsBlankRequiredFields(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}

	input := "\n" + // Name tag (blank -- rejected)
		"web\n" + // Name tag (retry, valid)
		"1\n" + // instance type: t3.micro
		"\n" + // Key pair name (blank -- invalid, rejected; free-text fallback)
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-keypair\n" + // New key pair name
		"\n" + // Security groups (blank -- rejected)
		"sg-1\n" + // Security groups (retry, valid)
		"\n" + // Subnet ID (blank -- rejected)
		"subnet-abc\n" + // Subnet ID (retry, valid)
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // user data (optional, blank is fine)
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Name"] != "web" || got.KeyName != "my-keypair" || len(got.SecurityGroupIDs) != 1 || got.SubnetID != "subnet-abc" {
		t.Errorf("got %+v, want all required fields filled after retry", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected validation error messages in output, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParams_OfficialUbuntuAMIIsSelectableFromThePickList(t *testing.T) {
	// AMI selection (including imagesWithOfficialUbuntu's appended
	// official Ubuntu entries) now happens in the exported
	// CollectLaunchInstanceParams, via a real bubbletea Program
	// (tui.RunPicker) that can't be pipe-tested -- see
	// TestImagesWithOfficialUbuntu_AppendsToOwnedImages (official_ubuntu_
	// amis_test.go) for that expansion's own coverage. This test resolves
	// the same expanded list directly and picks the appended entry,
	// verifying collectLaunchInstanceParams' core correctly carries an
	// official Ubuntu AMI's fields through once resolved.
	images := []inventory.Image{{ImageID: "ami-owned", Region: "us-east-1"}}
	fake := &fakeEC2Client{
		officialUbuntuImages: map[string][]types.Image{
			nobleNamePattern: {{ImageId: aws.String("ami-noble"), CreationDate: aws.String("2026-06-01T00:00:00.000Z"), EnaSupport: aws.Bool(true)}},
		},
		describeKeyPairsErr: errNoKeyPairsConfigured,
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	expanded := imagesWithOfficialUbuntu(context.Background(), ec2Clients, images)
	image := expanded[len(expanded)-1] // the appended official Ubuntu AMI

	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" +
		"caltechdata\n" +
		"test\n"

	term, menuInput, buf := newPipeEditor(input)
	got, _, _, err := collectLaunchInstanceParams(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ImageID != "ami-noble" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-noble")
	}
}
