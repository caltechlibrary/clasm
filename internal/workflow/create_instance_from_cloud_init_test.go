package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCreateInstanceFromCloudInit_HappyPath(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "#cloud-config\n" + // cloud-init YAML, first
		"1\n" + // pick ami-1
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"web\n" +
		"caltechauthors\n" +
		"production\n" +
		"y\n" // confirm launch

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, ec2Client, ssmClient, images)
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
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "#cloud-config\n" +
		"1\n" +
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"web\n" +
		"caltechauthors\n" +
		"production\n" +
		"n\n" // decline

	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, ec2Client, ssmClient, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}

func TestCreateInstanceFromCloudInit_CancelledPickListReturnsCleanly(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "#cloud-config\n" + "0\n" // provide cloud-init, then cancel the AMI pick list
	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromCloudInit(context.Background(), term, le, ec2Client, ssmClient, images)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite cancelling the AMI pick list")
	}
}
