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
// collectLaunchInstanceParams's own flow reads them. The AMI picker also
// converted to tui.RunPicker (Picker tier) -- a real bubbletea Program
// that can't be pipe-tested -- so createInstanceFromAMI now takes an
// already-resolved image directly; CreateInstanceFromAMI's own
// AMI-selection step (including cancellation) is covered only by
// manual/interactive verification, the same accepted limitation this
// session's other Picker-tier conversions already have.

func TestCreateInstanceFromAMI_HappyPathNoUserData(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n" + // Name
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-1\n" + // subnet
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" + // user data (blank -- skip cloud-init check)
		"caltechauthors\n" + // Project
		"production\n" + // Environment
		"y\n" // confirm launch

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1, publicIP: "1.2.3.4", describeKeyPairsErr: errNoKeyPairsConfigured}
	ssmClient := &fakeSSMClient{}

	err := createInstanceFromAMI(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, fakeIAMClientNoProfiles(), image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput == nil {
		t.Fatal("RunInstances was never called")
	}
	if ssmClient.describeCalls != 0 {
		t.Errorf("SSM was queried (%d calls) even though no user data was provided", ssmClient.describeCalls)
	}
	if !strings.Contains(buf.String(), "1.2.3.4") {
		t.Errorf("expected connection info in output, got:\n%s", buf.String())
	}
}

func TestCreateInstanceFromAMI_WithUserDataChecksCloudInit(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"#cloud-config\n" + // user data present -> triggers cloud-init check
		"caltechauthors\n" +
		"production\n" +
		"y\n"

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1, describeKeyPairsErr: errNoKeyPairsConfigured}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}

	err := createInstanceFromAMI(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, fakeIAMClientNoProfiles(), image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ssmClient.describeCalls == 0 {
		t.Error("expected SSM to be queried for cloud-init completion since user data was provided")
	}
	if !strings.Contains(buf.String(), "cloud-init") {
		t.Errorf("expected a cloud-init status message in output, got:\n%s", buf.String())
	}
}

func TestCreateInstanceFromAMI_DeclinedConfirmationDoesNotLaunch(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"\n" +
		"caltechauthors\n" +
		"production\n" +
		"n\n" // decline

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}
	ssmClient := &fakeSSMClient{}

	err := createInstanceFromAMI(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, fakeIAMClientNoProfiles(), image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}
