package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestCollectLaunchInstanceParamsFromCloudInit_PromptsCloudInitBeforeAMI(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1"},
		{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", Project: "caltechauthors"},
	}

	input := path + "\n" + // cloud-init YAML file path, first
		"2\n" + // pick ami-2, second
		"newauthors\n" + // Name tag
		"4\n" + // instance type: t3.large
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-keypair\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"1\n" + // IAM profile: select (none)
		"\n" + // Project tag (blank -> default from ami-2)
		"development\n" // Environment tag

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
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

	out := buf.String()
	cloudInitIdx := strings.Index(out, "Cloud-init")
	amiIdx := strings.Index(out, "Select a base AMI")
	if cloudInitIdx < 0 || amiIdx < 0 || cloudInitIdx > amiIdx {
		t.Errorf("expected the cloud-init prompt to precede the AMI pick list in output:\n%s", out)
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_RequiresNonEmptyCloudInit(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "\n" + // blank -- rejected
		path + "\n" + // retry, accepted
		"1\n" + // pick ami-1
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile: select (none)
		"caltechdata\n" +
		"test\n"

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserData != "#cloud-config" {
		t.Errorf("UserData = %q, want %q", got.UserData, "#cloud-config")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the blank cloud-init input, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_ReadsFromFile(t *testing.T) {
	want := "#cloud-config\npackages: [docker]\n"
	path := writeCloudInitFixture(t, want)

	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := path + "\n" +
		"1\n" +
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile: select (none)
		"caltechdata\n" +
		"test\n"

	term, le, _ := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserData != want {
		t.Errorf("UserData = %q, want %q", got.UserData, want)
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_ToleratesLeadingAtSign(t *testing.T) {
	// Backward-compat: an operator used to Feature 2's "@file path"
	// convention shouldn't be broken by typing "@" out of habit here,
	// even though this prompt no longer requires (or supports inline
	// text as an alternative to) it.
	want := "#cloud-config\n"
	path := writeCloudInitFixture(t, want)

	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "@" + path + "\n" +
		"1\n" +
		"web\n" +
		"1\n" +
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" +
		"caltechdata\n" +
		"test\n"

	term, le, _ := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserData != want {
		t.Errorf("UserData = %q, want %q", got.UserData, want)
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_RetriesOnUnreadableFile(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "/no/such/file-really-does-not-exist.yaml\n" + // rejected -- cannot read
		path + "\n" + // retry, accepted
		"1\n" +
		"web\n" +
		"1\n" +
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" +
		"caltechdata\n" +
		"test\n"

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserData != "#cloud-config" {
		t.Errorf("UserData = %q, want %q", got.UserData, "#cloud-config")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the unreadable file, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParamsFromCloudInit_OfficialUbuntuAMIIsSelectableFromThePickList(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-owned", Region: "us-east-1"}}
	fake := &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
		nobleNamePattern: {{ImageId: aws.String("ami-noble"), CreationDate: aws.String("2026-06-01T00:00:00.000Z"), EnaSupport: aws.Bool(true)}},
	}}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	input := path + "\n" +
		"2\n" + // owned AMI (1), then the appended official Ubuntu AMI (2)
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" +
		"caltechdata\n" +
		"test\n"

	term, le, buf := newPipeEditor(t, input)
	got, _, _, err := CollectLaunchInstanceParamsFromCloudInit(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ImageID != "ami-noble" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-noble")
	}
	if !strings.Contains(buf.String(), "Ubuntu 24.04 LTS") {
		t.Errorf("expected the official Ubuntu AMI to appear in the pick list, got:\n%s", buf.String())
	}
}
