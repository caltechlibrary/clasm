package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
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

func TestTerminateEC2Instance_DeleteOnTerminationWarningShown(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsInstanceBlockDevice{DeleteOnTermination: aws.Bool(true), VolumeId: aws.String("vol-1")}},
		},
	}
	term, le, buf := newPipeEditor(t, "1\ni-1\n") // pick i-1, then type-to-confirm with the instance ID
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
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
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsInstanceBlockDevice{DeleteOnTermination: aws.Bool(false), VolumeId: aws.String("vol-1")}},
		},
	}
	term, le, buf := newPipeEditor(t, "1\ni-1\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "DeleteOnTermination") {
		t.Errorf("expected no DeleteOnTermination warning when no volume has it set, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_ProductionWarningShown(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Environment: "production", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\ni-1\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected a production warning in output, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_ProductionWarningAbsentOtherwise(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Environment: "development", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\ni-1\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(buf.String()), "production") {
		t.Errorf("expected no production warning for a development instance, got:\n%s", buf.String())
	}
}

func TestTerminateEC2Instance_TypeToConfirmAcceptsName(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "1\nweb\n") // type the Name, not the ID
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput == nil {
		t.Fatal("TerminateInstances was never called despite matching the Name")
	}
}

func TestTerminateEC2Instance_TypeToConfirmMismatchCancels(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "1\nwrong\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTerminateInstancesInput != nil {
		t.Error("TerminateInstances was called despite a type-to-confirm mismatch")
	}
}

func TestTerminateEC2Instance_CancelledPickList(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "0\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastTerminateInstancesInput != nil {
		t.Error("TerminateInstances was called despite cancelling the pick list")
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
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	fake := &fakeEC2Client{terminateInstancesErr: errors.New("boom")}
	term, le, _ := newPipeEditor(t, "1\ni-1\n")
	err := TerminateEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err == nil {
		t.Fatal("expected an error")
	}
}
