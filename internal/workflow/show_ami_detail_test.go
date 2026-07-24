package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestShowAMIDetail_DisplaysCuratedFields(t *testing.T) {
	fake := &fakeEC2Client{
		imageAvailableAfterCall:      1,
		describeImagesName:           "granian-13-init",
		describeImagesCreationDate:   "2026-07-24T00:00:00.000Z",
		describeImagesArchitecture:   "x86_64",
		describeImagesEnaSupport:     true,
		describeImagesRootDeviceName: "/dev/sda1",
		describeImagesBlockDeviceMappings: []types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(20), SnapshotId: aws.String("snap-root")}},
			{DeviceName: aws.String("/dev/sdb"), Ebs: &types.EbsBlockDevice{VolumeSize: aws.Int32(100), SnapshotId: aws.String("snap-data")}},
		},
		describeImagesTags: []types.Tag{
			{Key: aws.String("Project"), Value: aws.String("granian")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
		},
	}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	img := inventory.Image{ImageID: "ami-abc123", Region: "us-west-2"}

	w, _, buf := newPipeEditor("")
	if err := showAMIDetail(context.Background(), w, clients, img); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"ami-abc123", "granian-13-init", "2026-07-24", "x86_64", "ENA", "/dev/sda1",
		"/dev/sdb", "20 GiB", "100 GiB", "snap-root", "snap-data", "granian", "test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestShowAMIDetail_NoBlockDeviceMappings(t *testing.T) {
	fake := &fakeEC2Client{imageAvailableAfterCall: 1}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	img := inventory.Image{ImageID: "ami-bare", Region: "us-west-2"}

	w, _, buf := newPipeEditor("")
	if err := showAMIDetail(context.Background(), w, clients, img); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "none") {
		t.Errorf("expected \"none\" for an AMI with no block device mappings, got:\n%s", buf.String())
	}
}

func TestShowAMIDetail_UnknownRegionErrors(t *testing.T) {
	clients := map[string]awsclient.EC2API{}
	img := inventory.Image{ImageID: "ami-abc123", Region: "us-east-1"}
	w, _, _ := newPipeEditor("")
	if err := showAMIDetail(context.Background(), w, clients, img); err == nil {
		t.Fatal("expected an error for a region with no configured client")
	}
}

func TestShowAMIDetail_DescribeImagesErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &fakeEC2Client{describeImagesErr: wantErr}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	img := inventory.Image{ImageID: "ami-abc123", Region: "us-west-2"}
	w, _, _ := newPipeEditor("")
	if err := showAMIDetail(context.Background(), w, clients, img); !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestShowAMIDetail_NoImagesMessage(t *testing.T) {
	w, _, buf := newPipeEditor("")
	if err := ShowAMIDetail(context.Background(), w, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs found") {
		t.Errorf("expected \"No AMIs found\", got:\n%s", buf.String())
	}
}
