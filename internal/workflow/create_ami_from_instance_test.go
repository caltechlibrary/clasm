package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCreateAMIFromInstance_HappyPathRunningInstance(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", State: "running"}}
	input := "1\n" + // pick i-1
		"\n" + // AMI name (blank -> default)
		"\n" + // description (blank)
		"y\n" + // no-reboot confirm (offered since running)
		"caltechauthors\n" + // Project
		"production\n" + // Environment
		"y\n" // confirm create

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{
		createImageID:           "ami-new1",
		imageAvailableAfterCall: 1,
		describeVolumesOutput:   []types.Volume{{VolumeId: aws.String("vol-1"), Size: aws.Int32(50)}},
	}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 0} // SSM unavailable -> fstrim skipped cleanly, no extra input needed

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	in := ec2Client.lastCreateImageInput
	if in == nil {
		t.Fatal("CreateImage was never called")
	}
	if aws.ToString(in.InstanceId) != "i-1" {
		t.Errorf("InstanceId = %q, want %q", aws.ToString(in.InstanceId), "i-1")
	}
	if !strings.HasPrefix(aws.ToString(in.Name), "newauthors-copy-") {
		t.Errorf("Name = %q, want the default newauthors-copy-<date> suggestion", aws.ToString(in.Name))
	}
	if !aws.ToBool(in.NoReboot) {
		t.Error("NoReboot = false, want true")
	}

	out := buf.String()
	if !strings.Contains(out, "50") {
		t.Errorf("expected the volume size in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Crash-consistency") {
		t.Errorf("expected the crash-consistency guidance for a running instance, got:\n%s", out)
	}
	if !strings.Contains(out, "now available") {
		t.Errorf("expected an available-AMI confirmation, got:\n%s", out)
	}
}

func TestCreateAMIFromInstance_StoppedInstanceSkipsCrashGuidanceAndReboot(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "authorstest", State: "stopped"}}
	input := "1\n" + // pick i-1
		"\n" + // AMI name (blank -> default)
		"\n" + // description
		"caltechauthors\n" + // Project (no no-reboot prompt for a stopped instance)
		"test\n" + // Environment
		"y\n" // confirm create

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{createImageID: "ami-new2", imageAvailableAfterCall: 1}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastCreateImageInput == nil || aws.ToBool(ec2Client.lastCreateImageInput.NoReboot) {
		t.Errorf("NoReboot = %v, want false/unset for a stopped instance", ec2Client.lastCreateImageInput)
	}
	if strings.Contains(buf.String(), "Crash-consistency") {
		t.Errorf("did not expect crash-consistency guidance for a stopped instance, got:\n%s", buf.String())
	}
}

func TestCreateAMIFromInstance_DeclinedConfirmationDoesNotCreate(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "stopped"}}
	input := "1\n\n\ncaltechauthors\ntest\nn\n"
	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastCreateImageInput != nil {
		t.Error("CreateImage was called despite a declined confirmation")
	}
}

func TestCreateAMIFromInstance_CancelledPickList(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", State: "stopped"}}
	term, le, _ := newPipeEditor(t, "0\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, instances)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if ec2Client.lastCreateImageInput != nil {
		t.Error("CreateImage was called despite cancelling the pick list")
	}
}

func TestCreateAMIFromInstance_ReportsFailedState(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "stopped"}}
	input := "1\n\n\ncaltechauthors\ntest\ny\n"
	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{createImageID: "ami-new3", imageFailedAfterCall: 1}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "failed") {
		t.Errorf("expected a failure message in output, got:\n%s", buf.String())
	}
}

func TestCreateAMIFromInstance_NoInstances(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, le, ec2Client, ssmClient, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}
