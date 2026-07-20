package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// fakeEC2Client embeds the (nil) EC2API interface so it satisfies
// awsclient.EC2API without stubbing every method.
type fakeEC2Client struct {
	awsclient.EC2API

	lastRunInstancesInput *ec2.RunInstancesInput
	runInstancesErr       error
	runInstancesID        string

	describeCalls    int
	runningAfterCall int // DescribeInstances reports running starting at this call number; 0 = never
	stoppedAfterCall int // DescribeInstances reports stopped starting at this call number; 0 = never
	describeErr      error
	publicIP         string
	// notFoundForCalls, if > 0, makes the first N DescribeInstances
	// calls return InvalidInstanceID.NotFound -- simulates the real
	// eventual-consistency window right after ec2:RunInstances returns
	// an instance ID that isn't immediately visible to
	// ec2:DescribeInstances (see launch_execute.go's
	// isInstanceNotYetVisible).
	notFoundForCalls int

	lastStartInstancesInput *ec2.StartInstancesInput
	startInstancesErr       error

	lastStopInstancesInput *ec2.StopInstancesInput
	stopInstancesErr       error

	lastTerminateInstancesInput *ec2.TerminateInstancesInput
	terminateInstancesErr       error

	blockDeviceMappings []types.InstanceBlockDeviceMapping // returned by DescribeInstances, for dry-run tests
	instanceTags        []types.Tag                        // returned by DescribeInstances, for Manage Tags tests

	describeImagesErr  error
	describeImagesTags []types.Tag // returned by DescribeImages, for Manage Tags tests
	// officialUbuntuImages, keyed by "name" filter value, backs
	// DescribeImages calls scoped to ubuntuAMIOwnerID (see
	// official_ubuntu_amis.go) -- a separate code path from the
	// ImageIds-based polling logic below, since the query shape differs.
	officialUbuntuImages    map[string][]types.Image
	describeUbuntuImagesErr error
	// imageNotFoundForCalls, if > 0, makes the first N DescribeImages
	// calls return InvalidAMIID.NotFound -- the AMI-side analog of
	// notFoundForCalls above (see create_ami_execute.go's
	// isImageNotYetVisible).
	imageNotFoundForCalls int

	lastCreateTagsInput *ec2.CreateTagsInput
	createTagsErr       error
	lastDeleteTagsInput *ec2.DeleteTagsInput
	deleteTagsErr       error

	describeImagesCalls     int
	imageAvailableAfterCall int // DescribeImages reports available starting at this call number; 0 = never
	imageFailedAfterCall    int // DescribeImages reports failed starting at this call number; 0 = never

	describeVolumesOutput []types.Volume
	describeVolumesErr    error

	lastCreateImageInput *ec2.CreateImageInput
	createImageErr       error
	createImageID        string

	lastDeregisterImageInput *ec2.DeregisterImageInput
	deregisterImageErr       error

	userDataValue               string // returned by DescribeInstanceAttribute; empty means "not set"
	describeInstanceAttrErr     error
	terminateInstancesCallCount int

	keyPairs                  []types.KeyPairInfo
	describeKeyPairsErr       error
	securityGroups            []types.SecurityGroup
	describeSecurityGroupsErr error
	subnets                   []types.Subnet
	describeSubnetsErr        error

	// instanceTypeOfferings maps instance type -> the Availability Zones
	// it's offered in, for DescribeInstanceTypeOfferings (see
	// instance_type_az_check.go). describeInstanceTypeOfferingsErr, if
	// set, makes every call fail regardless of filters.
	instanceTypeOfferings            map[string][]string
	describeInstanceTypeOfferingsErr error

	// enaRequiredInstanceTypes is the set of instance types
	// DescribeInstanceTypes reports as requiring ENA (see
	// instance_type_ena_check.go); any type not in this set is reported
	// as not requiring it. describeInstanceTypesErr, if set, makes every
	// call fail.
	enaRequiredInstanceTypes map[string]bool
	describeInstanceTypesErr error

	createKeyPairCalls       int
	createKeyPairErr         error
	createKeyPairErrOnce     bool // if true, only the first CreateKeyPair call errors
	lastCreateKeyPairInput   *ec2.CreateKeyPairInput
	createKeyPairKeyMaterial string

	importKeyPairCalls     int
	lastImportKeyPairInput *ec2.ImportKeyPairInput
	importKeyPairErr       error
	importKeyPairErrOnce   bool // if true, only the first ImportKeyPair call errors

	lastDeleteKeyPairInput *ec2.DeleteKeyPairInput
	deleteKeyPairErr       error

	// launchTemplates backs DescribeLaunchTemplates -- the template
	// resource's own listing/tags, distinct from launchTemplateVersions
	// below (a specific version's LaunchTemplateData). Used by
	// fetchLaunchTemplateTags (manage_tags.go).
	launchTemplates                         []types.LaunchTemplate
	describeLaunchTemplatesErr              error
	lastDescribeLaunchTemplatesInput        *ec2.DescribeLaunchTemplatesInput
	launchTemplateVersions                  []types.LaunchTemplateVersion
	describeLaunchTemplateVersionsErr       error
	lastDescribeLaunchTemplateVersionsInput *ec2.DescribeLaunchTemplateVersionsInput
	// launchTemplateVersionsByVersion, if set, resolves
	// DescribeLaunchTemplateVersions by the requested Versions[0]
	// selector instead of always returning the same fixed
	// launchTemplateVersions slice -- needed by tests that fetch two
	// different versions of the same template (Show Launch Template's
	// version-to-version diff) and need each call to return distinct
	// content.
	launchTemplateVersionsByVersion map[string]types.LaunchTemplateVersion

	lastCreateLaunchTemplateInput *ec2.CreateLaunchTemplateInput
	createLaunchTemplateErr       error
	createLaunchTemplateID        string

	lastCreateLaunchTemplateVersionInput *ec2.CreateLaunchTemplateVersionInput
	createLaunchTemplateVersionErr       error
	createLaunchTemplateVersionNumber    int64

	lastModifyLaunchTemplateInput *ec2.ModifyLaunchTemplateInput
	modifyLaunchTemplateErr       error

	lastDeleteLaunchTemplateInput *ec2.DeleteLaunchTemplateInput
	deleteLaunchTemplateErr       error

	lastDeleteLaunchTemplateVersionsInput *ec2.DeleteLaunchTemplateVersionsInput
	deleteLaunchTemplateVersionsErr       error
	// deleteLaunchTemplateVersionsUnsuccessful, if set, is returned as
	// DeleteLaunchTemplateVersionsOutput's UnsuccessfullyDeleted list --
	// every other version requested is reported as successfully deleted.
	deleteLaunchTemplateVersionsUnsuccessful []types.DeleteLaunchTemplateVersionsResponseErrorItem
}

