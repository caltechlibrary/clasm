package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Instance selection (DESIGN.md's full conversion punch list, Picker
// tier) now runs a real bubbletea Program (tui.RunPicker), which can't
// be driven by a test's pipe input -- see internal/tui/picker_test.go
// for that component's own thorough test suite. Tests below exercise
// everything once an instance is already resolved via the unexported
// createAMIFromInstance; CreateAMIFromInstance's own picker-selection
// step is covered only by manual/interactive verification, the same
// accepted limitation power_state.go/terminate_instance.go/
// backup_archive.go's own conversions already have.

func TestCreateAMIFromInstance_HappyPathRunningInstance(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", State: "running", Region: "us-east-1"}
	input := "\n" + // AMI name (blank -> default)
		"\n" + // description (blank)
		"y\n" + // no-reboot confirm (offered since running)
		"caltechauthors\n" + // Project
		"production\n" + // Environment
		"y\n" // confirm create

	term, le, buf := newPipeEditor(input)
	ec2Client := &fakeEC2Client{
		createImageID:           "ami-new1",
		imageAvailableAfterCall: 1,
		describeVolumesOutput:   []types.Volume{{VolumeId: aws.String("vol-1"), Size: aws.Int32(50)}},
	}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 0} // SSM unavailable -> fstrim skipped cleanly, no extra input needed

	err := createAMIFromInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, le, buf)
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
	inst := inventory.Instance{InstanceID: "i-1", Name: "authorstest", State: "stopped", Region: "us-east-1"}
	input := "\n" + // AMI name (blank -> default)
		"\n" + // description
		"caltechauthors\n" + // Project (no no-reboot prompt for a stopped instance)
		"test\n" + // Environment
		"y\n" // confirm create

	term, le, buf := newPipeEditor(input)
	ec2Client := &fakeEC2Client{createImageID: "ami-new2", imageAvailableAfterCall: 1}
	ssmClient := &fakeSSMClient{}

	err := createAMIFromInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, le, buf)
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
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "stopped", Region: "us-east-1"}
	input := "\n\ncaltechauthors\ntest\nn\n"
	term, le, buf := newPipeEditor(input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := createAMIFromInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastCreateImageInput != nil {
		t.Error("CreateImage was called despite a declined confirmation")
	}
}

func TestCreateAMIFromInstance_ReportsFailedState(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "stopped", Region: "us-east-1"}
	input := "\n\ncaltechauthors\ntest\ny\n"
	term, le, buf := newPipeEditor(input)
	ec2Client := &fakeEC2Client{createImageID: "ami-new3", imageFailedAfterCall: 1}
	ssmClient := &fakeSSMClient{}

	err := createAMIFromInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, inst, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "failed") {
		t.Errorf("expected a failure message in output, got:\n%s", buf.String())
	}
}

func TestCreateAMIFromInstance_NoInstances(t *testing.T) {
	term, _, buf := newPipeEditor("")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateAMIFromInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}
