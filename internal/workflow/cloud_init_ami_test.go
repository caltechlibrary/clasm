package workflow

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// --- reading an AMI's user-data (2026-09-30) ---
//
// Found by a round trip (template with user-data -> instance -> AMI ->
// extraction): the extraction returned nothing for an AMI whose source HAD
// user-data. On boot cloud-init points /var/lib/cloud/instance at the NEW
// instance's directory, so /var/lib/cloud/instance/user-data.txt is the
// temporary instance's own, empty file (0 bytes, confirmed on a real
// AMI-launched instance). The source's user-data is in the other directory
// under /var/lib/cloud/instances/, and clasm stores user-data gzipped, so the
// file starts 1f 8b and needs decompressing. These tests run the real remote
// script, in POSIX sh as SSM does, against a fake /var/lib/cloud tree.

// cloudTree builds a fake /var/lib/cloud under t.TempDir().
type cloudTree struct {
	t    *testing.T
	root string
}

func newCloudTree(t *testing.T) *cloudTree {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "instances"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &cloudTree{t: t, root: root}
}

// instance adds instances/<id> with the given user-data.txt bytes, at mtime age.
func (c *cloudTree) instance(id string, userData []byte, mtime time.Time) {
	c.t.Helper()
	dir := filepath.Join(c.root, "instances", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.t.Fatal(err)
	}
	if userData != nil {
		if err := os.WriteFile(filepath.Join(dir, "user-data.txt"), userData, 0o600); err != nil {
			c.t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		c.t.Fatal(err)
	}
}

// current points "instance" at instances/<id>, as cloud-init does on boot.
func (c *cloudTree) current(id string) {
	c.t.Helper()
	if err := os.Symlink(filepath.Join(c.root, "instances", id), filepath.Join(c.root, "instance")); err != nil {
		c.t.Fatal(err)
	}
}

// run executes the real remote script against the tree and returns its stdout.
func (c *cloudTree) run() (string, error) {
	c.t.Helper()
	script := strings.ReplaceAll(amiUserDataCommand, "/var/lib/cloud", c.root)
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	return string(out), err
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The case found live: the current instance has empty user-data, the AMI's
// source kept its own, gzipped.
func TestAMIUserDataCommand_ReadsTheSourceInstanceNotTheCurrentOne(t *testing.T) {
	now := time.Now()
	c := newCloudTree(t)
	c.instance("i-source", gz(t, "#cloud-config\n# marker\n"), now.Add(-time.Hour))
	c.instance("i-temp", []byte{}, now)
	c.current("i-temp")
	got, err := c.run()
	if err != nil || got != "#cloud-config\n# marker\n" {
		t.Errorf("got %q err=%v, want the source's decompressed user-data", got, err)
	}
}

func TestAMIUserDataCommand_PlainUserDataIsPassedThrough(t *testing.T) {
	now := time.Now()
	c := newCloudTree(t)
	c.instance("i-source", []byte("#!/bin/bash\necho hi\n"), now.Add(-time.Hour))
	c.instance("i-temp", []byte{}, now)
	c.current("i-temp")
	if got, err := c.run(); err != nil || got != "#!/bin/bash\necho hi\n" {
		t.Errorf("got %q err=%v", got, err)
	}
}

// An AMI that has been through several instances: the most recent other than
// the current one is the source the image was taken from.
func TestAMIUserDataCommand_PicksTheNewestOtherInstance(t *testing.T) {
	now := time.Now()
	c := newCloudTree(t)
	c.instance("i-oldest", gz(t, "oldest\n"), now.Add(-48*time.Hour))
	c.instance("i-source", gz(t, "source\n"), now.Add(-time.Hour))
	c.instance("i-temp", []byte{}, now)
	c.current("i-temp")
	if got, err := c.run(); err != nil || got != "source\n" {
		t.Errorf("got %q err=%v, want the newest non-current instance's user-data", got, err)
	}
}

// Nothing to find is not an error: the script succeeds with no output, which the
// caller reports as "no user-data found".
func TestAMIUserDataCommand_NothingFoundIsEmptyAndSucceeds(t *testing.T) {
	now := time.Now()
	for name, build := range map[string]func(*cloudTree){
		"no other instance": func(c *cloudTree) { c.instance("i-temp", []byte{}, now); c.current("i-temp") },
		"source without user-data": func(c *cloudTree) {
			c.instance("i-source", nil, now.Add(-time.Hour))
			c.instance("i-temp", []byte{}, now)
			c.current("i-temp")
		},
		"source with empty file": func(c *cloudTree) {
			c.instance("i-source", []byte{}, now.Add(-time.Hour))
			c.instance("i-temp", []byte{}, now)
			c.current("i-temp")
		},
	} {
		c := newCloudTree(t)
		build(c)
		if got, err := c.run(); err != nil || got != "" {
			t.Errorf("%s: got %q err=%v, want empty output and success", name, got, err)
		}
	}
}

// The command must never read the current instance's file: that is the bug.
func TestAMIUserDataCommand_DoesNotReadTheCurrentInstancesFile(t *testing.T) {
	if strings.Contains(amiUserDataCommand, "/var/lib/cloud/instance/user-data") {
		t.Errorf("the script reads the current instance's user-data, which is always the temporary instance's own:\n%s", amiUserDataCommand)
	}
	if !strings.Contains(amiUserDataCommand, "/var/lib/cloud/instances") {
		t.Error("the script must look in /var/lib/cloud/instances")
	}
}

// ExtractCloudInitFromAMI sends that script, and returns what it printed.
func TestExtractCloudInitFromAMI_SendsTheSourceInstanceScript(t *testing.T) {
	ec2Client := extractionFake(egressAll())
	ssmClient := okSSM()
	got, err := ExtractCloudInitFromAMI(context.Background(), ec2Client, ssmClient, "ami-1", "", testPollInterval, testPollInterval)
	if err != nil || got != "#cloud-config" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if !commandSent(ssmClient.sentCommands, "/var/lib/cloud/instances") || commandSent(ssmClient.sentCommands, "cat /var/lib/cloud/instance/user-data.txt") {
		t.Errorf("sent %v, want the instances/ script and not a read of the current instance", ssmClient.sentCommands)
	}
}
