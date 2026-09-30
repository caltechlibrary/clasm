package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func cliInstances() []inventory.Instance {
	return []inventory.Instance{
		{InstanceID: "i-abc123", Name: "web-1", Region: "us-west-2"},
		{InstanceID: "i-dup1", Name: "twin", Region: "us-west-2"},
		{InstanceID: "i-dup2", Name: "twin", Region: "us-east-1"},
	}
}

func cliImages() []inventory.Image {
	return []inventory.Image{{ImageID: "ami-abc123", Name: "granian-13-init", Region: "us-west-2"}}
}

func cliTemplates() []inventory.LaunchTemplate {
	return []inventory.LaunchTemplate{{TemplateID: "lt-1", Name: "authors-tmpl", DefaultVersion: 1, LatestVersion: 3, Region: "us-west-2"}}
}

func usageErr(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), wantSubstr) || CLIExitCode(err) != 2 {
		t.Errorf("want an exit-2 usage error mentioning %q, got: %v", wantSubstr, err)
	}
}

func instanceFake() *fakeEC2Client {
	return &fakeEC2Client{
		runningAfterCall: 1, instanceDetailImageID: "ami-1", instanceDetailInstanceType: "t3.micro",
		instanceDetailSecurityGroup: []string{"sg-1", "sg-2"},
		instanceTags:                []types.Tag{{Key: aws.String("Name"), Value: aws.String("web-1")}, {Key: aws.String("Owner"), Value: aws.String("dld")}},
		describeVolumesOutput:       []types.Volume{{VolumeId: aws.String("vol-1"), Size: aws.Int32(20)}, {VolumeId: aws.String("vol-2"), Size: aws.Int32(100)}},
	}
}

func TestRunShowInstanceDetailCLI_TextIsTheDetailViewWithoutALeadingBlankLine(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": instanceFake()}
	var b bytes.Buffer
	if err := RunShowInstanceDetailCLI(context.Background(), &b, clients, cliInstances(), "web-1", ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "Instance i-abc123") || !strings.Contains(b.String(), "t3.micro") {
		t.Errorf("got:\n%q", b.String())
	}
}

func TestRunShowInstanceDetailCLI_JSON(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": instanceFake()}
	var b bytes.Buffer
	if err := RunShowInstanceDetailCLI(context.Background(), &b, clients, cliInstances(), "i-abc123", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if got["instance_id"] != "i-abc123" || got["instance_type"] != "t3.micro" || got["total_ebs_gib"] != float64(120) {
		t.Errorf("got %v", got)
	}
	if sgs, _ := got["security_group_ids"].([]any); len(sgs) != 2 {
		t.Errorf("security_group_ids = %v", got["security_group_ids"])
	}
	if vols, _ := got["ebs_volumes"].([]any); len(vols) != 2 {
		t.Errorf("ebs_volumes = %v", got["ebs_volumes"])
	}
	if tags, _ := got["tags"].(map[string]any); tags["Owner"] != "dld" {
		t.Errorf("tags = %v", got["tags"])
	}
}

// An argument that names nothing, or two instances, is a usage error and is
// never guessed at; nothing is sent to AWS (the client map is empty).
func TestRunShowInstanceDetailCLI_ResolutionFailuresAreUsageErrors(t *testing.T) {
	var b bytes.Buffer
	err := RunShowInstanceDetailCLI(context.Background(), &b, nil, cliInstances(), "nope", ui.FormatText)
	usageErr(t, err, "nope")
	err = RunShowInstanceDetailCLI(context.Background(), &b, nil, cliInstances(), "twin", ui.FormatText)
	usageErr(t, err, "ambiguous")
	if b.Len() != 0 {
		t.Errorf("nothing may be written, got %q", b.String())
	}
}

func TestRunShowInstanceDetailCLI_AWSFailureIsNotAUsageError(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{describeErr: errors.New("boom")}}
	err := RunShowInstanceDetailCLI(context.Background(), &bytes.Buffer{}, clients, cliInstances(), "web-1", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %v", err)
	}
}

