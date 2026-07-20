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

	w, input, buf := newPipeEditor("\n") // accept the pre-filled $Default
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

func TestShowLaunchTemplate_IMDSv2RequiredShowsNoFlag(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersionDetail(2, true, true),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", DefaultVersion: 2}

	w, input, buf := newPipeEditor("\n")
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

func TestShowLaunchTemplate_UnknownRegionErrors(t *testing.T) {
	clients := map[string]awsclient.EC2API{}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	w, input, buf := newPipeEditor("\n")
	if err := showLaunchTemplate(context.Background(), w, clients, lt, input, buf); err == nil {
		t.Fatal("expected an error for a region with no configured client")
	}
}
