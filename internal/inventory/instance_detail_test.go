package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func sdkInstanceDetailed(id string) types.Instance {
	return types.Instance{
		InstanceId:   aws.String(id),
		State:        &types.InstanceState{Name: types.InstanceStateNameRunning},
		InstanceType: types.InstanceTypeT3Micro,
		ImageId:      aws.String("ami-detail"),
		VpcId:        aws.String("vpc-1"),
		SubnetId:     aws.String("subnet-1"),
		SecurityGroups: []types.GroupIdentifier{
			{GroupId: aws.String("sg-1")},
			{GroupId: aws.String("sg-2")},
		},
		IamInstanceProfile: &types.IamInstanceProfile{
			Arn: aws.String("arn:aws:iam::123456789012:instance-profile/my-profile"),
		},
		KeyName:          aws.String("my-key"),
		PublicIpAddress:  aws.String("1.2.3.4"),
		PrivateIpAddress: aws.String("10.0.0.4"),
		Tags: []types.Tag{
			{Key: aws.String("Name"), Value: aws.String("test-instance")},
			{Key: aws.String("Project"), Value: aws.String("granian")},
			{Key: aws.String("Environment"), Value: aws.String("test")},
			{Key: aws.String("Owner"), Value: aws.String("rsdoiel")},
		},
	}
}

func TestDescribeInstanceDetail_Found(t *testing.T) {
	client := &fakeEC2Client{reservations: []types.Reservation{{Instances: []types.Instance{sdkInstanceDetailed("i-abc123")}}}}

	got, err := DescribeInstanceDetail(context.Background(), client, "us-west-2", "i-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := InstanceDetail{
		InstanceID:         "i-abc123",
		Name:               "test-instance",
		State:              "running",
		InstanceType:       "t3.micro",
		ImageID:            "ami-detail",
		Region:             "us-west-2",
		VPCID:              "vpc-1",
		SubnetID:           "subnet-1",
		SecurityGroupIDs:   []string{"sg-1", "sg-2"},
		IAMInstanceProfile: "my-profile",
		KeyName:            "my-key",
		PublicIP:           "1.2.3.4",
		PrivateIP:          "10.0.0.4",
		Project:            "granian",
		Environment:        "test",
		Tags: map[string]string{
			"Name":        "test-instance",
			"Project":     "granian",
			"Environment": "test",
			"Owner":       "rsdoiel",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDescribeInstanceDetail_NoIAMProfileOrIPs(t *testing.T) {
	client := &fakeEC2Client{reservations: []types.Reservation{{Instances: []types.Instance{{
		InstanceId: aws.String("i-bare"),
		State:      &types.InstanceState{Name: types.InstanceStateNameStopped},
	}}}}}

	got, err := DescribeInstanceDetail(context.Background(), client, "us-west-1", "i-bare")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.IAMInstanceProfile != "" || got.PublicIP != "" || got.PrivateIP != "" || got.KeyName != "" {
		t.Errorf("expected empty optional fields, got %+v", got)
	}
	if got.Tags == nil {
		t.Errorf("expected non-nil empty Tags map")
	}
}

func TestDescribeInstanceDetail_NotFound(t *testing.T) {
	client := &fakeEC2Client{reservations: nil}

	_, err := DescribeInstanceDetail(context.Background(), client, "us-west-2", "i-missing")
	if err == nil {
		t.Fatal("expected an error for a missing instance, got nil")
	}
}

func TestDescribeInstanceDetail_ClientError(t *testing.T) {
	wantErr := errors.New("boom")
	client := &fakeEC2Client{err: wantErr}

	_, err := DescribeInstanceDetail(context.Background(), client, "us-west-2", "i-abc123")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
