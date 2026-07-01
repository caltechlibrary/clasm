package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestDeregisterAMI_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	if err := DeregisterAMI(context.Background(), fake, "ami-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeregisterImageInput == nil {
		t.Fatal("DeregisterImage was never called")
	}
}

func TestDeregisterAMI_Failure(t *testing.T) {
	fake := &fakeEC2Client{deregisterImageErr: errors.New("boom")}
	if err := DeregisterAMI(context.Background(), fake, "ami-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRemoveAMI_DryRunDisplay(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeregisterImageInput == nil {
		t.Fatal("DeregisterImage was never called")
	}
	if !strings.Contains(buf.String(), "DRY RUN") || !strings.Contains(buf.String(), "ami-1") {
		t.Errorf("expected a dry-run display mentioning ami-1, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_DependencyWarningShownWhenInUse(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	instances := []inventory.Instance{
		{InstanceID: "i-1", Name: "web", ImageID: "ami-1"},
		{InstanceID: "i-2", Name: "other", ImageID: "ami-2"},
	}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "i-1") {
		t.Errorf("expected the dependent instance i-1 in output, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "i-2") {
		t.Errorf("did not expect the unrelated instance i-2 in output, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_NoDependencyWarningWhenUnused(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", ImageID: "ami-2"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "currently reference") {
		t.Errorf("did not expect a dependency warning when the AMI is unused, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_ProductionWarningShown(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Environment: "production", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected a production warning in output, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_ProductionWarningAbsentOtherwise(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Environment: "development", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected no production warning for a development AMI, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_TypeToConfirmAcceptsName(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "1\nbase-ubuntu\n") // type the Name, not the ID

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeregisterImageInput == nil {
		t.Fatal("DeregisterImage was never called despite matching the Name")
	}
}

func TestRemoveAMI_TypeToConfirmMismatchCancels(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "1\nwrong\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeregisterImageInput != nil {
		t.Error("DeregisterImage was called despite a type-to-confirm mismatch")
	}
}

func TestRemoveAMI_CancelledPickList(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "0\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastDeregisterImageInput != nil {
		t.Error("DeregisterImage was called despite cancelling the pick list")
	}
}

func TestRemoveAMI_NoAMIs(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}

func TestRemoveAMI_PropagatesRemoveError(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	fake := &fakeEC2Client{deregisterImageErr: errors.New("boom")}
	term, le, _ := newPipeEditor(t, "1\nami-1\n")

	err := RemoveAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, images, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
}
