package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func writeCloudInitFixture(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cloud-init.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}
	return path
}

// The curated-instance-type picker (huh.Select) and every free-text
// prompt in this function now share one accessible-mode reader
// (menuInput), read in sequence one line at a time, in the exact order
// collectLaunchInstanceParamsFromCloudInit's own flow reads them. The
// cloud-init-YAML-file prompt and the AMI picker (also converted to
// tui.RunPicker, Picker tier -- a real bubbletea Program that can't be
// pipe-tested) both now run in the exported
// CollectLaunchInstanceParamsFromCloudInit, before this testable core --
// see userdata_test.go's promptCloudInitYAMLFile tests for that
// prompt's own coverage (blank rejection, retry-on-unreadable-file, "@"
// prefix tolerance), migrated there from this file.
// CollectLaunchInstanceParamsFromCloudInit's own prompt-ordering and
// AMI-selection behavior is covered only by manual/interactive
// verification, the same accepted limitation this session's other
// Picker-tier conversions already have.

func TestCollectLaunchInstanceParamsFromCloudInit_HappyPath(t *testing.T) {
	image := inventory.Image{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", Project: "caltechauthors"}

	input := "newauthors\n" + // Name tag
		"4\n" + // instance type: t3.large
		"\n" + // Root EBS volume size in GB (blank -> AMI default of 0 in this fake)
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-keypair\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // Project tag (blank -> default from ami-2)
		"development\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := collectLaunchInstanceParamsFromCloudInit(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), "#cloud-config", image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.UserData != "#cloud-config" {
		t.Errorf("UserData = %q, want %q", got.UserData, "#cloud-config")
	}
	if got.ImageID != "ami-2" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-2")
	}
	if got.InstanceType != "t3.large" {
		t.Errorf("InstanceType = %q, want %q", got.InstanceType, "t3.large")
	}
	if got.Tags["Project"] != "caltechauthors" {
		t.Errorf("Tags[Project] = %q, want default %q", got.Tags["Project"], "caltechauthors")
	}
	if got.Tags["Environment"] != "development" {
		t.Errorf("Tags[Environment] = %q, want %q", got.Tags["Environment"], "development")
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_SetsRootVolumeSize(t *testing.T) {
	// End-to-end coverage for the TODO.md bug fix, cloud-init flow --
	// this is also the flow Create Launch Template from Cloud-Init YAML
	// reuses directly (launch_template_create.go), so this covers all
	// three of DECISIONS.md's "every instance-creation flow and template
	// creation" paths at once.
	image := inventory.Image{ImageID: "ami-rdm", Region: "us-east-1", Project: "caltechauthors"}
	fake := &fakeEC2Client{
		describeKeyPairsErr:          errNoKeyPairsConfigured,
		describeImagesRootDeviceName: "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}},
		},
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	input := "rdm-compare\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"500\n" + // Root EBS volume size in GB (explicit override, e.g. an RDM comparison instance)
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-keypair\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // Project tag (blank -> default from image)
		"production\n" // Environment tag

	term, menuInput, buf := newPipeEditor(input)
	got, _, _, err := collectLaunchInstanceParamsFromCloudInit(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), "#cloud-config", image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RootVolumeSizeGB != 500 {
		t.Errorf("RootVolumeSizeGB = %d, want 500", got.RootVolumeSizeGB)
	}
	if got.RootDeviceName != "/dev/xvda" {
		t.Errorf("RootDeviceName = %q, want %q", got.RootDeviceName, "/dev/xvda")
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_OfficialUbuntuAMIIsSelectableFromThePickList(t *testing.T) {
	// AMI selection (including imagesWithOfficialUbuntu's appended
	// official Ubuntu entries) now happens in the exported
	// CollectLaunchInstanceParamsFromCloudInit, via a real bubbletea
	// Program (tui.RunPicker) that can't be pipe-tested -- see
	// TestImagesWithOfficialUbuntu_AppendsToOwnedImages (official_ubuntu_
	// amis_test.go) for that expansion's own coverage. This test resolves
	// the same expanded list directly and picks the appended entry,
	// verifying collectLaunchInstanceParamsFromCloudInit's core correctly
	// carries an official Ubuntu AMI's fields through once resolved.
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
		"\n" + // Root EBS volume size in GB (blank -> AMI default of 0 in this fake)
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile (free-text fallback via fakeIAMClientNoProfiles; value unchecked)
		"caltechdata\n" +
		"test\n"

	term, menuInput, buf := newPipeEditor(input)
	got, _, _, err := collectLaunchInstanceParamsFromCloudInit(context.Background(), term, ec2Clients, ssmClients, fakeIAMClientNoProfiles(), "#cloud-config", image, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ImageID != "ami-noble" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-noble")
	}
}
