package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestListSecurityGroups_Success(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web"), Description: aws.String("web tier"), VpcId: aws.String("vpc-1")},
	}}
	got, err := listSecurityGroups(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].GroupID != "sg-1" || got[0].GroupName != "web" || got[0].Description != "web tier" || got[0].VpcID != "vpc-1" {
		t.Errorf("got %+v", got)
	}
}

func TestListSecurityGroups_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeSecurityGroupsErr: errors.New("boom")}
	_, err := listSecurityGroups(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestListSubnets_Success(t *testing.T) {
	fake := &fakeEC2Client{subnets: []types.Subnet{
		{SubnetId: aws.String("subnet-1"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("us-east-1a")},
	}}
	got, err := listSubnets(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].SubnetID != "subnet-1" || got[0].VpcID != "vpc-1" || got[0].CIDR != "10.0.1.0/24" || got[0].AvailabilityZone != "us-east-1a" {
		t.Errorf("got %+v", got)
	}
}

func TestListSubnets_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeSubnetsErr: errors.New("boom")}
	_, err := listSubnets(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error")
	}
}
