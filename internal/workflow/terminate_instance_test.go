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

func TestTerminateInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	if err := TerminateInstance(context.Background(), fake, "i-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput == nil || fake.lastTerminateInstancesInput.InstanceIds[0] != "i-1" {
		t.Errorf("TerminateInstances called with %+v, want InstanceIds=[i-1]", fake.lastTerminateInstancesInput)
	}
}

func TestTerminateInstance_Failure(t *testing.T) {
	fake := &fakeEC2Client{terminateInstancesErr: errors.New("boom")}
	if err := TerminateInstance(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error")
	}
}

// Instance selection (DESIGN.md's full conversion punch list, Picker
// tier) now runs a real bubbletea Program (tui.RunPicker), which can't
// be driven by a test's pipe input -- see internal/tui/picker_test.go
// for that component's own thorough test suite. Tests below exercise
// everything once an instance is already resolved via the unexported
// terminateEC2Instance; TerminateEC2Instance's own picker-selection step
// is covered only by manual/interactive verification, the same accepted
// limitation power_state.go's startEC2Instance/stopEC2Instance already
// have.

func TestTerminateEC2Instance_DeleteOnTerminationWarningShown(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}
	fake := &fakeEC2Client{
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsInstanceBlockDevice{DeleteOnTermination: aws.Bool(true), VolumeId: aws.String("vol-1")}},
		},
	}
	term, le, buf := newPipeEditor(t, "i-1\n") // type-to-confirm with the instance ID
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput == nil {
		t.Fatal("TerminateInstances was never called")
	}
	if !strings.Contains(buf.String(), "DeleteOnTermination") {
		t.Errorf("expected a DeleteOnTermination warning in output, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "/dev/sda1") {
		t.Errorf("expected the flagged device name in output, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_DeleteOnTerminationWarningAbsentWhenFalse(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}
	fake := &fakeEC2Client{
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsInstanceBlockDevice{DeleteOnTermination: aws.Bool(false), VolumeId: aws.String("vol-1")}},
		},
	}
	term, le, buf := newPipeEditor(t, "i-1\n")
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "DeleteOnTermination") {
		t.Errorf("expected no DeleteOnTermination warning when no volume has it set, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_ProductionWarningShown(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Environment: "production", Region: "us-east-1"}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "i-1\n")
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected a production warning in output, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_ProductionWarningAbsentOtherwise(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Environment: "development", Region: "us-east-1"}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "i-1\n")
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected no production warning for a development instance, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_TypeToConfirmAcceptsName(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "web\n") // type the Name, not the ID
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput == nil {
		t.Fatal("TerminateInstances was never called despite matching the Name")
	}
}

func TestTerminateEC2Instance_TypeToConfirmMismatchCancels(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "wrong\n")
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput != nil {
		t.Error("TerminateInstances was called despite a type-to-confirm mismatch")
	}
}

func TestTerminateEC2Instance_NoInstances(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_PropagatesTerminateError(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}
	fake := &fakeEC2Client{terminateInstancesErr: errors.New("boom")}
	term, le, _ := newPipeEditor(t, "i-1\n")
	err := terminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, inst)
	if err == nil {
		t.Fatal("expected an error")
	}
}
