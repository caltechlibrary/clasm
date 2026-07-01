package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCollectLaunchInstanceParamsFromCloudInit_PromptsCloudInitBeforeAMI(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1"},
		{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", Project: "caltechauthors"},
	}

	input := "#cloud-config\n" + // cloud-init YAML (inline, single-line), first
		"2\n" + // pick ami-2, second
		"t3.large\n" + // instance type
		"my-keypair\n" + // key pair
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"\n" + // IAM profile (blank)
		"newauthors\n" + // Name tag
		"\n" + // Project tag (blank -> default from ami-2)
		"development\n" // Environment tag

	term, le, buf := newPipeEditor(t, input)

	got, err := CollectLaunchInstanceParamsFromCloudInit(term, le, images)
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
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "\n" + // blank -- rejected
		"#cloud-config\n" + // retry, accepted
		"1\n" + // pick ami-1
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"web\n" +
		"caltechdata\n" +
		"test\n"

	term, le, buf := newPipeEditor(t, input)

	got, err := CollectLaunchInstanceParamsFromCloudInit(term, le, images)
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

func TestCollectLaunchInstanceParamsFromCloudInit_LoadsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invenio-rdm.yaml")
	want := "#cloud-config\npackages: [docker]\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}

	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "@" + path + "\n" +
		"1\n" +
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"web\n" +
		"caltechdata\n" +
		"test\n"

	term, le, _ := newPipeEditor(t, input)
	got, err := CollectLaunchInstanceParamsFromCloudInit(term, le, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserData != want {
		t.Errorf("UserData = %q, want %q", got.UserData, want)
	}
}