func (f *fakeEC2Client) DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	f.lastDescribeLaunchTemplatesInput = params
	if f.describeLaunchTemplatesErr != nil {
		return nil, f.describeLaunchTemplatesErr
	}
	return &ec2.DescribeLaunchTemplatesOutput{LaunchTemplates: f.launchTemplates}, nil
}

func (f *fakeEC2Client) DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	f.lastDescribeLaunchTemplateVersionsInput = params
	if f.describeLaunchTemplateVersionsErr != nil {
		return nil, f.describeLaunchTemplateVersionsErr
	}
	if f.launchTemplateVersionsByVersion != nil && len(params.Versions) == 1 {
		if v, ok := f.launchTemplateVersionsByVersion[params.Versions[0]]; ok {
			return &ec2.DescribeLaunchTemplateVersionsOutput{LaunchTemplateVersions: []types.LaunchTemplateVersion{v}}, nil
		}
	}
	return &ec2.DescribeLaunchTemplateVersionsOutput{LaunchTemplateVersions: f.launchTemplateVersions}, nil
}

func (f *fakeEC2Client) CreateLaunchTemplate(ctx context.Context, params *ec2.CreateLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	f.lastCreateLaunchTemplateInput = params
	if f.createLaunchTemplateErr != nil {
		return nil, f.createLaunchTemplateErr
	}
	id := f.createLaunchTemplateID
	if id == "" {
		id = "lt-fake0123456789"
	}
	return &ec2.CreateLaunchTemplateOutput{LaunchTemplate: &types.LaunchTemplate{
		LaunchTemplateId:     aws.String(id),
		LaunchTemplateName:   params.LaunchTemplateName,
		DefaultVersionNumber: aws.Int64(1),
		LatestVersionNumber:  aws.Int64(1),
	}}, nil
}