func TestRunShowAMIDetailCLI(t *testing.T) {
	fake := &fakeEC2Client{
		imageAvailableAfterCall: 1, describeImagesName: "granian-13-init", describeImagesArchitecture: "x86_64", describeImagesEnaSupport: true,
		describeImagesRootDeviceName: "/dev/sda1",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(20), SnapshotId: aws.String("snap-root")}},
		},
	}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	var b bytes.Buffer
	// By Name tag and by ID resolve to the same AMI.
	for _, arg := range []string{"granian-13-init", "ami-abc123"} {
		b.Reset()
		if err := RunShowAMIDetailCLI(context.Background(), &b, clients, cliImages(), arg, ui.FormatJSON); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		bdms, _ := got["block_devices"].([]any)
		if got["ami_id"] != "ami-abc123" || got["architecture"] != "x86_64" || got["ena_support"] != true || len(bdms) != 1 {
			t.Errorf("%s: got %v", arg, got)
		}
	}
	b.Reset()
	if err := RunShowAMIDetailCLI(context.Background(), &b, clients, cliImages(), "ami-abc123", ui.FormatText); err != nil || !strings.HasPrefix(b.String(), "AMI ami-abc123") {
		t.Errorf("text: err=%v %q", err, b.String())
	}
	usageErr(t, RunShowAMIDetailCLI(context.Background(), &b, nil, cliImages(), "ami-none", ui.FormatText), "ami-none")
}

func templateFake() *fakeEC2Client {
	v := launchTemplateVersionWithUserData(2, "#cloud-config")
	v.DefaultVersion = aws.Bool(false)
	return &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{v}}
}

