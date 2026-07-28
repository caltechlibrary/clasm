package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestImagesForRegionAndArchitecture_FiltersOnBothDimensions(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Region: "us-east-1", Architecture: "x86_64"},
		{ImageID: "ami-2", Region: "us-east-1", Architecture: "arm64"},
		{ImageID: "ami-3", Region: "us-west-2", Architecture: "arm64"},
	}

	got := imagesForRegionAndArchitecture(images, "us-east-1", "arm64")
	if len(got) != 1 || got[0].ImageID != "ami-2" {
		t.Errorf("got %+v, want just ami-2", got)
	}
}

func TestImagesForRegionAndArchitecture_NoMatches(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Region: "us-east-1", Architecture: "x86_64"},
	}
	got := imagesForRegionAndArchitecture(images, "us-east-1", "arm64")
	if len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}

func TestModifyLaunchTemplateVersion_SetsImageInstanceTypeAndBlockDeviceMapping(t *testing.T) {
	fake := &fakeEC2Client{createLaunchTemplateVersionNumber: 3}
	got, err := modifyLaunchTemplateVersion(context.Background(), fake, "lt-1", "2", "m5.large", "ami-new", "/dev/xvda", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Errorf("got version %d, want 3", got)
	}
	in := fake.lastCreateLaunchTemplateVersionInput
	if aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplateId))
	}
	if aws.ToString(in.SourceVersion) != "2" {
		t.Errorf("SourceVersion = %q, want 2", aws.ToString(in.SourceVersion))
	}
	if aws.ToString(in.LaunchTemplateData.ImageId) != "ami-new" {
		t.Errorf("ImageId = %q, want ami-new", aws.ToString(in.LaunchTemplateData.ImageId))
	}
	if string(in.LaunchTemplateData.InstanceType) != "m5.large" {
		t.Errorf("InstanceType = %q, want m5.large", in.LaunchTemplateData.InstanceType)
	}
	if len(in.LaunchTemplateData.BlockDeviceMappings) != 1 {
		t.Fatalf("BlockDeviceMappings = %+v, want exactly one entry", in.LaunchTemplateData.BlockDeviceMappings)
	}
	bdm := in.LaunchTemplateData.BlockDeviceMappings[0]
	if aws.ToString(bdm.DeviceName) != "/dev/xvda" {
		t.Errorf("DeviceName = %q, want /dev/xvda", aws.ToString(bdm.DeviceName))
	}
	if bdm.Ebs == nil || aws.ToInt32(bdm.Ebs.VolumeSize) != 100 {
		t.Errorf("Ebs.VolumeSize = %v, want 100", bdm.Ebs)
	}
	if in.LaunchTemplateData.UserData != nil {
		t.Error("expected UserData to be left unset -- only ImageId/InstanceType/BlockDeviceMappings should be overridden, everything else inherited via SourceVersion")
	}
}

func launchTemplateVersionForModify(versionNumber int64, instanceType, imageID string, rootVolumeSizeGB int32) types.LaunchTemplateVersion {
	var bdms []types.LaunchTemplateBlockDeviceMapping
	if rootVolumeSizeGB > 0 {
		bdms = []types.LaunchTemplateBlockDeviceMapping{{
			DeviceName: aws.String("/dev/xvda"),
			Ebs:        &types.LaunchTemplateEbsBlockDevice{VolumeSize: aws.Int32(rootVolumeSizeGB)},
		}}
	}
	return types.LaunchTemplateVersion{
		LaunchTemplateId: aws.String("lt-1"),
		VersionNumber:    aws.Int64(versionNumber),
		DefaultVersion:   aws.Bool(true),
		LaunchTemplateData: &types.ResponseLaunchTemplateData{
			ImageId:             aws.String(imageID),
			InstanceType:        types.InstanceType(instanceType),
			BlockDeviceMappings: bdms,
		},
	}
}

func TestModifyLaunchTemplateSize_NoOpWhenNothingChanged(t *testing.T) {
	fake := &fakeEC2Client{
		launchTemplateVersions:       []types.LaunchTemplateVersion{launchTemplateVersionForModify(1, "t3.large", "ami-1", 20)},
		describeImagesRootDeviceName: "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}},
		},
		describeImagesArchitecture: "x86_64",
		instanceTypeArchitectures:  map[string]string{"t3.large": "x86_64"},
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "\n" + // accept pre-filled $Default
		"4\n" + // Select an instance type -> curated entry 4 (t3.large, same as current)
		"20\n" // same root volume size
	var buf bytes.Buffer
	err := modifyLaunchTemplateSize(context.Background(), &buf, clients, lt, nil, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No changes") {
		t.Errorf("expected a no-op message, got:\n%s", buf.String())
	}
	if fake.lastCreateLaunchTemplateVersionInput != nil {
		t.Error("CreateLaunchTemplateVersion was called despite no changes")
	}
}

