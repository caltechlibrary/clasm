package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestLaunchFromTemplate_UsesLaunchTemplateSpecification(t *testing.T) {
	fake := &fakeEC2Client{runInstancesID: "i-1"}
	id, err := launchFromTemplate(context.Background(), fake, "lt-1", "$Default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-1" {
		t.Errorf("got %q, want i-1", id)
	}
	in := fake.lastRunInstancesInput
	if in.LaunchTemplate == nil {
		t.Fatal("expected RunInstances to be called with a LaunchTemplate spec")
	}
	if aws.ToString(in.LaunchTemplate.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplate.LaunchTemplateId))
	}
	if aws.ToString(in.LaunchTemplate.Version) != "$Default" {
		t.Errorf("Version = %q, want $Default", aws.ToString(in.LaunchTemplate.Version))
	}
	if in.ImageId != nil || len(in.SecurityGroupIds) != 0 || in.SubnetId != nil {
		t.Error("expected no other launch parameters to be set -- the template supplies them")
	}
}

func TestCreateInstanceFromLaunchTemplate_HappyPathUsesDefaultVersion(t *testing.T) {
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}
	fake := &fakeEC2Client{runInstancesID: "i-1", runningAfterCall: 1, publicIP: "1.2.3.4"}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}

	var buf bytes.Buffer
	input := "\n" + // accept pre-filled $Default
		"y\n" // confirm launch
	err := createInstanceFromLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(fake.lastRunInstancesInput.LaunchTemplate.Version) != "$Default" {
		t.Errorf("Version = %q, want $Default", aws.ToString(fake.lastRunInstancesInput.LaunchTemplate.Version))
	}
	if !strings.Contains(buf.String(), "1.2.3.4") {
		t.Errorf("expected connection info in output, got:\n%s", buf.String())
	}
}

func TestCreateInstanceFromLaunchTemplate_EditedVersionIsPassedThrough(t *testing.T) {
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	fake := &fakeEC2Client{runInstancesID: "i-1", runningAfterCall: 1}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}

	var buf bytes.Buffer
	input := "3\n" + // override to an explicit version number
		"y\n"
	err := createInstanceFromLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(fake.lastRunInstancesInput.LaunchTemplate.Version) != "3" {
		t.Errorf("Version = %q, want 3", aws.ToString(fake.lastRunInstancesInput.LaunchTemplate.Version))
	}
}

func TestCreateInstanceFromLaunchTemplate_DeclinedConfirmationDoesNotLaunch(t *testing.T) {
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}

	var buf bytes.Buffer
	input := "\n" + "n\n"
	err := createInstanceFromLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}

func TestCreateInstanceFromLaunchTemplate_UnknownRegionErrors(t *testing.T) {
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	clients := map[string]awsclient.EC2API{}
	var buf bytes.Buffer
	err := createInstanceFromLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput("\n"), &buf)
	if err == nil {
		t.Fatal("expected an error for a region with no configured client")
	}
}
