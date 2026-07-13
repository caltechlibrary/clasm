package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The curated-instance-type picker (huh.Select) and every free-text
// prompt in this function now share one accessible-mode reader
// (menuInput), read in sequence one line at a time, in the exact order
// collectLaunchInstanceParamsFromCloudInit's own flow reads them. The
// cloud-init-YAML-file prompt and the AMI picker (also converted to
// tui.RunPicker, Picker tier -- a real bubbletea Program that can't be
// pipe-tested) both now run in the exported CreateInstanceFromCloudInit,
// before the testable createInstanceFromCloudInit core, which takes the
// resolved userData/image directly. CreateInstanceFromCloudInit's own
// prompt/AMI-selection steps (including cancellation) are covered only
// by manual/interactive verification, the same accepted limitation this
// session's other Picker-tier conversions already have.

func TestCreateInstanceFromCloudInit_HappyPath(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"caltechauthors\n" +
		"production\n" +
		"y\n" // confirm launch

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1, describeKeyPairsErr: errNoKeyPairsConfigured}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}

	err := createInstanceFromCloudInit(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, fakeIAMClientNoProfiles(), "#cloud-config", image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput == nil {
		t.Fatal("RunInstances was never called")
	}
	if ssmClient.describeCalls == 0 {
		t.Error("expected SSM to be queried for cloud-init completion")
	}
	if !strings.Contains(buf.String(), "cloud-init") {
		t.Errorf("expected a cloud-init status message in output, got:\n%s", buf.String())
	}
}

func TestCreateInstanceFromCloudInit_DeclinedConfirmationDoesNotLaunch(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}
	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"caltechauthors\n" +
		"production\n" +
		"n\n" // decline

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}
	ssmClient := &fakeSSMClient{}

	err := createInstanceFromCloudInit(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, fakeIAMClientNoProfiles(), "#cloud-config", image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}
