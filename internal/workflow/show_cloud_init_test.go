package workflow

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The Instance-vs-AMI kind picker converted to huh.Select (DESIGN.md's
// full conversion punch list): its selection is fed via a separate
// newHuhAccessibleInput reader (kindInput), not le, which still feeds
// every other prompt in this function. Cancelling that picker is only
// reachable via 'q'/ctrl+c, which accessible mode has no keyboard to
// simulate (mapMenuPickerErr's doc comment covers the same limitation),
// so the old "0=Cancel" test is retired rather than kept. The instance/
// AMI picker also converted to tui.RunPicker (Picker tier) -- a real
// bubbletea Program that can't be pipe-tested -- so the happy-path tests
// below exercise showCloudInitForInstance/showCloudInitForAMI directly
// with an already-resolved instance/AMI; showCloudInit's own picker-
// selection step is covered only by manual/interactive verification, the
// same accepted limitation this session's other Picker-tier conversions
// already have.

func TestShowCloudInit_InstancePathWithUserData(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}
	term, le, buf := newPipeEditor(t, "\n") // skip export
	ec2Client := &fakeEC2Client{userDataValue: base64.StdEncoding.EncodeToString([]byte("#cloud-config"))}

	err := showCloudInitForInstance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "#cloud-config") {
		t.Errorf("expected the decoded cloud-init in output, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_InstancePathNoUserData(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}
	term, le, buf := newPipeEditor(t, "")
	ec2Client := &fakeEC2Client{}

	err := showCloudInitForInstance(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No user-data") {
		t.Errorf("expected a no-user-data message, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_AMIPathAcceptedConfirmation(t *testing.T) {
	img := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "y\n" + // confirm the billable extraction
		"\n" // skip export
	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config from AMI"}

	err := showCloudInitForAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "#cloud-config from AMI") {
		t.Errorf("expected the extracted cloud-init in output, got:\n%s", buf.String())
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Errorf("terminateInstancesCallCount = %d, want 1", ec2Client.terminateInstancesCallCount)
	}
}

func TestShowCloudInit_AMIPathDeclinedConfirmation(t *testing.T) {
	img := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	term, le, _ := newPipeEditor(t, "n\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInitForAMI(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("a temporary instance was launched despite a declined confirmation")
	}
}

func TestShowCloudInit_NoInstances(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_NoAMIs(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil, newHuhAccessibleInput("2\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}