func (f *fakeEC2Client) CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	f.lastCreateLaunchTemplateVersionInput = params
	if f.createLaunchTemplateVersionErr != nil {
		return nil, f.createLaunchTemplateVersionErr
	}
	n := f.createLaunchTemplateVersionNumber
	if n == 0 {
		n = 2
	}
	return &ec2.CreateLaunchTemplateVersionOutput{LaunchTemplateVersion: &types.LaunchTemplateVersion{
		LaunchTemplateId: params.LaunchTemplateId,
		VersionNumber:    aws.Int64(n),
	}}, nil
}

func (f *fakeEC2Client) ModifyLaunchTemplate(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
	f.lastModifyLaunchTemplateInput = params
	if f.modifyLaunchTemplateErr != nil {
		return nil, f.modifyLaunchTemplateErr
	}
	return &ec2.ModifyLaunchTemplateOutput{}, nil
}

func (f *fakeEC2Client) DeleteLaunchTemplate(ctx context.Context, params *ec2.DeleteLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error) {
	f.lastDeleteLaunchTemplateInput = params
	if f.deleteLaunchTemplateErr != nil {
		return nil, f.deleteLaunchTemplateErr
	}
	return &ec2.DeleteLaunchTemplateOutput{}, nil
}

func (f *fakeEC2Client) DeleteLaunchTemplateVersions(ctx context.Context, params *ec2.DeleteLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateVersionsOutput, error) {
	f.lastDeleteLaunchTemplateVersionsInput = params
	if f.deleteLaunchTemplateVersionsErr != nil {
		return nil, f.deleteLaunchTemplateVersionsErr
	}

	unsuccessful := map[string]bool{}
	for _, item := range f.deleteLaunchTemplateVersionsUnsuccessful {
		unsuccessful[fmt.Sprintf("%d", aws.ToInt64(item.VersionNumber))] = true
	}

	out := &ec2.DeleteLaunchTemplateVersionsOutput{UnsuccessfullyDeletedLaunchTemplateVersions: f.deleteLaunchTemplateVersionsUnsuccessful}
	for _, v := range params.Versions {
		if unsuccessful[v] {
			continue
		}
		out.SuccessfullyDeletedLaunchTemplateVersions = append(out.SuccessfullyDeletedLaunchTemplateVersions, types.DeleteLaunchTemplateVersionsResponseSuccessItem{
			VersionNumber: aws.Int64(mustParseInt64(v)),
		})
	}
	return out, nil
}

// mustParseInt64 parses a version-number string for the fake's
// success-list bookkeeping above -- test-only, so a parse failure
// (never expected: callers always pass digit strings) panics rather
// than threading an error through this helper.
func mustParseInt64(s string) int64 {
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		panic(err)
	}
	return n
}

