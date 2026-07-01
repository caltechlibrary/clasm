package workflow

import (
	"strings"
	"testing"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCollectLaunchInstanceParams(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1", CreationDate: "2026-01-15"},
		{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", CreationDate: "2026-02-01", Project: "caltechauthors"},
	}

	input := "2\n" + // pick ami-2
		"t3.large\n" + // instance type
		"my-keypair\n" + // key pair
		"sg-1, sg-2\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile (blank)
		"#cloud-config\n" + // user data (inline)
		"authorstest\n" + // Name tag
		"\n" + // Project tag (blank -> default from ami-2)
		"test\n" // Environment tag

	term, le, _ := newPipeEditor(t, input)

	got, err := CollectLaunchInstanceParams(term, le, images)
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

func TestCollectLaunchInstanceParams_RejectsInvalidEnvironment(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}

	input := "1\n" + // pick ami-1
		"t3.micro\n" + // instance type
		"my-keypair\n" + // key pair
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile
		"\n" + // user data
		"web\n" + // Name tag
		"caltechdata\n" + // Project tag
		"prod\n" + // Environment tag (invalid)
		"production\n" // Environment tag (retry, valid)

	term, le, buf := newPipeEditor(t, input)

	got, err := CollectLaunchInstanceParams(term, le, images)
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
