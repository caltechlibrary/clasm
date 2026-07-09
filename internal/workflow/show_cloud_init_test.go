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

func TestShowCloudInit_InstancePathWithUserData(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	input := "1\n" + // Instance
		"1\n" + // pick i-1
		"\n" // skip export
	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{userDataValue: base64.StdEncoding.EncodeToString([]byte("#cloud-config"))}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "#cloud-config") {
		t.Errorf("expected the decoded cloud-init in output, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_InstancePathNoUserData(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	input := "1\n1\n"
	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No user-data") {
		t.Errorf("expected a no-user-data message, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_AMIPathAcceptedConfirmation(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "2\n" + // AMI
		"1\n" + // pick ami-1
		"y\n" + // confirm the billable extraction
		"\n" // skip export
	term, le, buf := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config from AMI"}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, images)
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
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	input := "2\n1\nn\n"
	term, le, _ := newPipeEditor(t, input)
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("a temporary instance was launched despite a declined confirmation")
	}
}

func TestShowCloudInit_CancelledKindPickList(t *testing.T) {
	term, le, _ := newPipeEditor(t, "0\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
}

func TestShowCloudInit_NoInstances(t *testing.T) {
	term, le, buf := newPipeEditor(t, "1\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestShowCloudInit_NoAMIs(t *testing.T) {
	term, le, buf := newPipeEditor(t, "2\n")
	ec2Client := &fakeEC2Client{}
	ssmClient := &fakeSSMClient{}

	err := ShowCloudInit(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}