func (f *fakeEC2Client) CreateKeyPair(ctx context.Context, params *ec2.CreateKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	f.createKeyPairCalls++
	f.lastCreateKeyPairInput = params
	if f.createKeyPairErr != nil && (!f.createKeyPairErrOnce || f.createKeyPairCalls == 1) {
		return nil, f.createKeyPairErr
	}
	material := f.createKeyPairKeyMaterial
	if material == "" {
		material = "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	}
	return &ec2.CreateKeyPairOutput{
		KeyName:     params.KeyName,
		KeyPairId:   aws.String("key-fake0123456789"),
		KeyMaterial: aws.String(material),
	}, nil
}

func (f *fakeEC2Client) DescribeKeyPairs(ctx context.Context, params *ec2.DescribeKeyPairsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	if f.describeKeyPairsErr != nil {
		return nil, f.describeKeyPairsErr
	}
	return &ec2.DescribeKeyPairsOutput{KeyPairs: f.keyPairs}, nil
}

func (f *fakeEC2Client) ImportKeyPair(ctx context.Context, params *ec2.ImportKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error) {
	f.importKeyPairCalls++
	f.lastImportKeyPairInput = params
	if f.importKeyPairErr != nil && (!f.importKeyPairErrOnce || f.importKeyPairCalls == 1) {
		return nil, f.importKeyPairErr
	}
	return &ec2.ImportKeyPairOutput{
		KeyName:   params.KeyName,
		KeyPairId: aws.String("key-fake0123456789"),
	}, nil
}

func (f *fakeEC2Client) DeleteKeyPair(ctx context.Context, params *ec2.DeleteKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.DeleteKeyPairOutput, error) {
	f.lastDeleteKeyPairInput = params
	if f.deleteKeyPairErr != nil {
		return nil, f.deleteKeyPairErr
	}
	return &ec2.DeleteKeyPairOutput{}, nil
}

func (f *fakeEC2Client) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	if f.describeSecurityGroupsErr != nil {
		return nil, f.describeSecurityGroupsErr
	}
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: f.securityGroups}, nil
}

func (f *fakeEC2Client) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if f.describeSubnetsErr != nil {
		return nil, f.describeSubnetsErr
	}
	return &ec2.DescribeSubnetsOutput{Subnets: f.subnets}, nil
}

// DescribeInstanceTypeOfferings filters f.instanceTypeOfferings by the
// "instance-type"/"location" filters present in params, mimicking the
// real API closely enough for instance_type_az_check.go's two query
// shapes: type+location (existence check) and type-only (listing AZs).
func (f *fakeEC2Client) DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	if f.describeInstanceTypeOfferingsErr != nil {
		return nil, f.describeInstanceTypeOfferingsErr
	}

	var wantType, wantLocation string
	for _, filt := range params.Filters {
		if len(filt.Values) == 0 {
			continue
		}
		switch aws.ToString(filt.Name) {
		case "instance-type":
			wantType = filt.Values[0]
		case "location":
			wantLocation = filt.Values[0]
		}
	}

	var offerings []types.InstanceTypeOffering
	for typ, azs := range f.instanceTypeOfferings {
		if wantType != "" && typ != wantType {
			continue
		}
		for _, az := range azs {
			if wantLocation != "" && az != wantLocation {
				continue
			}
			offerings = append(offerings, types.InstanceTypeOffering{
				InstanceType: types.InstanceType(typ),
				Location:     aws.String(az),
			})
		}
	}
	return &ec2.DescribeInstanceTypeOfferingsOutput{InstanceTypeOfferings: offerings}, nil
}

// DescribeInstanceTypes reports each requested type's NetworkInfo.EnaSupport
// as Required if it's in f.enaRequiredInstanceTypes, Unsupported otherwise.
func (f *fakeEC2Client) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if f.describeInstanceTypesErr != nil {
		return nil, f.describeInstanceTypesErr
	}

	infos := make([]types.InstanceTypeInfo, 0, len(params.InstanceTypes))
	for _, it := range params.InstanceTypes {
		ena := types.EnaSupportUnsupported
		if f.enaRequiredInstanceTypes[string(it)] {
			ena = types.EnaSupportRequired
		}
		infos = append(infos, types.InstanceTypeInfo{
			InstanceType: it,
			NetworkInfo:  &types.NetworkInfo{EnaSupport: ena},
		})
	}
	return &ec2.DescribeInstanceTypesOutput{InstanceTypes: infos}, nil
}

func (f *fakeEC2Client) DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	f.lastDeregisterImageInput = params
	if f.deregisterImageErr != nil {
		return nil, f.deregisterImageErr
	}
	return &ec2.DeregisterImageOutput{}, nil
}

