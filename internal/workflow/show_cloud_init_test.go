package workflow

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
	term, le, buf := newPipeEditor("\n") // skip export
	ec2Client := &fakeEC2Client{userDataValue: base64.StdEncoding.EncodeToString([]byte("#cloud-config"))}

	err := showCloudInitForInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, inst, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "#cloud-config") {
		t.Errorf("expected the decoded cloud-init in output, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_InstancePathNoUserData(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}
	term, le, buf := newPipeEditor("")
	ec2Client := &fakeEC2Client{}

	err := showCloudInitForInstance(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, inst, le, buf)
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
	term, le, buf := newPipeEditor(input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config from AMI"}

	err := showCloudInitForAMI(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img, nil, le, buf)
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
	term, le, buf := newPipeEditor("n\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInitForAMI(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("a temporary instance was launched despite a declined confirmation")
	}
}

func TestShowCloudInit_NoInstances(t *testing.T) {
	term, _, buf := newPipeEditor("")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInit(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil, nil, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_NoAMIs(t *testing.T) {
	term, _, buf := newPipeEditor("")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := showCloudInit(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil, nil, newHuhAccessibleInput("2\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}

// An AMI whose source had no user-data reads back empty. That is "none found",
// said plainly, not a blank cloud-init block followed by a prompt to save
// nothing to a file.
func TestShowCloudInit_AMIPathEmptyUserDataSaysSoAndDoesNotOfferToSave(t *testing.T) {
	img := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	term, le, buf := newPipeEditor("y\n") // confirm only; a second prompt would hang the test's pipe
	ec2Client := extractionFake(egressAll())
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: ""}

	err := showCloudInitForAMI(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img, nil, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No user-data was found in AMI ami-1") {
		t.Errorf("want the plain message, got:\n%s", out)
	}
	if strings.Contains(out, "--- cloud-init ---") || strings.Contains(out, "Save to file") {
		t.Errorf("an empty result must not be displayed as a cloud-init block or offered for saving:\n%s", out)
	}
}

func TestShowCloudInit_AMIPathPassesTheConfiguredSecurityGroup(t *testing.T) {
	img := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	term, le, buf := newPipeEditor("y\n\n")
	ec2Client := extractionFake(nil) // default group closed; the configured one is open
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config"}
	err := showCloudInitForAMI(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img, map[string]string{"us-east-1": "sg-open"}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ec2Client.lastRunInstancesInput.SecurityGroupIds; len(got) != 1 || got[0] != "sg-open" {
		t.Errorf("SecurityGroupIds = %v, want [sg-open]", got)
	}
}

// A security group belongs to one region, so a group configured for another
// region must not be handed to an AMI's own.
func TestShowCloudInit_AMIPathIgnoresAnotherRegionsSecurityGroup(t *testing.T) {
	img := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	term, le, buf := newPipeEditor("y\n\n")
	ec2Client := extractionFake([]ec2types.IpPermission{{IpProtocol: aws.String("-1")}}) // default group open
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config"}
	err := showCloudInitForAMI(context.Background(), term, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, img, map[string]string{"us-west-2": "sg-elsewhere"}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := ec2Client.lastRunInstancesInput.SecurityGroupIds; len(got) != 0 {
		t.Errorf("SecurityGroupIds = %v, want none (the VPC default)", got)
	}
}
