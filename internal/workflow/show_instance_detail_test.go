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

func TestShowInstanceDetail_DisplaysCuratedFieldsAndVolumes(t *testing.T) {
	fake := &fakeEC2Client{
		runningAfterCall:            1,
		instanceDetailImageID:       "ami-1",
		instanceDetailInstanceType:  "t3.micro",
		instanceDetailVpcID:         "vpc-1",
		instanceDetailSubnetID:      "subnet-1",
		instanceDetailSecurityGroup: []string{"sg-1", "sg-2"},
		instanceDetailIAMProfileARN: "arn:aws:iam::123456789012:instance-profile/my-profile",
		instanceDetailKeyName:       "my-key",
		instanceDetailPrivateIP:     "10.0.0.4",
		publicIP:                    "1.2.3.4",
		instanceTags: []types.Tag{
			{Key: aws.String("Name"), Value: aws.String("web-1")},
			{Key: aws.String("Project"), Value: aws.String("caltechauthors")},
			{Key: aws.String("Environment"), Value: aws.String("production")},
		},
		describeVolumesOutput: []types.Volume{
			{VolumeId: aws.String("vol-1"), Size: aws.Int32(20)},
			{VolumeId: aws.String("vol-2"), Size: aws.Int32(100)},
		},
	}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	inst := inventory.Instance{InstanceID: "i-abc123", Region: "us-west-2"}

	w, _, buf := newPipeEditor("")
	if err := showInstanceDetail(context.Background(), w, clients, inst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"i-abc123", "web-1", "running", "t3.micro", "ami-1", "vpc-1", "subnet-1",
		"sg-1, sg-2", "my-profile", "my-key", "1.2.3.4", "10.0.0.4",
		"caltechauthors", "production", "vol-1", "vol-2", "120",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestShowInstanceDetail_NoVolumes(t *testing.T) {
	fake := &fakeEC2Client{runningAfterCall: 1}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	inst := inventory.Instance{InstanceID: "i-bare", Region: "us-west-2"}

	w, _, buf := newPipeEditor("")
	if err := showInstanceDetail(context.Background(), w, clients, inst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "none") {
		t.Errorf("expected \"none\" for an instance with no EBS volumes, got:\n%s", buf.String())
	}
}

func TestShowInstanceDetail_UnknownRegionErrors(t *testing.T) {
	clients := map[string]awsclient.EC2API{}
	inst := inventory.Instance{InstanceID: "i-abc123", Region: "us-east-1"}
	w, _, _ := newPipeEditor("")
	if err := showInstanceDetail(context.Background(), w, clients, inst); err == nil {
		t.Fatal("expected an error for a region with no configured client")
	}
}

func TestShowInstanceDetail_DescribeInstancesErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &fakeEC2Client{describeErr: wantErr}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	inst := inventory.Instance{InstanceID: "i-abc123", Region: "us-west-2"}
	w, _, _ := newPipeEditor("")
	if err := showInstanceDetail(context.Background(), w, clients, inst); !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestShowInstanceDetail_GatherVolumeInfoErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &fakeEC2Client{runningAfterCall: 1, describeVolumesErr: wantErr}
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	inst := inventory.Instance{InstanceID: "i-abc123", Region: "us-west-2"}
	w, _, _ := newPipeEditor("")
	if err := showInstanceDetail(context.Background(), w, clients, inst); !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestShowInstanceDetail_NoInstancesMessage(t *testing.T) {
	w, _, buf := newPipeEditor("")
	if err := ShowInstanceDetail(context.Background(), w, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances found") {
		t.Errorf("expected \"No instances found\", got:\n%s", buf.String())
	}
}