func (f *fakeEC2Client) DescribeInstanceAttribute(ctx context.Context, params *ec2.DescribeInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceAttributeOutput, error) {
	if f.describeInstanceAttrErr != nil {
		return nil, f.describeInstanceAttrErr
	}
	out := &ec2.DescribeInstanceAttributeOutput{InstanceId: aws.String(aws.ToString(params.InstanceId))}
	if f.userDataValue != "" {
		out.UserData = &types.AttributeValue{Value: aws.String(f.userDataValue)}
	}
	return out, nil
}

func (f *fakeEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if len(params.Owners) == 1 && params.Owners[0] == ubuntuAMIOwnerID {
		if f.describeUbuntuImagesErr != nil {
			return nil, f.describeUbuntuImagesErr
		}
		var namePattern string
		for _, filt := range params.Filters {
			if aws.ToString(filt.Name) == "name" && len(filt.Values) > 0 {
				namePattern = filt.Values[0]
			}
		}
		return &ec2.DescribeImagesOutput{Images: f.officialUbuntuImages[namePattern]}, nil
	}

	f.describeImagesCalls++
	if f.imageNotFoundForCalls > 0 && f.describeImagesCalls <= f.imageNotFoundForCalls {
		return nil, &smithy.GenericAPIError{Code: "InvalidAMIID.NotFound", Message: "The image id does not exist"}
	}
	if f.describeImagesErr != nil {
		return nil, f.describeImagesErr
	}
	imageID := ""
	if len(params.ImageIds) > 0 {
		imageID = params.ImageIds[0]
	}
	state := types.ImageStatePending
	if f.imageAvailableAfterCall > 0 && f.describeImagesCalls >= f.imageAvailableAfterCall {
		state = types.ImageStateAvailable
	}
	if f.imageFailedAfterCall > 0 && f.describeImagesCalls >= f.imageFailedAfterCall {
		state = types.ImageStateFailed
	}
	return &ec2.DescribeImagesOutput{Images: []types.Image{{ImageId: aws.String(imageID), Tags: f.describeImagesTags, State: state}}}, nil
}

func (f *fakeEC2Client) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if f.describeVolumesErr != nil {
		return nil, f.describeVolumesErr
	}
	return &ec2.DescribeVolumesOutput{Volumes: f.describeVolumesOutput}, nil
}

func (f *fakeEC2Client) CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	f.lastCreateImageInput = params
	if f.createImageErr != nil {
		return nil, f.createImageErr
	}
	return &ec2.CreateImageOutput{ImageId: aws.String(f.createImageID)}, nil
}

func (f *fakeEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	f.lastCreateTagsInput = params
	if f.createTagsErr != nil {
		return nil, f.createTagsErr
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (f *fakeEC2Client) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	f.lastDeleteTagsInput = params
	if f.deleteTagsErr != nil {
		return nil, f.deleteTagsErr
	}
	return &ec2.DeleteTagsOutput{}, nil
}

func (f *fakeEC2Client) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	f.terminateInstancesCallCount++
	f.lastTerminateInstancesInput = params
	if f.terminateInstancesErr != nil {
		return nil, f.terminateInstancesErr
	}
	return &ec2.TerminateInstancesOutput{}, nil
}

func (f *fakeEC2Client) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.lastStartInstancesInput = params
	if f.startInstancesErr != nil {
		return nil, f.startInstancesErr
	}
	return &ec2.StartInstancesOutput{}, nil
}

func (f *fakeEC2Client) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	f.lastStopInstancesInput = params
	if f.stopInstancesErr != nil {
		return nil, f.stopInstancesErr
	}
	return &ec2.StopInstancesOutput{}, nil
}

func (f *fakeEC2Client) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.lastRunInstancesInput = params
	if f.runInstancesErr != nil {
		return nil, f.runInstancesErr
	}
	return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: aws.String(f.runInstancesID)}}}, nil
}

