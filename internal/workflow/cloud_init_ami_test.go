package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestExtractCloudInitFromAMI_HappyPath(t *testing.T) {
	ec2Client := &fakeEC2Client{runInstancesID: "i-temp1", runningAfterCall: 1}
	ssmClient := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config\npackages: [docker]"}

	got, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", testPollInterval, testPollInterval)
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

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", 20*testPollInterval, testPollInterval)
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

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", testPollInterval, testPollInterval)
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

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", 20*testPollInterval, testPollInterval)
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

	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when launch itself fails")
	}
	if ec2Client.terminateInstancesCallCount != 0 {
		t.Errorf("terminateInstancesCallCount = %d, want 0 (nothing to clean up)", ec2Client.terminateInstancesCallCount)
	}
}

// --- the disposable instance's security group (2026-09-30) ---
//
// Found live: the VPC default security group here has no outbound rules, and
// clasm launched the disposable instance without naming a group, so its SSM
// agent could not reach the SSM endpoints and every extraction timed out after
// three minutes with "SSM never came online". With a group that allows outbound
// traffic the same AMI registered in seventy seconds.

func egressAll() []ec2types.IpPermission {
	return []ec2types.IpPermission{{IpProtocol: aws.String("-1")}}
}

// extractionFake is a world with a default VPC whose default group has the given
// outbound rules.
func extractionFake(defaultEgress []ec2types.IpPermission) *fakeEC2Client {
	return &fakeEC2Client{
		runInstancesID: "i-temp1", runningAfterCall: 1,
		subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-d"), VpcId: aws.String("vpc-d"), DefaultForAz: aws.Bool(true)}},
		securityGroups: []ec2types.SecurityGroup{
			{GroupId: aws.String("sg-default"), GroupName: aws.String("default"), VpcId: aws.String("vpc-d"), IpPermissionsEgress: defaultEgress},
			{GroupId: aws.String("sg-open"), GroupName: aws.String("open"), VpcId: aws.String("vpc-d"), IpPermissionsEgress: egressAll()},
			{GroupId: aws.String("sg-closed"), GroupName: aws.String("closed"), VpcId: aws.String("vpc-d")},
			{GroupId: aws.String("sg-web"), GroupName: aws.String("web"), VpcId: aws.String("vpc-d"),
				IpPermissionsEgress: []ec2types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80)}}},
			{GroupId: aws.String("sg-https"), GroupName: aws.String("https"), VpcId: aws.String("vpc-d"),
				IpPermissionsEgress: []ec2types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(400), ToPort: aws.Int32(500)}}},
		},
	}
}

func okSSM() *fakeSSMClient {
	return &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "#cloud-config"}
}

// A default group with no outbound rules is refused before anything billable is
// launched, with the reason and the fix, as a usage error (exit 2).
func TestExtractCloudInitFromAMI_RefusesADefaultGroupWithNoOutboundRules(t *testing.T) {
	ec2Client := extractionFake(nil)
	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, okSSM(), "ami-1", "", testPollInterval, testPollInterval)
	var ue *UsageError
	if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
		t.Fatalf("want an exit-2 usage error, got %v", err)
	}
	for _, want := range []string{"sg-default", "outbound", "443", "cloud_init_extraction_security_group", "-security-group"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q: %v", want, err)
		}
	}
	if ec2Client.lastRunInstancesInput != nil || ec2Client.terminateInstancesCallCount != 0 {
		t.Error("nothing may be launched (or terminated) when the check refuses")
	}
}

func TestExtractCloudInitFromAMI_AcceptsADefaultGroupThatAllowsHTTPSOut(t *testing.T) {
	for name, egress := range map[string][]ec2types.IpPermission{
		"all traffic":    egressAll(),
		"tcp port range": {{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(400), ToPort: aws.Int32(500)}},
		"tcp 443 only":   {{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443)}},
	} {
		ec2Client := extractionFake(egress)
		if _, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, okSSM(), "ami-1", "", testPollInterval, testPollInterval); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if ec2Client.lastRunInstancesInput == nil || len(ec2Client.lastRunInstancesInput.SecurityGroupIds) != 0 {
			t.Errorf("%s: with no group named the launch must leave the choice to the VPC default, got %+v", name, ec2Client.lastRunInstancesInput)
		}
	}
	// Port 80 alone is not HTTPS.
	if _, err := ExtractCloudInitFromAMI(context.Background(), extractionFake([]ec2types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80)}}), okSSM(), "ami-1", "", testPollInterval, testPollInterval); err == nil {
		t.Error("a default group that only allows port 80 out must be refused")
	}
}

func TestExtractCloudInitFromAMI_NamedGroupIsUsedAndChecked(t *testing.T) {
	ec2Client := extractionFake(nil) // the default group is closed, but we name another
	if _, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, okSSM(), "ami-1", "sg-open", testPollInterval, testPollInterval); err != nil {
		t.Fatal(err)
	}
	if got := ec2Client.lastRunInstancesInput.SecurityGroupIds; len(got) != 1 || got[0] != "sg-open" {
		t.Errorf("SecurityGroupIds = %v, want [sg-open]", got)
	}

	for _, tc := range []struct{ sg, want string }{
		{"sg-closed", "sg-closed"}, {"sg-web", "sg-web"}, {"sg-missing", "not found"},
	} {
		ec2Client := extractionFake(egressAll())
		_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, okSSM(), "ami-1", tc.sg, testPollInterval, testPollInterval)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want a usage error mentioning %q, got %v", tc.sg, tc.want, err)
		}
		if ec2Client.lastRunInstancesInput != nil {
			t.Errorf("%s: nothing may be launched", tc.sg)
		}
	}
}

// The check only blocks on positive evidence. If the default VPC or its group
// cannot be found, or the lookup fails, the launch proceeds as it always did.
func TestExtractCloudInitFromAMI_UnableToCheckProceeds(t *testing.T) {
	for name, mutate := range map[string]func(*fakeEC2Client){
		"no default subnet":   func(f *fakeEC2Client) { f.subnets = nil },
		"no default group":    func(f *fakeEC2Client) { f.securityGroups = nil },
		"subnet lookup fails": func(f *fakeEC2Client) { f.describeSubnetsErr = errors.New("UnauthorizedOperation") },
		"group lookup fails":  func(f *fakeEC2Client) { f.describeSecurityGroupsErr = errors.New("UnauthorizedOperation") },
	} {
		f := extractionFake(nil)
		mutate(f)
		if _, err := ExtractCloudInitFromAMI(context.Background(), f, okSSM(), "ami-1", "", testPollInterval, testPollInterval); err != nil {
			t.Errorf("%s: should have proceeded, got %v", name, err)
		}
		if f.lastRunInstancesInput == nil {
			t.Errorf("%s: no launch", name)
		}
	}
}

// When SSM still never registers, the message points at the network rather than
// just saying it never came online.
func TestExtractCloudInitFromAMI_NeverOnlineMessageNamesTheNetwork(t *testing.T) {
	ec2Client := extractionFake(egressAll())
	_, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, &fakeSSMClient{}, "ami-1", "sg-open", 20*testPollInterval, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "outbound") || !strings.Contains(err.Error(), "sg-open") {
		t.Errorf("want a message naming the group and outbound access, got %v", err)
	}
	if ec2Client.terminateInstancesCallCount != 1 {
		t.Error("cleanup must still run")
	}
}
