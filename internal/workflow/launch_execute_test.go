package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
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

	lastCreateTagsInput *ec2.CreateTagsInput
	createTagsErr       error
	lastDeleteTagsInput *ec2.DeleteTagsInput
	deleteTagsErr       error
}

func (f *fakeEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.describeImagesErr != nil {
		return nil, f.describeImagesErr
	}
	imageID := ""
	if len(params.ImageIds) > 0 {
		imageID = params.ImageIds[0]
	}
	return &ec2.DescribeImagesOutput{Images: []types.Image{{ImageId: aws.String(imageID), Tags: f.describeImagesTags}}}, nil
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
	f.describeCalls++
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
