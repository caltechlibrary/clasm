package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestStartInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	if err := StartInstance(context.Background(), fake, "i-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStartInstancesInput == nil || len(fake.lastStartInstancesInput.InstanceIds) != 1 || fake.lastStartInstancesInput.InstanceIds[0] != "i-1" {
		t.Errorf("StartInstances called with %+v, want InstanceIds=[i-1]", fake.lastStartInstancesInput)
	}
}

func TestStartInstance_Failure(t *testing.T) {
	fake := &fakeEC2Client{startInstancesErr: errors.New("boom")}
	if err := StartInstance(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStartEC2Instance_HappyPath(t *testing.T) {
	instances := []inventory.Instance{
		{InstanceID: "i-running", Name: "web", State: "running", Region: "us-east-1"},
		{InstanceID: "i-stopped", Name: "db", State: "stopped", Region: "us-east-1"},
	}
	term, le, buf := newPipeEditor(t, "1\ny\n") // only one stopped instance to pick, then confirm
	fake := &fakeEC2Client{runningAfterCall: 1, publicIP: "5.6.7.8"}

	err := StartEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStartInstancesInput == nil || fake.lastStartInstancesInput.InstanceIds[0] != "i-stopped" {
		t.Errorf("StartInstances called with %+v, want InstanceIds=[i-stopped]", fake.lastStartInstancesInput)
	}
	out := buf.String()
	if !strings.Contains(out, "5.6.7.8") {
		t.Errorf("expected connection info in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Elastic IP") {
		t.Errorf("expected the changed-public-IP caveat in output, got:\n%s", out)
	}
}

func TestStartEC2Instance_NoStoppedInstances(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-running", State: "running", Region: "us-east-1"}}
	term, le, buf := newPipeEditor(t, "")
	fake := &fakeEC2Client{}

	err := StartEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStartInstancesInput != nil {
		t.Error("StartInstances was called despite no stopped instances")
	}
	if !strings.Contains(buf.String(), "No stopped") {
		t.Errorf("expected a no-stopped-instances message, got:\n%s", buf.String())
	}
}

func TestStartEC2Instance_CancelledPickList(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-stopped", State: "stopped", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "0\n")
	fake := &fakeEC2Client{}

	err := StartEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastStartInstancesInput != nil {
		t.Error("StartInstances was called despite cancelling the pick list")
	}
}

func TestStartEC2Instance_DeclinedConfirmationDoesNotStart(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-stopped", State: "stopped", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "1\nn\n")
	fake := &fakeEC2Client{}

	err := StartEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStartInstancesInput != nil {
		t.Error("StartInstances was called despite a declined confirmation")
	}
}

func TestStartEC2Instance_PropagatesStartError(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-stopped", State: "stopped", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "1\ny\n")
	fake := &fakeEC2Client{startInstancesErr: errors.New("boom")}

	err := StartEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestStopInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	if err := StopInstance(context.Background(), fake, "i-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStopInstancesInput == nil || len(fake.lastStopInstancesInput.InstanceIds) != 1 || fake.lastStopInstancesInput.InstanceIds[0] != "i-1" {
		t.Errorf("StopInstances called with %+v, want InstanceIds=[i-1]", fake.lastStopInstancesInput)
	}
}

func TestStopInstance_Failure(t *testing.T) {
	fake := &fakeEC2Client{stopInstancesErr: errors.New("boom")}
	if err := StopInstance(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStopEC2Instance_HappyPath(t *testing.T) {
	instances := []inventory.Instance{
		{InstanceID: "i-stopped", Name: "db", State: "stopped", Region: "us-east-1"},
		{InstanceID: "i-running", Name: "web", State: "running", Region: "us-east-1"},
	}
	term, le, buf := newPipeEditor(t, "1\ny\n") // only one running instance to pick, then confirm
	fake := &fakeEC2Client{stoppedAfterCall: 1}

	err := StopEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStopInstancesInput == nil || fake.lastStopInstancesInput.InstanceIds[0] != "i-running" {
		t.Errorf("StopInstances called with %+v, want InstanceIds=[i-running]", fake.lastStopInstancesInput)
	}
	if !strings.Contains(buf.String(), "is now stopped") {
		t.Errorf("expected a stopped confirmation message in output, got:\n%s", buf.String())
	}
}

func TestStopEC2Instance_NoRunningInstances(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-stopped", State: "stopped", Region: "us-east-1"}}
	term, le, buf := newPipeEditor(t, "")
	fake := &fakeEC2Client{}

	err := StopEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStopInstancesInput != nil {
		t.Error("StopInstances was called despite no running instances")
	}
	if !strings.Contains(buf.String(), "No running") {
		t.Errorf("expected a no-running-instances message, got:\n%s", buf.String())
	}
}

func TestStopEC2Instance_CancelledPickList(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-running", State: "running", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "0\n")
	fake := &fakeEC2Client{}

	err := StopEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastStopInstancesInput != nil {
		t.Error("StopInstances was called despite cancelling the pick list")
	}
}

func TestStopEC2Instance_DeclinedConfirmationDoesNotStop(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-running", State: "running", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "1\nn\n")
	fake := &fakeEC2Client{}

	err := StopEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastStopInstancesInput != nil {
		t.Error("StopInstances was called despite a declined confirmation")
	}
}

func TestStopEC2Instance_PropagatesStopError(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-running", State: "running", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "1\ny\n")
	fake := &fakeEC2Client{stopInstancesErr: errors.New("boom")}

	err := StopEC2Instance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances)
	if err == nil {
		t.Fatal("expected an error")
	}
}
