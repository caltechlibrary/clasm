package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func sdkImageDetailed(id string) types.Image {
	return types.Image{
		ImageId:        aws.String(id),
		Name:           aws.String("test-ami"),
		CreationDate:   aws.String("2026-07-24T00:00:00.000Z"),
		Architecture:   types.ArchitectureValuesX8664,
		EnaSupport:     aws.Bool(true),
		RootDeviceName: aws.String("/dev/sda1"),
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/sda1"),
				Ebs:        &types.EbsBlockDevice{VolumeSize: aws.Int32(20), SnapshotId: aws.String("snap-root")},
			},
			{
				DeviceName: aws.String("/dev/sdb"),
				Ebs:        &types.EbsBlockDevice{VolumeSize: aws.Int32(100), SnapshotId: aws.String("snap-data")},
			},
		},
		Tags: []types.Tag{
			{Key: aws.String("Project"), Value: aws.String("granian")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
			{Key: aws.String("Owner"), Value: aws.String("rsdoiel")},
		},
	}
}

func TestDescribeImageDetail_Found(t *testing.T) {
	client := &fakeEC2Client{images: []types.Image{sdkImageDetailed("ami-abc123")}}

	got, err := DescribeImageDetail(context.Background(), client, "us-west-2", "ami-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := ImageDetail{
		ImageID:        "ami-abc123",
		Name:           "test-ami",
		CreationDate:   "2026-07-24T00:00:00.000Z",
		Region:         "us-west-2",
		Architecture:   "x86_64",
		EnaSupport:     true,
		RootDeviceName: "/dev/sda1",
		BlockDeviceMappings: []BlockDeviceMappingDetail{
			{DeviceName: "/dev/sda1", VolumeSizeGB: 20, SnapshotID: "snap-root"},
			{DeviceName: "/dev/sdb", VolumeSizeGB: 100, SnapshotID: "snap-data"},
		},
		Project:     "granian",
		Environment: "test",
		Tags: map[string]string{
			"Project":     "granian",
			"Environment": "test",
			"Owner":       "rsdoiel",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDescribeImageDetail_NoBlockDeviceMappings(t *testing.T) {
	client := &fakeEC2Client{images: []types.Image{{
		ImageId: aws.String("ami-bare"),
		Name:    aws.String("bare"),
	}}}

	got, err := DescribeImageDetail(context.Background(), client, "us-west-1", "ami-bare")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.BlockDeviceMappings) != 0 {
		t.Errorf("expected no block device mappings, got %+v", got.BlockDeviceMappings)
	}
	if got.Tags == nil {
		t.Errorf("expected non-nil empty Tags map")
	}
}

func TestDescribeImageDetail_NotFound(t *testing.T) {
	client := &fakeEC2Client{images: nil}

	_, err := DescribeImageDetail(context.Background(), client, "us-west-2", "ami-missing")
	if err == nil {
		t.Fatal("expected an error for a missing AMI, got nil")
	}
}

func TestDescribeImageDetail_ClientError(t *testing.T) {
	wantErr := errors.New("boom")
	client := &fakeEC2Client{err: wantErr}

	_, err := DescribeImageDetail(context.Background(), client, "us-west-2", "ami-abc123")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
