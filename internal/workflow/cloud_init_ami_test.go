package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestExtractCloudInitFromAMI_HappyPath(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config\npackages: [docker]"}

	got, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "#cloud-config\npackages: [docker]" {
		t.Errorf("got %q, want the fixture stdout", got)
	}
	if ec2Client.lastRunInstancesInput == nil || string(ec2Client.lastRunInstancesInput.InstanceType) == "" {
		t.Error("RunInstances was not called with an instance type")
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Errorf("terminateInstancesCallCount = %d, want 1", ec2Client.terminateInstancesCallCount)
	}
	if ec2Client.lastTerminateInstancesInput == nil || ec2Client.lastTerminateInstancesInput.InstanceIds[0] != "i-temp1" {
		t.Errorf("TerminateInstances called with %+v, want InstanceIds=[i-temp1]", ec2Client.lastTerminateInstancesInput)
	}
}

func TestExtractCloudInitFromAMI_CleansUpOnSSMNeverOnline(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 0} // never comes online

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", 20*testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when SSM never comes online")
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Errorf("terminateInstancesCallCount = %d, want 1 (cleanup must still run)", ec2Client.terminateInstancesCallCount)
	}
}

func TestExtractCloudInitFromAMI_CleansUpOnCommandFailure(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the SSM command fails")
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Errorf("terminateInstancesCallCount = %d, want 1 (cleanup must still run)", ec2Client.terminateInstancesCallCount)
	}
}

func TestExtractCloudInitFromAMI_CleansUpWhenInstanceNeverReachesRunning(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1"} // never running
	ssmClient := &fakeSSMClient{}

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", 20*testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error when the instance never reaches running")
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Errorf("terminateInstancesCallCount = %d, want 1 (cleanup must still run)", ec2Client.terminateInstancesCallCount)
	}
}

func TestExtractCloudInitFromAMI_NoTerminateWhenLaunchFails(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesErr: errors.New("boom")}
	ssmClient := &fakeSSMClient{}

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when launch itself fails")
	}
	if ec2Client.terminateInstancesCallCount != 0 {
		t.Errorf("terminateInstancesCallCount = %d, want 0 (nothing to clean up)", ec2Client.terminateInstancesCallCount)
	}
}
