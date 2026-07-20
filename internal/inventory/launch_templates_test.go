package inventory

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

func sdkLaunchTemplate(id, name string, defaultVersion, latestVersion int64, project, environment string) types.LaunchTemplate {
	var tags []types.Tag
	if project != "" {
		tags = append(tags, types.Tag{Key: aws.String("Project"), Value: aws.String(project)})
	}
	if environment != "" {
		tags = append(tags, types.Tag{Key: aws.String("Environment"), Value: aws.String(environment)})
	}
	return types.LaunchTemplate{
		LaunchTemplateId:     aws.String(id),
		LaunchTemplateName:   aws.String(name),
		DefaultVersionNumber: aws.Int64(defaultVersion),
		LatestVersionNumber:  aws.Int64(latestVersion),
		Tags:                 tags,
	}
}

func sortLaunchTemplates(templates []LaunchTemplate) {
	sort.Slice(templates, func(i, j int) bool { return templates[i].TemplateID < templates[j].TemplateID })
}

func TestListLaunchTemplates_AggregatesAcrossRegions(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{launchTemplates: []types.LaunchTemplate{
			sdkLaunchTemplate("lt-1", "rdm-app", 2, 3, "caltechauthors", "production"),
		}},
		"us-west-2": &fakeEC2Client{launchTemplates: []types.LaunchTemplate{
			sdkLaunchTemplate("lt-2", "rdm-worker", 1, 1, "caltechdata", "development"),
		}},
	}

	got, err := ListLaunchTemplates(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sortLaunchTemplates(got)

	want := []LaunchTemplate{
		{TemplateID: "lt-1", Name: "rdm-app", DefaultVersion: 2, LatestVersion: 3, Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{TemplateID: "lt-2", Name: "rdm-worker", DefaultVersion: 1, LatestVersion: 1, Region: "us-west-2", Project: "caltechdata", Environment: "development"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d templates, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListLaunchTemplates_UntaggedResourceHasEmptyFields(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{launchTemplates: []types.LaunchTemplate{
			{LaunchTemplateId: aws.String("lt-1"), LaunchTemplateName: aws.String("bare")},
		}},
	}
	got, err := ListLaunchTemplates(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Project != "" || got[0].Environment != "" {
		t.Fatalf("got %+v, want empty Project/Environment", got)
	}
}

func TestListLaunchTemplates_PropagatesError(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{describeLaunchTemplatesErr: errors.New("boom")},
	}
	_, err := ListLaunchTemplates(context.Background(), clients)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func sdkLaunchTemplateVersion(templateID string, versionNumber int64, isDefault bool, imdsRequired bool) types.LaunchTemplateVersion {
	httpTokens := types.LaunchTemplateHttpTokensStateOptional
	if imdsRequired {
		httpTokens = types.LaunchTemplateHttpTokensStateRequired
	}
	return types.LaunchTemplateVersion{
		LaunchTemplateId: aws.String(templateID),
		VersionNumber:    aws.Int64(versionNumber),
		DefaultVersion:   aws.Bool(isDefault),
		LaunchTemplateData: &types.ResponseLaunchTemplateData{
			ImageId:      aws.String("ami-1"),
			InstanceType: types.InstanceTypeT3Micro,
			KeyName:      aws.String("my-key"),
			IamInstanceProfile: &types.LaunchTemplateIamInstanceProfileSpecification{
				Name: aws.String("my-profile"),
			},
			MetadataOptions: &types.LaunchTemplateInstanceMetadataOptions{HttpTokens: httpTokens},
			NetworkInterfaces: []types.LaunchTemplateInstanceNetworkInterfaceSpecification{
				{SubnetId: aws.String("subnet-1"), Groups: []string{"sg-1", "sg-2"}},
			},
			TagSpecifications: []types.LaunchTemplateTagSpecification{
				{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{
					{Key: aws.String("Project"), Value: aws.String("caltechauthors")},
					{Key: aws.String("Environment"), Value: aws.String("production")},
				}},
			},
			UserData: aws.String("I2Nsb3VkLWNvbmZpZw=="),
		},
	}
}

func TestDescribeLaunchTemplateVersion_DecodesCuratedFields(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersion("lt-1", 2, true, true),
	}}

	got, err := DescribeLaunchTemplateVersion(context.Background(), fake, "lt-1", "$Default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := LaunchTemplateVersionDetail{
		TemplateID:         "lt-1",
		VersionNumber:      2,
		IsDefaultVersion:   true,
		ImageID:            "ami-1",
		InstanceType:       "t3.micro",
		KeyName:            "my-key",
		IAMInstanceProfile: "my-profile",
		SecurityGroupIDs:   []string{"sg-1", "sg-2"},
		SubnetID:           "subnet-1",
		UserData:           "I2Nsb3VkLWNvbmZpZw==",
		IMDSv2Required:     true,
		Project:            "caltechauthors",
		Environment:        "production",
	}
	if got.TemplateID != want.TemplateID || got.VersionNumber != want.VersionNumber ||
		got.IsDefaultVersion != want.IsDefaultVersion || got.ImageID != want.ImageID ||
		got.InstanceType != want.InstanceType || got.KeyName != want.KeyName ||
		got.IAMInstanceProfile != want.IAMInstanceProfile || got.SubnetID != want.SubnetID ||
		got.UserData != want.UserData || got.IMDSv2Required != want.IMDSv2Required ||
		got.Project != want.Project || got.Environment != want.Environment {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(got.SecurityGroupIDs) != 2 || got.SecurityGroupIDs[0] != "sg-1" || got.SecurityGroupIDs[1] != "sg-2" {
		t.Errorf("SecurityGroupIDs = %v, want [sg-1 sg-2]", got.SecurityGroupIDs)
	}

	in := fake.lastDescribeLaunchTemplateVersionsInput
	if aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplateId))
	}
	if len(in.Versions) != 1 || in.Versions[0] != "$Default" {
		t.Errorf("Versions = %v, want [$Default]", in.Versions)
	}
}

func TestDescribeLaunchTemplateVersion_FlagsMissingIMDSv2(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		sdkLaunchTemplateVersion("lt-1", 1, true, false),
	}}
	got, err := DescribeLaunchTemplateVersion(context.Background(), fake, "lt-1", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.IMDSv2Required {
		t.Error("IMDSv2Required = true, want false")
	}
}

func TestDescribeLaunchTemplateVersion_NotFound(t *testing.T) {
	fake := &fakeEC2Client{launchTemplateVersions: nil}
	_, err := DescribeLaunchTemplateVersion(context.Background(), fake, "lt-missing", "$Default")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDescribeLaunchTemplateVersion_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeLaunchTemplateVersionsErr: errors.New("boom")}
	_, err := DescribeLaunchTemplateVersion(context.Background(), fake, "lt-1", "$Default")
	if err == nil {
		t.Fatal("expected an error")
	}
}
