package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestBuildRequestLaunchTemplateData_SetsIMDSv2RequiredAndSubnetViaNetworkInterface(t *testing.T) {
	params := LaunchInstanceParams{
		ImageID:            "ami-1",
		InstanceType:       "t3.micro",
		KeyName:            "my-key",
		SecurityGroupIDs:   []string{"sg-1", "sg-2"},
		SubnetID:           "subnet-1",
		IAMInstanceProfile: "my-profile",
		UserData:           "#cloud-config",
		Tags:               map[string]string{"Name": "web", "Project": "caltechauthors", "Environment": "production"},
	}

	data, err := buildRequestLaunchTemplateData(params, "rdm-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if aws.ToString(data.ImageId) != "ami-1" {
		t.Errorf("ImageId = %q, want ami-1", aws.ToString(data.ImageId))
	}
	if data.MetadataOptions == nil || data.MetadataOptions.HttpTokens != types.LaunchTemplateHttpTokensStateRequired {
		t.Errorf("MetadataOptions = %v, want HttpTokens: required", data.MetadataOptions)
	}
	if len(data.SecurityGroupIds) != 0 {
		t.Errorf("SecurityGroupIds (top-level) = %v, want empty -- security groups must live in NetworkInterfaces once it's used", data.SecurityGroupIds)
	}
	if len(data.NetworkInterfaces) != 1 {
		t.Fatalf("NetworkInterfaces = %v, want exactly one entry", data.NetworkInterfaces)
	}
	ni := data.NetworkInterfaces[0]
	if aws.ToString(ni.SubnetId) != "subnet-1" {
		t.Errorf("NetworkInterfaces[0].SubnetId = %q, want subnet-1", aws.ToString(ni.SubnetId))
	}
	if len(ni.Groups) != 2 {
		t.Errorf("NetworkInterfaces[0].Groups = %v, want 2 entries", ni.Groups)
	}
	if data.IamInstanceProfile == nil || aws.ToString(data.IamInstanceProfile.Name) != "my-profile" {
		t.Errorf("IamInstanceProfile = %v, want Name=my-profile", data.IamInstanceProfile)
	}
	gotUserData, err := decodeUserData(aws.ToString(data.UserData))
	if err != nil {
		t.Fatalf("unexpected error decoding UserData: %v", err)
	}
	if gotUserData != "#cloud-config" {
		t.Errorf("UserData decodes to %q, want %q", gotUserData, "#cloud-config")
	}
	if len(data.TagSpecifications) != 1 || data.TagSpecifications[0].ResourceType != types.ResourceTypeInstance {
		t.Fatalf("TagSpecifications = %+v, want one instance-scoped spec", data.TagSpecifications)
	}
	if len(data.TagSpecifications[0].Tags) != 4 {
		t.Errorf("Tags = %+v, want the 3 given plus templateName", data.TagSpecifications[0].Tags)
	}
}

