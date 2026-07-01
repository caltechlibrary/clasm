package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestCreateInstanceFromAMI_HappyPathNoUserData(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "1\n" + // pick ami-1
		"t3.micro\n" + // instance type
		"my-key\n" + // key pair
		"sg-1\n" + // security groups
		"subnet-1\n" + // subnet
		"\n" + // IAM profile
		"\n" + // user data (blank -- skip cloud-init check)
		"web\n" + // Name
		"caltechauthors\n" + // Project
		"production\n" + // Environment
		"y\n" // confirm launch

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1, publicIP: "1.2.3.4"}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromAMI(context.Background(), term, le, ec2Client, ssmClient, images)
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
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "1\n" +
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"#cloud-config\n" + // user data present -> triggers cloud-init check
		"web\n" +
		"caltechauthors\n" +
		"production\n" +
		"y\n"

	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-abc123", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "status: done\n"}

	err := CreateInstanceFromAMI(context.Background(), term, le, ec2Client, ssmClient, images)
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

func TestCreateInstanceFromAMI_CancelledPickListReturnsCleanly(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "0\n") // cancel the AMI pick list
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromAMI(context.Background(), term, le, ec2Client, ssmClient, images)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite cancelling the AMI pick list")
	}
}

func TestCreateInstanceFromAMI_DeclinedConfirmationDoesNotLaunch(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "1\n" +
		"t3.micro\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"\n" +
		"web\n" +
		"caltechauthors\n" +
		"production\n" +
		"n\n" // decline

	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := CreateInstanceFromAMI(context.Background(), term, le, ec2Client, ssmClient, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("RunInstances was called despite a declined confirmation")
	}
}