func TestRunShowLaunchTemplateDetailCLI_DefaultVersionAndExplicitVersion(t *testing.T) {
	fake := templateFake()
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	var b bytes.Buffer
	if err := RunShowLaunchTemplateDetailCLI(context.Background(), &b, clients, cliTemplates(), "authors-tmpl", "", false, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if v := fake.lastDescribeLaunchTemplateVersionsInput.Versions; len(v) != 1 || v[0] != "$Default" {
		t.Errorf("no version asked for means $Default, sent %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if got["template_id"] != "lt-1" || got["name"] != "authors-tmpl" || got["version"] != float64(2) || got["image_id"] != "ami-1" {
		t.Errorf("got %v", got)
	}
	if err := RunShowLaunchTemplateDetailCLI(context.Background(), &b, clients, cliTemplates(), "lt-1", "v2", false, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if v := fake.lastDescribeLaunchTemplateVersionsInput.Versions; len(v) != 1 || v[0] != "2" {
		t.Errorf("\"v2\" is normalized to 2 as in the prompt, sent %v", v)
	}
}

func TestRunShowLaunchTemplateDetailCLI_VersionsList(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": templateFake()}
	var b bytes.Buffer
	if err := RunShowLaunchTemplateDetailCLI(context.Background(), &b, clients, cliTemplates(), "lt-1", "", true, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil || len(got) != 1 || got[0]["version"] != float64(2) || got[0]["default"] != false {
		t.Errorf("got %v err=%v", got, err)
	}
	b.Reset()
	if err := RunShowLaunchTemplateDetailCLI(context.Background(), &b, clients, cliTemplates(), "lt-1", "", true, ui.FormatText); err != nil || !strings.Contains(b.String(), "v2") {
		t.Errorf("text: %q err=%v", b.String(), err)
	}
	// A version and -versions together ask two different questions.
	usageErr(t, RunShowLaunchTemplateDetailCLI(context.Background(), &b, clients, cliTemplates(), "lt-1", "2", true, ui.FormatText), "-versions")
	usageErr(t, RunShowLaunchTemplateDetailCLI(context.Background(), &b, nil, cliTemplates(), "nope", "", false, ui.FormatText), "nope")
}

func cloudInitFake(raw string) *fakeEC2Client {
	return &fakeEC2Client{userDataValue: base64.StdEncoding.EncodeToString([]byte(raw))}
}

func TestRunExportCloudInitCLI_InstanceToStdoutIsRawYAML(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": cloudInitFake("#cloud-config\nx: 1\n")}
	var out, eout bytes.Buffer
	err := RunExportCloudInitCLI(context.Background(), &out, &eout, clients, nil, cliInstances(), cliImages(), "web-1", "", ComputeOptions{Format: ui.FormatText})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "#cloud-config\nx: 1\n" || eout.Len() != 0 {
		t.Errorf("stdout must be exactly the YAML; out=%q err=%q", out.String(), eout.String())
	}
}

func TestRunExportCloudInitCLI_JSONAndFile(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": cloudInitFake("#cloud-config\n")}
	var out, eout bytes.Buffer
	if err := RunExportCloudInitCLI(context.Background(), &out, &eout, clients, nil, cliInstances(), cliImages(), "i-abc123", "", ComputeOptions{Format: ui.FormatJSON}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got["id"] != "i-abc123" || got["kind"] != "instance" || got["user_data_set"] != true || got["user_data"] != "#cloud-config\n" {
		t.Errorf("got %v err=%v", got, err)
	}
	path := t.TempDir() + "/ci.yaml"
	out.Reset()
	if err := RunExportCloudInitCLI(context.Background(), &out, &eout, clients, nil, cliInstances(), cliImages(), "web-1", path, ComputeOptions{Format: ui.FormatText}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Saved to "+path) {
		t.Errorf("want the save notice, got %q", out.String())
	}
}

func TestRunExportCloudInitCLI_NoUserDataIsNotAnErrorAndStdoutStaysEmpty(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{}}
	var out, eout bytes.Buffer
	if err := RunExportCloudInitCLI(context.Background(), &out, &eout, clients, nil, cliInstances(), cliImages(), "web-1", "", ComputeOptions{Format: ui.FormatText}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || !strings.Contains(eout.String(), "No user-data") {
		t.Errorf("out=%q err=%q", out.String(), eout.String())
	}
}

// An AMI's cloud-init costs a temporary instance. Without the explicit option
// the form refuses before any AWS call; nil clients would panic on one.
func TestRunExportCloudInitCLI_AMIRequiresExplicitConsent(t *testing.T) {
	var out, eout bytes.Buffer
	err := RunExportCloudInitCLI(context.Background(), &out, &eout, nil, nil, cliInstances(), cliImages(), "ami-abc123", "", ComputeOptions{Format: ui.FormatText})
	usageErr(t, err, "-launch-temporary-instance")
	if out.Len() != 0 {
		t.Errorf("nothing may be written, got %q", out.String())
	}
}

func TestRunExportCloudInitCLI_UnknownTargetIsAUsageError(t *testing.T) {
	err := RunExportCloudInitCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, nil, nil, cliInstances(), cliImages(), "nope", "", ComputeOptions{Format: ui.FormatText})
	usageErr(t, err, "nope")
}

// Every read-only form must send reads only: after running them all against one
// fake, no mutating call may have been recorded.
func TestReadOnlyComputeForms_SendNoMutatingCall(t *testing.T) {
	fake := instanceFake()
	fake.userDataValue = base64.StdEncoding.EncodeToString([]byte("#cloud-config"))
	v := launchTemplateVersionWithUserData(2, "#cloud-config")
	fake.launchTemplateVersions = []types.LaunchTemplateVersion{v}
	fake.imageAvailableAfterCall = 1
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	ctx := context.Background()
	var w, e bytes.Buffer
	for _, f := range []ui.Format{ui.FormatText, ui.FormatJSON} {
		_ = RunShowInstanceDetailCLI(ctx, &w, clients, cliInstances(), "web-1", f)
		_ = RunShowAMIDetailCLI(ctx, &w, clients, cliImages(), "ami-abc123", f)
		_ = RunShowLaunchTemplateDetailCLI(ctx, &w, clients, cliTemplates(), "lt-1", "", false, f)
		_ = RunShowLaunchTemplateDetailCLI(ctx, &w, clients, cliTemplates(), "lt-1", "", true, f)
		_ = RunExportCloudInitCLI(ctx, &w, &e, clients, nil, cliInstances(), cliImages(), "web-1", "", ComputeOptions{Format: f})
	}
	if fake.lastRunInstancesInput != nil || fake.lastStartInstancesInput != nil || fake.lastStopInstancesInput != nil ||
		fake.lastTerminateInstancesInput != nil || fake.lastCreateTagsInput != nil || fake.lastDeleteTagsInput != nil {
		t.Error("a read-only form sent a mutating call (Run/Start/Stop/Terminate/CreateTags/DeleteTags)")
	}
	if fake.describeCalls == 0 {
		t.Error("the guard is vacuous: no Describe call was recorded")
	}
}

// The AMI path of the cloud-init form: the security group reaches the launch,
// and a source with no user-data is reported as none, not as an empty cloud-init.
func amiExportEnv(stdout string, defaultEgress []types.IpPermission) (map[string]awsclient.EC2API, map[string]awsclient.SSMAPI, *fakeEC2Client) {
	ec2Client := extractionFake(defaultEgress)
	ssm := &fakeSSMClient{onlineAfterCalls: 1, commandID: "cmd-1", finalStatus: ssmtypes.CommandInvocationStatusSuccess, stdout: stdout}
	return map[string]awsclient.EC2API{"us-west-2": ec2Client}, map[string]awsclient.SSMAPI{"us-west-2": ssm}, ec2Client
}

func TestRunExportCloudInitCLI_AMIUsesTheGivenSecurityGroup(t *testing.T) {
	ec2s, ssms, ec2Client := amiExportEnv("#cloud-config\nx: 1\n", nil)
	var out, eout bytes.Buffer
	opts := ComputeOptions{Format: ui.FormatText, LaunchTemporaryInstance: true, SecurityGroup: "sg-open"}
	if err := RunExportCloudInitCLI(context.Background(), &out, &eout, ec2s, ssms, cliInstances(), cliImages(), "ami-abc123", "", opts); err != nil {
		t.Fatal(err)
	}
	if got := ec2Client.lastRunInstancesInput.SecurityGroupIds; len(got) != 1 || got[0] != "sg-open" {
		t.Errorf("SecurityGroupIds = %v, want [sg-open]", got)
	}
	if out.String() != "#cloud-config\nx: 1\n" {
		t.Errorf("stdout must be the YAML alone, got %q", out.String())
	}
}

func TestRunExportCloudInitCLI_AMIClosedDefaultGroupIsAUsageErrorBeforeLaunch(t *testing.T) {
	ec2s, ssms, ec2Client := amiExportEnv("x", nil)
	opts := ComputeOptions{Format: ui.FormatText, LaunchTemporaryInstance: true}
	err := RunExportCloudInitCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, ec2s, ssms, cliInstances(), cliImages(), "ami-abc123", "", opts)
	usageErr(t, err, "cloud_init_extraction_security_group")
	if ec2Client.lastRunInstancesInput != nil {
		t.Error("nothing may be launched")
	}
}

func TestRunExportCloudInitCLI_AMIWithNoUserData(t *testing.T) {
	ec2s, ssms, _ := amiExportEnv("", egressAll())
	var out, eout bytes.Buffer
	if err := RunExportCloudInitCLI(context.Background(), &out, &eout, ec2s, ssms, cliInstances(), cliImages(), "ami-abc123", "", ComputeOptions{Format: ui.FormatText, LaunchTemporaryInstance: true}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || !strings.Contains(eout.String(), "No user-data was found in AMI ami-abc123") {
		t.Errorf("out=%q err=%q", out.String(), eout.String())
	}

	ec2s, ssms, _ = amiExportEnv("", egressAll())
	out.Reset()
	if err := RunExportCloudInitCLI(context.Background(), &out, &bytes.Buffer{}, ec2s, ssms, cliInstances(), cliImages(), "ami-abc123", "", ComputeOptions{Format: ui.FormatJSON, LaunchTemporaryInstance: true}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got["kind"] != "ami" || got["user_data_set"] != false || got["user_data"] != "" {
		t.Errorf("got %v err=%v", got, err)
	}
}
