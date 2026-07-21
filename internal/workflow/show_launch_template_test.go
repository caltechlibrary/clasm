package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func sdkLaunchTemplateVersionDetail(versionNumber int64, isDefault, imdsRequired bool) types.LaunchTemplateVersion {
	httpTokens := types.LaunchTemplateHttpTokensStateOptional
	if imdsRequired {
		httpTokens = types.LaunchTemplateHttpTokensStateRequired
	}
	return types.LaunchTemplateVersion{
		LaunchTemplateId: aws.String("lt-1"),
		VersionNumber:    aws.Int64(versionNumber),
		DefaultVersion:   aws.Bool(isDefault),
		LaunchTemplateData: &types.ResponseLaunchTemplateData{
			ImageId:         aws.String("ami-1"),
			InstanceType:    types.InstanceTypeT3Micro,
			MetadataOptions: &types.LaunchTemplateInstanceMetadataOptions{HttpTokens: httpTokens},
		},
	}
}

func TestShowLaunchTemplate_DisplaysCuratedFieldsAndFlagsMissingIMDSv2(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(1, true, false),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 1}

	w, input, buf := newPipeEditor("1\n" + // Show version detail
		"\n") // accept the pre-filled $Default
	err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"lt-1", "rdm-app", "ami-1", "t3.micro", "NOT required"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	in := fake.lastDescribeLaunchTemplateVersionsInput
	if aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplateId))
	}
	if len(in.Versions) != 1 || in.Versions[0] != "$Default" {
		t.Errorf("Versions = %v, want [$Default] (the pre-filled default, unedited)", in.Versions)
	}
}

func TestShowLaunchTemplate_DisplaysRootVolumeSize(t *testing.T) {
	v := sdkLaunchTemplateVersionDetail(1, true, true)
	v.LaunchTemplateData.BlockDeviceMappings = []types.LaunchTemplateBlockDeviceMapping{
		{DeviceName: aws.String("/dev/xvda"), Ebs: &types.LaunchTemplateEbsBlockDevice{VolumeSize: aws.Int32(250)}},
	}
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{v}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 1}

	w, input, buf := newPipeEditor("1\n" + "\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "250 GB") {
		t.Errorf("output missing root volume size:\n%s", out)
	}
}

func TestShowLaunchTemplate_NoRootVolumeOverrideShowsAMIDefault(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(1, true, true),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 1}

	w, input, buf := newPipeEditor("1\n" + "\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "AMI default") {
		t.Errorf("output missing the AMI-default fallback for an unset root volume size:\n%s", out)
	}
}

func TestShowLaunchTemplate_IMDSv2RequiredShowsNoFlag(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(2, true, true),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 2}

	w, input, buf := newPipeEditor("1\n" + "\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "NOT required") {
		t.Errorf("output flags IMDSv2 as missing when it's required:\n%s", out)
	}
	if !strings.Contains(out, "IMDSv2:               required") {
		t.Errorf("output missing the IMDSv2-required line:\n%s", out)
	}
}

// TestShowLaunchTemplate_AcceptsVPrefixedVersion reproduces a bug found
// during real-AWS testing 2026-07-20 (clasm-debug-20260720-132204.jsonl):
// the operator typed "v1" at the version prompt -- a natural thing to
// type, since launchTemplateLabel's own display format is "default v2"
// -- but AWS's DescribeLaunchTemplateVersions rejected it outright:
// "Invalid launch template version: either '$Default', '$Latest', or a
// numeric version are allowed." normalizeVersionSelector must strip a
// leading v/V before the value ever reaches the SDK call.
func TestShowLaunchTemplate_AcceptsVPrefixedVersion(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(1, true, true),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 1}

	w, input, buf := newPipeEditor("1\n" + "v1\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	in := fake.lastDescribeLaunchTemplateVersionsInput
	if len(in.Versions) != 1 || in.Versions[0] != "1" {
		t.Errorf("Versions = %v, want [1] (the \"v\" prefix must be stripped before calling AWS)", in.Versions)
	}
}

func TestNormalizeVersionSelector(t *testing.T) {
	cases := map[string]string{
		"v1":       "1",
		"V23":      "23",
		"1":        "1",
		"$Default": "$Default",
		"$Latest":  "$Latest",
		"  v2  ":   "2",
		"velvet":   "velvet", // "v" prefix but not followed by only digits -- left alone
		"v":        "v",      // just "v", nothing to strip
		"":         "",
	}
	for in, want := range cases {
		if got := normalizeVersionSelector(in); got != want {
			t.Errorf("normalizeVersionSelector(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLaunchTemplateVersionRows(t *testing.T) {
	rows := launchTemplateVersionRows([]inventory.LaunchTemplateVersionSummary{
		{VersionNumber: 1, CreateTime: "2026-07-13T10:00:00Z", IsDefaultVersion: false},
		{VersionNumber: 2, CreateTime: "2026-07-20T10:00:00Z", IsDefaultVersion: true},
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !strings.Contains(rows[0], "v1") || strings.Contains(rows[0], "(default)") {
		t.Errorf("rows[0] = %q, want v1 without (default)", rows[0])
	}
	if !strings.Contains(rows[1], "v2") || !strings.Contains(rows[1], "(default)") {
		t.Errorf("rows[1] = %q, want v2 with (default)", rows[1])
	}
}

func TestShowLaunchTemplate_ListAllVersions(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(1, false, true),
		sdkLaunchTemplateVersionDetail(2, true, true),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	w, input, buf := newPipeEditor("2\n") // List all versions
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "v1") || !strings.Contains(out, "v2") || !strings.Contains(out, "(default)") {
		t.Errorf("output missing version list content:\n%s", out)
	}
}

func TestShowLaunchTemplate_DiffTwoVersions(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersionsByVersion: map[string]types.LaunchTemplateVersion{
		"1": launchTemplateVersionWithUserData(1, "#cloud-config\nold line\n"),
		"2": launchTemplateVersionWithUserData(2, "#cloud-config\nnew line\n"),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	w, input, buf := newPipeEditor("3\n" + // Diff two versions
		"1\n" + // first version
		"2\n") // second version
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "old line") || !strings.Contains(out, "new line") {
		t.Errorf("expected the diff to show both versions' content, got:\n%s", out)
	}
}

func TestShowLaunchTemplate_DiffTwoVersions_IdenticalReportsNoDifference(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersionsByVersion: map[string]types.LaunchTemplateVersion{
		"1": launchTemplateVersionWithUserData(1, "#cloud-config\nsame\n"),
		"2": launchTemplateVersionWithUserData(2, "#cloud-config\nsame\n"),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	w, input, buf := newPipeEditor("3\n" + "1\n" + "2\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "identical") {
		t.Errorf("expected an identical-content message, got:\n%s", out)
	}
}

func TestShowLaunchTemplate_UnknownRegionErrors(t *testing.T) {
	clients := map[string]awsclient.EC2API{}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	w, input, buf := newPipeEditor("\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err == nil {
		t.Fatal("expected an error for a region with no configured client")
	}
}
