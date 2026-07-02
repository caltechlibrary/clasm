package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCreateInstanceFromCloudInit_HappyPath(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := path + "\n" + // cloud-init YAML file path, first
		"1\n" + // pick ami-1
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile: select (none)
		"caltechauthors\n" +
		"production\n" +
		"y\n" // confirm launch

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, &fakeIAMClient{}, images)
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
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := path + "\n" +
		"1\n" +
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile: select (none)
		"caltechauthors\n" +
		"production\n" +
		"n\n" // decline

	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}

func TestCreateInstanceFromCloudInit_CancelledPickListReturnsCleanly(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := path + "\n" + "0\n" // provide cloud-init, then cancel the AMI pick list
	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite cancelling the AMI pick list")
	}
}