func TestModifyLaunchTemplateSize_SameArchitectureNeverPicksNewAMI(t *testing.T) {
	fake := &fakeEC2Client{
		launchTemplateVersions:            []types.LaunchTemplateVersion{launchTemplateVersionForModify(1, "t3.large", "ami-1", 20)},
		describeImagesRootDeviceName:      "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}}},
		describeImagesArchitecture:        "x86_64",
		instanceTypeArchitectures:         map[string]string{"t3.large": "x86_64", "m5.large": "x86_64"},
		createLaunchTemplateVersionNumber: 2,
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	pickCalled := false
	orig := pickNewLaunchTemplateAMIFunc
	defer func() { pickNewLaunchTemplateAMIFunc = orig }()
	pickNewLaunchTemplateAMIFunc = func(ctx context.Context, title, description string, images []inventory.Image) (inventory.Image, error) {
		pickCalled = true
		return inventory.Image{}, errors.New("should not be called")
	}

	input := "\n" +
		"6\n" + // curated entry 6 (m5.large)
		"20\n" +
		"y\n"
	var buf bytes.Buffer
	err := modifyLaunchTemplateSize(context.Background(), &buf, clients, lt, nil, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pickCalled {
		t.Error("pickNewLaunchTemplateAMIFunc was called despite a same-architecture instance type change")
	}
	if fake.lastCreateLaunchTemplateVersionInput == nil {
		t.Fatal("CreateLaunchTemplateVersion was never called")
	}
	if aws.ToString(fake.lastCreateLaunchTemplateVersionInput.LaunchTemplateData.ImageId) != "ami-1" {
		t.Errorf("ImageId = %q, want unchanged ami-1", aws.ToString(fake.lastCreateLaunchTemplateVersionInput.LaunchTemplateData.ImageId))
	}
}

func TestModifyLaunchTemplateSize_ArchitectureMismatchPicksNewAMI(t *testing.T) {
	fake := &fakeEC2Client{
		launchTemplateVersions:            []types.LaunchTemplateVersion{launchTemplateVersionForModify(1, "t4g.large", "ami-arm", 20)},
		describeImagesRootDeviceName:      "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}}},
		describeImagesArchitecture:        "arm64",
		instanceTypeArchitectures:         map[string]string{"t4g.large": "arm64", "m5.large": "x86_64"},
		createLaunchTemplateVersionNumber: 2,
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}
	images := []inventory.Image{{ImageID: "ami-x86", Region: "us-east-1", Architecture: "x86_64"}}

	var gotCandidates []inventory.Image
	orig := pickNewLaunchTemplateAMIFunc
	defer func() { pickNewLaunchTemplateAMIFunc = orig }()
	pickNewLaunchTemplateAMIFunc = func(ctx context.Context, title, description string, candidates []inventory.Image) (inventory.Image, error) {
		gotCandidates = candidates
		return inventory.Image{ImageID: "ami-x86", Region: "us-east-1", Architecture: "x86_64"}, nil
	}

	input := "\n" +
		"6\n" + // curated entry 6 (m5.large)
		"20\n" +
		"y\n"
	var buf bytes.Buffer
	err := modifyLaunchTemplateSize(context.Background(), &buf, clients, lt, images, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotCandidates) != 1 || gotCandidates[0].ImageID != "ami-x86" {
		t.Errorf("candidates passed to pickNewLaunchTemplateAMIFunc = %+v, want just ami-x86", gotCandidates)
	}
	in := fake.lastCreateLaunchTemplateVersionInput
	if in == nil {
		t.Fatal("CreateLaunchTemplateVersion was never called")
	}
	if aws.ToString(in.LaunchTemplateData.ImageId) != "ami-x86" {
		t.Errorf("ImageId = %q, want ami-x86", aws.ToString(in.LaunchTemplateData.ImageId))
	}
}

func TestModifyLaunchTemplateSize_NoCompatibleAMIFailsLoud(t *testing.T) {
	fake := &fakeEC2Client{
		launchTemplateVersions:            []types.LaunchTemplateVersion{launchTemplateVersionForModify(1, "t4g.large", "ami-arm", 20)},
		describeImagesRootDeviceName:      "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}}},
		describeImagesArchitecture:        "arm64",
		instanceTypeArchitectures:         map[string]string{"t4g.large": "arm64", "m5.large": "x86_64"},
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	pickCalled := false
	orig := pickNewLaunchTemplateAMIFunc
	defer func() { pickNewLaunchTemplateAMIFunc = orig }()
	pickNewLaunchTemplateAMIFunc = func(ctx context.Context, title, description string, images []inventory.Image) (inventory.Image, error) {
		pickCalled = true
		return inventory.Image{}, nil
	}

	input := "\n" + "6\n" // curated entry 6 (m5.large)
	var buf bytes.Buffer
	err := modifyLaunchTemplateSize(context.Background(), &buf, clients, lt, nil, newHuhAccessibleInput(input), &buf)
	if err == nil {
		t.Fatal("expected an error")
	}
	if pickCalled {
		t.Error("pickNewLaunchTemplateAMIFunc should not be called when there are no compatible candidates")
	}
	if fake.lastCreateLaunchTemplateVersionInput != nil {
		t.Error("CreateLaunchTemplateVersion was called despite no compatible AMI")
	}
}

func TestModifyLaunchTemplateSize_DeclinedConfirmationDoesNotCreateVersion(t *testing.T) {
	fake := &fakeEC2Client{
		launchTemplateVersions:            []types.LaunchTemplateVersion{launchTemplateVersionForModify(1, "t3.large", "ami-1", 20)},
		describeImagesRootDeviceName:      "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}}},
		describeImagesArchitecture:        "x86_64",
		instanceTypeArchitectures:         map[string]string{"t3.large": "x86_64", "m5.large": "x86_64"},
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "\n" + "6\n" + "20\n" + "n\n" // curated entry 6 (m5.large)
	var buf bytes.Buffer
	err := modifyLaunchTemplateSize(context.Background(), &buf, clients, lt, nil, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateLaunchTemplateVersionInput != nil {
		t.Error("CreateLaunchTemplateVersion was called despite a declined confirmation")
	}
}
