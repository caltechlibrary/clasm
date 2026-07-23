package workflow

import (
	"bytes"
	"context"
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

	data := buildRequestLaunchTemplateData(params)

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
	if len(data.TagSpecifications[0].Tags) != 3 {
		t.Errorf("Tags = %+v, want 3 entries", data.TagSpecifications[0].Tags)
	}
}

func TestBuildRequestLaunchTemplateData_NoIAMProfileOrTagsOmitsFields(t *testing.T) {
	data := buildRequestLaunchTemplateData(LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1"})
	if data.IamInstanceProfile != nil {
		t.Errorf("IamInstanceProfile = %+v, want nil", data.IamInstanceProfile)
	}
	if data.TagSpecifications != nil {
		t.Errorf("TagSpecifications = %+v, want nil", data.TagSpecifications)
	}
	if data.UserData != nil {
		t.Errorf("UserData = %v, want nil", aws.ToString(data.UserData))
	}
}

func TestBuildRequestLaunchTemplateData_SetsRootVolumeSize(t *testing.T) {
	// Same TODO.md bug as Launch (launch_execute_test.go): templates
	// created before this feature silently baked in the AMI's default
	// root volume size with no way to override it.
	data := buildRequestLaunchTemplateData(LaunchInstanceParams{
		ImageID:          "ami-1",
		SubnetID:         "subnet-1",
		RootDeviceName:   "/dev/xvda",
		RootVolumeSizeGB: 250,
	})
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
	data := buildRequestLaunchTemplateData(LaunchInstanceParams{ImageID: "ami-1", SubnetID: "subnet-1"})
	if data.BlockDeviceMappings != nil {
		t.Errorf("BlockDeviceMappings = %+v, want nil", data.BlockDeviceMappings)
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