func TestBuildRequestLaunchTemplateData_NoIAMProfileOrTagsOmitsFields(t *testing.T) {
	data, err := buildRequestLaunchTemplateData(LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1"}, "rdm-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.IamInstanceProfile != nil {
		t.Errorf("IamInstanceProfile = %+v, want nil", data.IamInstanceProfile)
	}
	// No other tags, but the template's name is always recorded (GitHub issue #1).
	if got := instanceTags(data); len(got) != 1 || got[launchTemplateNameTagKey] != "rdm-app" {
		t.Errorf("instance tags = %v, want only templateName=rdm-app", got)
	}
	if data.UserData != nil {
		t.Errorf("UserData = %v, want nil", aws.ToString(data.UserData))
	}
}

func TestBuildRequestLaunchTemplateData_SetsRootVolumeSize(t *testing.T) {
	// Same TODO.md bug as Launch (launch_execute_test.go): templates
	// created before this feature silently baked in the AMI's default
	// root volume size with no way to override it.
	data, err := buildRequestLaunchTemplateData(LaunchInstanceParams{
		ImageID:          "ami-1",
		SubnetID:         "subnet-1",
		RootDeviceName:   "/dev/xvda",
		RootVolumeSizeGB: 250,
	}, "rdm-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.BlockDeviceMappings) != 1 {
		t.Fatalf("BlockDeviceMappings = %+v, want exactly one entry", data.BlockDeviceMappings)
	}
	bdm := data.BlockDeviceMappings[0]
	if aws.ToString(bdm.DeviceName) != "/dev/xvda" {
		t.Errorf("DeviceName = %q, want %q", aws.ToString(bdm.DeviceName), "/dev/xvda")
	}
	if bdm.Ebs == nil || aws.ToInt32(bdm.Ebs.VolumeSize) != 250 {
		t.Errorf("Ebs.VolumeSize = %v, want 250", bdm.Ebs)
	}
}

func TestBuildRequestLaunchTemplateData_OmitsBlockDeviceMappingsWhenSizeNotSet(t *testing.T) {
	data, err := buildRequestLaunchTemplateData(LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1"}, "rdm-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.BlockDeviceMappings != nil {
		t.Errorf("BlockDeviceMappings = %+v, want nil", data.BlockDeviceMappings)
	}
}

func TestBuildRequestLaunchTemplateData_PropagatesEncodeUserDataError(t *testing.T) {
	_, err := buildRequestLaunchTemplateData(LaunchInstanceParams{
		ImageID:  "ami-1",
		SubnetID: "subnet-1",
		UserData: string(pseudoRandomBytes(20000)),
	}, "rdm-app")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestCreateLaunchTemplateFromCloudInit_HappyPath(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n" +
		"1\n" + // instance type: t3.micro
		"\n" + // Root EBS volume size in GB (blank -> AMI default of 0 in this fake)
		"new\n" + // key pair: create new (free-text fallback forced via describeKeyPairsErr)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"\n" + // IAM profile (blank -- free-text fallback via fakeIAMClientNoProfiles)
		"caltechauthors\n" +
		"production\n" +
		"rdm-app\n" + // launch template name
		"y\n" // confirm create

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured, createLaunchTemplateID: "lt-1"}

	err := createLaunchTemplateFromCloudInit(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}, fakeIAMClientNoProfiles(), "#cloud-config", image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	in := ec2Client.lastCreateLaunchTemplateInput
	if in == nil {
		t.Fatal("CreateLaunchTemplate was never called")
	}
	if aws.ToString(in.LaunchTemplateName) != "rdm-app" {
		t.Errorf("LaunchTemplateName = %q, want rdm-app", aws.ToString(in.LaunchTemplateName))
	}
	if len(in.TagSpecifications) != 1 || in.TagSpecifications[0].ResourceType != types.ResourceTypeLaunchTemplate {
		t.Fatalf("TagSpecifications = %+v, want one launch-template-scoped spec", in.TagSpecifications)
	}
	if in.LaunchTemplateData.MetadataOptions == nil || in.LaunchTemplateData.MetadataOptions.HttpTokens != types.LaunchTemplateHttpTokensStateRequired {
		t.Error("expected IMDSv2 to be required unconditionally on a newly created template")
	}
}

func TestCreateLaunchTemplateFromCloudInit_DeclinedConfirmationDoesNotCreate(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Region: "us-east-1"}
	input := "web\n" +
		"1\n" +
		"\n" + // Root EBS volume size in GB (blank -> AMI default of 0 in this fake)
		"new\n" +
		"my-key\n" +
		"sg-1\n" +
		"subnet-1\n" +
		"\n" +
		"caltechauthors\n" +
		"production\n" +
		"rdm-app\n" +
		"n\n" // decline

	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured}

	err := createLaunchTemplateFromCloudInit(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}, fakeIAMClientNoProfiles(), "#cloud-config", image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Client.lastCreateLaunchTemplateInput != nil {
		t.Error("CreateLaunchTemplate was called despite a declined confirmation")
	}
}

// instanceTags flattens data's instance-scoped tag spec into a map.
func instanceTags(data *types.RequestLaunchTemplateData) map[string]string {
	got := map[string]string{}
	for _, spec := range data.TagSpecifications {
		if spec.ResourceType != types.ResourceTypeInstance {
			continue
		}
		for _, tg := range spec.Tags {
			got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
		}
	}
	return got
}

