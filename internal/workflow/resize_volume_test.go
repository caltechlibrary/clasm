package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestRootVolumeInfo_ResolvesVolumeIDAndSize(t *testing.T) {
	fake := &fakeEC2Client{
		instanceRootDeviceName: "/dev/xvda",
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sdb"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-data")}},
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-root")}},
		},
		describeVolumesOutput: []types.Volume{{VolumeId: aws.String("vol-root"), Size: aws.Int32(8)}},
	}

	volumeID, currentGB, err := rootVolumeInfo(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volumeID != "vol-root" {
		t.Errorf("volumeID = %q, want %q", volumeID, "vol-root")
	}
	if currentGB != 8 {
		t.Errorf("currentGB = %d, want 8", currentGB)
	}
}

func TestRootVolumeInfo_ErrorsWhenInstanceNotFound(t *testing.T) {
	fake := &fakeEC2Client{describeErr: errors.New("boom")}
	if _, _, err := rootVolumeInfo(context.Background(), fake, "i-missing"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRootVolumeInfo_ErrorsWhenRootVolumeNotResolved(t *testing.T) {
	fake := &fakeEC2Client{
		instanceRootDeviceName: "/dev/xvda",
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/sdb"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-data")}},
		},
	}
	if _, _, err := rootVolumeInfo(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error when no mapping matches the root device name")
	}
}

func TestRootVolumeInfo_PropagatesDescribeVolumesError(t *testing.T) {
	fake := &fakeEC2Client{
		instanceRootDeviceName: "/dev/xvda",
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-root")}},
		},
		describeVolumesErr: errors.New("boom"),
	}
	if _, _, err := rootVolumeInfo(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPromptNewVolumeSizeGB_RejectsNotGreaterThanCurrent(t *testing.T) {
	_, input, buf := newPipeEditor("8\n" + "300\n")
	got, err := promptNewVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 300 {
		t.Errorf("got %d, want 300 after retry", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message in output, got:\n%s", buf.String())
	}
}

func TestPromptNewVolumeSizeGB_AcceptsGreaterThanCurrent(t *testing.T) {
	_, input, buf := newPipeEditor("250\n")
	got, err := promptNewVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 250 {
		t.Errorf("got %d, want 250", got)
	}
}

func TestModifyVolumeSize_SetsVolumeIDAndSize(t *testing.T) {
	fake := &fakeEC2Client{}
	if err := modifyVolumeSize(context.Background(), fake, "vol-1", 250); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastModifyVolumeInput
	if in == nil {
		t.Fatal("ModifyVolume was never called")
	}
	if aws.ToString(in.VolumeId) != "vol-1" {
		t.Errorf("VolumeId = %q, want vol-1", aws.ToString(in.VolumeId))
	}
	if aws.ToInt32(in.Size) != 250 {
		t.Errorf("Size = %d, want 250", aws.ToInt32(in.Size))
	}
}

func TestModifyVolumeSize_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{modifyVolumeErr: errors.New("boom")}
	if err := modifyVolumeSize(context.Background(), fake, "vol-1", 250); err == nil {
		t.Fatal("expected an error")
	}
}

func TestWaitUntilVolumeModificationUsable_TransitionsToOptimizing(t *testing.T) {
	fake := &fakeEC2Client{volumeModificationUsableAfterCall: 3}
	err := waitUntilVolumeModificationUsable(context.Background(), fake, "vol-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.describeVolumesModificationsCalls < 3 {
		t.Errorf("describeVolumesModificationsCalls = %d, want at least 3", fake.describeVolumesModificationsCalls)
	}
}

func TestWaitUntilVolumeModificationUsable_TimesOut(t *testing.T) {
	fake := &fakeEC2Client{}
	err := waitUntilVolumeModificationUsable(context.Background(), fake, "vol-1", 20*time.Millisecond, testPollInterval)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestWaitUntilVolumeModificationUsable_FailedStateReturnsError(t *testing.T) {
	fake := &fakeEC2Client{volumeModificationFailed: true}
	err := waitUntilVolumeModificationUsable(context.Background(), fake, "vol-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed modification")
	}
}

func TestResizeInstanceRootVolume_HappyPath(t *testing.T) {
	fake := &fakeEC2Client{
		instanceRootDeviceName: "/dev/xvda",
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-root")}},
		},
		describeVolumesOutput:             []types.Volume{{VolumeId: aws.String("vol-root"), Size: aws.Int32(8)}},
		volumeModificationUsableAfterCall: 1,
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	// onlineAfterCalls/finalStatus keep growRootFilesystem's own SSM
	// round-trip (run after ModifyVolume, DESIGN.md Part 2) from waiting
	// out real production timeouts -- this test only asserts on
	// ModifyVolume's input, not on the SSM growth step's own outcome.
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{onlineAfterCalls: 1, finalStatus: ssmtypes.CommandInvocationStatusFailed}}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1", Environment: "test"}

	input := "250\n" + // new size
		"i-1\n" // type-to-confirm

	term, menuInput, buf := newPipeEditor(input)
	err := resizeInstanceRootVolume(context.Background(), term, ec2Clients, ssmClients, inst, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if aws.ToInt32(fake.lastModifyVolumeInput.Size) != 250 {
		t.Errorf("ModifyVolume Size = %d, want 250", aws.ToInt32(fake.lastModifyVolumeInput.Size))
	}
	if aws.ToString(fake.lastModifyVolumeInput.VolumeId) != "vol-root" {
		t.Errorf("ModifyVolume VolumeId = %q, want vol-root", aws.ToString(fake.lastModifyVolumeInput.VolumeId))
	}
}

func TestResizeInstanceRootVolume_DeclinedConfirmationDoesNotModify(t *testing.T) {
	fake := &fakeEC2Client{
		instanceRootDeviceName: "/dev/xvda",
		blockDeviceMappings: []types.InstanceBlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-root")}},
		},
		describeVolumesOutput: []types.Volume{{VolumeId: aws.String("vol-root"), Size: aws.Int32(8)}},
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}

	input := "250\n" + // new size
		"n\n" // decline type-to-confirm (anything not matching mustMatch)

	term, menuInput, buf := newPipeEditor(input)
	err := resizeInstanceRootVolume(context.Background(), term, ec2Clients, ssmClients, inst, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastModifyVolumeInput != nil {
		t.Error("ModifyVolume was called despite a declined confirmation")
	}
}
