package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestPromptSubnetID_PicksFromList(t *testing.T) {
	fake := &fakeEC2Client{subnets: []types.Subnet{
		{SubnetId: aws.String("subnet-1"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("us-east-1a")},
		{SubnetId: aws.String("subnet-2"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.2.0/24"), AvailabilityZone: aws.String("us-east-1b")},
	}}
	term, le, _ := newPipeEditor(t, "1\n")

	got, err := promptSubnetID(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "subnet-1" {
		t.Errorf("got %q, want %q", got, "subnet-1")
	}
}

func TestPromptSubnetID_FallsBackToFreeTextWhenEmpty(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "subnet-manual\n")

	got, err := promptSubnetID(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "subnet-manual" {
		t.Errorf("got %q, want %q", got, "subnet-manual")
	}
}

func TestPromptSecurityGroupIDs_ResolvesNumbersFromList(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web"), Description: aws.String("web tier")},
		{GroupId: aws.String("sg-2"), GroupName: aws.String("db"), Description: aws.String("db tier")},
	}}
	term, le, buf := newPipeEditor(t, "1,2\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "sg-1" || got[1] != "sg-2" {
		t.Errorf("got %v, want [sg-1 sg-2]", got)
	}
	if !strings.Contains(buf.String(), "web tier") {
		t.Errorf("expected the security group list to be displayed, got:\n%s", buf.String())
	}
}

func TestPromptSecurityGroupIDs_AcceptsRawIDsAlongsideNumbers(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
	}}
	term, le, _ := newPipeEditor(t, "1,sg-999\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "sg-1" || got[1] != "sg-999" {
		t.Errorf("got %v, want [sg-1 sg-999]", got)
	}
}

func TestPromptSecurityGroupIDs_RejectsOutOfRangeNumberThenRetries(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
	}}
	term, le, buf := newPipeEditor(t, "99\n1\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "sg-1" {
		t.Errorf("got %v, want [sg-1]", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message, got:\n%s", buf.String())
	}
}

func TestPromptSecurityGroupIDs_FallsBackToFreeTextWhenEmpty(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "sg-manual\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "sg-manual" {
		t.Errorf("got %v, want [sg-manual]", got)
	}
}