// GitHub issue #1 (Tom): the template's name goes into the template's own
// instance tag spec, so every instance launched from it -- by clasm, the
// console, the CLI or an Auto Scaling group -- carries the name of the template
// it came from, next to AWS's aws:ec2launchtemplate:id. A template named for the
// role its instances will grow into (caltechauthors-v14) says so on each one.
func TestBuildRequestLaunchTemplateData_TagsInstancesWithTheTemplateName(t *testing.T) {
	params := LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1", Tags: map[string]string{"Name": "web", "Project": "caltechauthors"}}
	data, err := buildRequestLaunchTemplateData(params, "caltechauthors-v14")
	if err != nil {
		t.Fatal(err)
	}
	got := instanceTags(data)
	if got["templateName"] != "caltechauthors-v14" || got["Name"] != "web" || got["Project"] != "caltechauthors" || len(got) != 3 {
		t.Errorf("instance tags = %v", got)
	}
	if len(data.TagSpecifications) != 1 {
		t.Errorf("want a single instance tag spec, got %+v", data.TagSpecifications)
	}
}

// The tag is built into a copy: params.Tags also tags the template resource
// itself, which is not given this tag.
func TestBuildRequestLaunchTemplateData_DoesNotMutateTheCallersTags(t *testing.T) {
	params := LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1", Tags: map[string]string{"Name": "web"}}
	if _, err := buildRequestLaunchTemplateData(params, "rdm-app"); err != nil {
		t.Fatal(err)
	}
	if _, leaked := params.Tags[launchTemplateNameTagKey]; leaked || len(params.Tags) != 1 {
		t.Errorf("params.Tags was modified: %v", params.Tags)
	}
}

// The name is the template's identity and immutable: it beats a stale or
// hand-typed value of the same key.
func TestBuildRequestLaunchTemplateData_TheTemplateNameWinsOverAnExistingTag(t *testing.T) {
	params := LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1", Tags: map[string]string{"templateName": "something-else"}}
	data, err := buildRequestLaunchTemplateData(params, "rdm-app")
	if err != nil {
		t.Fatal(err)
	}
	if got := instanceTags(data); got["templateName"] != "rdm-app" {
		t.Errorf("tag = %q, want rdm-app", got["templateName"])
	}
}

func TestBuildRequestLaunchTemplateData_NoNameAddsNoTag(t *testing.T) {
	data, err := buildRequestLaunchTemplateData(LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if data.TagSpecifications != nil {
		t.Errorf("want no tag spec, got %+v", data.TagSpecifications)
	}
}

func TestTemplateNameTagKey_IsNotInTheReservedNamespace(t *testing.T) {
	if strings.HasPrefix(strings.ToLower(launchTemplateNameTagKey), "aws:") {
		t.Errorf("tag key %q is in the reserved aws: namespace, which EC2 rejects", launchTemplateNameTagKey)
	}
	if launchTemplateNameTagKey != "templateName" {
		t.Errorf("tag key = %q; the documented key is templateName", launchTemplateNameTagKey)
	}
}

// End to end through the creation flow: the name typed at the prompt reaches the
// template's instance tag spec, and the template resource's own tags are the
// ones it always had.
func TestCreateLaunchTemplateFromCloudInit_TagsInstancesWithTheTypedName(t *testing.T) {
	image := inventory.Image{ImageID: "ami-1", Name: "base", Region: "us-east-1"}
	input := "web\n1\n\nnew\nmy-key\nsg-1\nsubnet-1\n\ncaltechauthors\nproduction\ncaltechauthors-v14\ny\n"
	var buf bytes.Buffer
	ec2Client := &fakeEC2Client{describeKeyPairsErr: errNoKeyPairsConfigured, createLaunchTemplateID: "lt-1"}
	err := createLaunchTemplateFromCloudInit(context.Background(), &buf, map[string]awsclient.EC2API{"us-east-1": ec2Client}, map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}, fakeIAMClientNoProfiles(), "#cloud-config", image, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatal(err)
	}
	in := ec2Client.lastCreateLaunchTemplateInput
	if got := instanceTags(in.LaunchTemplateData); got["templateName"] != "caltechauthors-v14" {
		t.Errorf("instance tags = %v, want templateName=caltechauthors-v14", got)
	}
	for _, spec := range in.TagSpecifications { // the template resource's own tags
		for _, tg := range spec.Tags {
			if aws.ToString(tg.Key) == "templateName" {
				t.Errorf("the template resource itself should not carry the tag: %+v", spec)
			}
		}
	}
}