func (f *fakeEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	// A real AWS call fails immediately against an already-canceled/
	// expired context; this fake must too, or bugs like a
	// withCallTimeout-scoped context being reused past its own cancel()
	// (2026-07-20, launch-from-template's WaitUntilRunning call) go
	// undetected by every test that doesn't explicitly wire up real
	// timing.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.describeCalls++
	if f.notFoundForCalls > 0 && f.describeCalls <= f.notFoundForCalls {
		return nil, &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound", Message: "The instance ID '" + params.InstanceIds[0] + "' does not exist"}
	}
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	state := types.InstanceStateNamePending
	if f.runningAfterCall > 0 && f.describeCalls >= f.runningAfterCall {
		state = types.InstanceStateNameRunning
	}
	if f.stoppedAfterCall > 0 && f.describeCalls >= f.stoppedAfterCall {
		state = types.InstanceStateNameStopped
	}
	inst := types.Instance{
		InstanceId:          aws.String(params.InstanceIds[0]),
		State:               &types.InstanceState{Name: state},
		BlockDeviceMappings: f.blockDeviceMappings,
		Tags:                f.instanceTags,
	}
	if state == types.InstanceStateNameRunning && f.publicIP != "" {
		inst.PublicIpAddress = aws.String(f.publicIP)
	}
	return &ec2.DescribeInstancesOutput{Reservations: []types.Reservation{{Instances: []types.Instance{inst}}}}, nil
}

