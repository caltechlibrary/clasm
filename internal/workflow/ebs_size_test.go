package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestDescribeImageRootVolume_ReturnsRootDeviceAndDefaultSize(t *testing.T) {
	fake := &fakeEC2Client{
		describeImagesRootDeviceName: "/dev/sda1",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(8)}},
		},
	}

	deviceName, defaultGB, err := describeImageRootVolume(context.Background(), fake, "ami-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deviceName != "/dev/sda1" {
		t.Errorf("deviceName = %q, want %q", deviceName, "/dev/sda1")
	}
	if defaultGB != 8 {
		t.Errorf("defaultGB = %d, want 8", defaultGB)
	}
}

func TestDescribeImageRootVolume_IgnoresNonRootMappings(t *testing.T) {
	fake := &fakeEC2Client{
		describeImagesRootDeviceName: "/dev/xvda",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/sdb"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(100)}},
			{DeviceName: aws.String("/dev/xvda"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(250)}},
		},
	}

	deviceName, defaultGB, err := describeImageRootVolume(context.Background(), fake, "ami-rdm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deviceName != "/dev/xvda" {
		t.Errorf("deviceName = %q, want %q", deviceName, "/dev/xvda")
	}
	if defaultGB != 250 {
		t.Errorf("defaultGB = %d, want 250", defaultGB)
	}
}

func TestDescribeImageRootVolume_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeImagesErr: errors.New("boom")}
	if _, _, err := describeImageRootVolume(context.Background(), fake, "ami-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDescribeImageRootVolume_ErrorsWhenImageNotFound(t *testing.T) {
	fake := &fakeEC2Client{describeImagesNoImages: true}
	if _, _, err := describeImageRootVolume(context.Background(), fake, "ami-missing"); err == nil {
		t.Fatal("expected an error for a missing image")
	}
}

func TestPromptRootVolumeSizeGB_BlankUsesAMIDefault(t *testing.T) {
	_, input, buf := newPipeEditor("\n")
	got, err := promptRootVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8 {
		t.Errorf("got %d, want 8 (the AMI's own default)", got)
	}
}

func TestPromptRootVolumeSizeGB_AcceptsLargerExplicitValue(t *testing.T) {
	_, input, buf := newPipeEditor("250\n")
	got, err := promptRootVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 250 {
		t.Errorf("got %d, want 250", got)
	}
}

func TestPromptRootVolumeSizeGB_RejectsSmallerThanAMIDefault(t *testing.T) {
	_, input, buf := newPipeEditor("4\n" + "8\n")
	got, err := promptRootVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 8 {
		t.Errorf("got %d, want 8 after retry", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message in output, got:\n%s", buf.String())
	}
}

func TestPromptRootVolumeSizeGB_RejectsNonInteger(t *testing.T) {
	_, input, buf := newPipeEditor("abc\n" + "10\n")
	got, err := promptRootVolumeSizeGB(8, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 10 {
		t.Errorf("got %d, want 10 after retry", got)
	}
}