func TestLaunch_Success(t *testing.T) {
	fake := &fakeEC2Client{runInstancesID: "i-abc123"}
	params := LaunchInstanceParams{
		ImageID:            "ami-1",
		InstanceType:       "t3.micro",
		KeyName:            "my-key",
		SecurityGroupIDs:   []string{"sg-1", "sg-2"},
		SubnetID:           "subnet-1",
		IAMInstanceProfile: "my-profile",
		UserData:           "#cloud-config",
		Tags:               map[string]string{"Name": "web", "Project": "caltechauthors", "Environment": "test"},
	}

	gotID, err := Launch(context.Background(), fake, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != "i-abc123" {
		t.Errorf("got %q, want %q", gotID, "i-abc123")
	}

	in := fake.lastRunInstancesInput
	if in == nil {
		t.Fatal("RunInstances was never called")
	}
	if aws.ToString(in.ImageId) != "ami-1" {
		t.Errorf("ImageId = %q, want %q", aws.ToString(in.ImageId), "ami-1")
	}
	if string(in.InstanceType) != "t3.micro" {
		t.Errorf("InstanceType = %q, want %q", in.InstanceType, "t3.micro")
	}
	if aws.ToString(in.KeyName) != "my-key" {
		t.Errorf("KeyName = %q, want %q", aws.ToString(in.KeyName), "my-key")
	}
	if len(in.SecurityGroupIds) != 2 {
		t.Errorf("SecurityGroupIds = %v, want 2 entries", in.SecurityGroupIds)
	}
	if aws.ToString(in.SubnetId) != "subnet-1" {
		t.Errorf("SubnetId = %q, want %q", aws.ToString(in.SubnetId), "subnet-1")
	}
	if in.IamInstanceProfile == nil || aws.ToString(in.IamInstanceProfile.Name) != "my-profile" {
		t.Errorf("IamInstanceProfile = %v, want Name=my-profile", in.IamInstanceProfile)
	}
	wantUserData := "I2Nsb3VkLWNvbmZpZw==" // base64("#cloud-config")
	if aws.ToString(in.UserData) != wantUserData {
		t.Errorf("UserData = %q, want %q", aws.ToString(in.UserData), wantUserData)
	}
	if len(in.TagSpecifications) != 1 || in.TagSpecifications[0].ResourceType != types.ResourceTypeInstance {
		t.Fatalf("TagSpecifications = %+v, want one instance-scoped spec", in.TagSpecifications)
	}
	if len(in.TagSpecifications[0].Tags) != 3 {
		t.Errorf("Tags = %+v, want 3 entries", in.TagSpecifications[0].Tags)
	}
}

func TestLaunch_SetsIMDSv2Required(t *testing.T) {
	// Security recommends IMDSv2 (HttpTokens: required) on every new
	// instance clasm launches -- TODO.md's bug: no MetadataOptions was
	// set at all, leaving new instances on AWS's own default (optional).
	fake := &fakeEC2Client{runInstancesID: "i-1"}
	params := LaunchInstanceParams{ImageID: "ami-1", InstanceType: "t3.micro", KeyName: "k", SecurityGroupIDs: []string{"sg-1"}, SubnetID: "subnet-1"}

	if _, err := Launch(context.Background(), fake, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastRunInstancesInput
	if in.MetadataOptions == nil {
		t.Fatal("MetadataOptions = nil, want HttpTokens: required")
	}
	if in.MetadataOptions.HttpTokens != types.HttpTokensStateRequired {
		t.Errorf("MetadataOptions.HttpTokens = %q, want %q", in.MetadataOptions.HttpTokens, types.HttpTokensStateRequired)
	}
}

func TestLaunch_NoIAMProfileOmitsField(t *testing.T) {
	fake := &fakeEC2Client{runInstancesID: "i-1"}
	params := LaunchInstanceParams{ImageID: "ami-1", InstanceType: "t3.micro", KeyName: "k", SecurityGroupIDs: []string{"sg-1"}, SubnetID: "subnet-1"}

	if _, err := Launch(context.Background(), fake, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastRunInstancesInput.IamInstanceProfile != nil {
		t.Errorf("IamInstanceProfile = %+v, want nil", fake.lastRunInstancesInput.IamInstanceProfile)
	}
	if fake.lastRunInstancesInput.UserData != nil {
		t.Errorf("UserData = %v, want nil", aws.ToString(fake.lastRunInstancesInput.UserData))
	}
}

func TestLaunch_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{runInstancesErr: errors.New("boom")}
	_, err := Launch(context.Background(), fake, LaunchInstanceParams{ImageID: "ami-1"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWaitUntilRunning_AlreadyRunning(t *testing.T) {
	fake := &fakeEC2Client{runningAfterCall: 1, publicIP: "1.2.3.4"}
	inst, err := WaitUntilRunning(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(inst.PublicIpAddress) != "1.2.3.4" {
		t.Errorf("PublicIpAddress = %q, want %q", aws.ToString(inst.PublicIpAddress), "1.2.3.4")
	}
}

func TestWaitUntilRunning_TransitionsAfterPolling(t *testing.T) {
	fake := &fakeEC2Client{runningAfterCall: 3}
	_, err := WaitUntilRunning(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.describeCalls < 3 {
		t.Errorf("describeCalls = %d, want at least 3", fake.describeCalls)
	}
}

func TestWaitUntilRunning_TimesOutWithError(t *testing.T) {
	fake := &fakeEC2Client{runningAfterCall: 0}
	_, err := WaitUntilRunning(context.Background(), fake, "i-1", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestWaitUntilRunning_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeErr: errors.New("boom")}
	_, err := WaitUntilRunning(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWaitUntilRunning_TreatsPostLaunchNotFoundAsNotYetVisible(t *testing.T) {
	// Real AWS behavior: ec2:RunInstances can return an instance ID that
	// ec2:DescribeInstances doesn't recognize for the first few seconds
	// (InvalidInstanceID.NotFound) -- this must be tolerated like "not
	// running yet", not treated as a hard failure.
	fake := &fakeEC2Client{notFoundForCalls: 2, runningAfterCall: 3}
	inst, err := WaitUntilRunning(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(inst.InstanceId) != "i-1" {
		t.Errorf("InstanceId = %q, want %q", aws.ToString(inst.InstanceId), "i-1")
	}
}

func TestWaitUntilRunning_TimesOutIfNeverVisible(t *testing.T) {
	fake := &fakeEC2Client{notFoundForCalls: 1000}
	_, err := WaitUntilRunning(context.Background(), fake, "i-1", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}
