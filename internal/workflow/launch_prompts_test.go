package workflow

import (
	"context"
	"errors"
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

	got, err := promptSubnetID(context.Background(), term, le, fake, "t3.micro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-1" {
		t.Errorf("got %q, want %q", got.SubnetID, "subnet-1")
	}
	if got.AvailabilityZone != "us-east-1a" {
		t.Errorf("AvailabilityZone = %q, want %q", got.AvailabilityZone, "us-east-1a")
	}
}

func TestPromptSubnetID_FallsBackToFreeTextWhenEmpty(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "subnet-manual\n")

	got, err := promptSubnetID(context.Background(), term, le, fake, "t3.micro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-manual" {
		t.Errorf("got %q, want %q", got.SubnetID, "subnet-manual")
	}
	if got.AvailabilityZone != "" {
		t.Errorf("AvailabilityZone = %q, want empty (unknown via free-text fallback)", got.AvailabilityZone)
	}
}

func TestPromptSubnetID_FiltersOutAZsThatDontSupportTheInstanceType(t *testing.T) {
	fake := &fakeEC2Client{
		subnets: []types.Subnet{
			{SubnetId: aws.String("subnet-bad"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("us-west-2d")},
			{SubnetId: aws.String("subnet-good"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.2.0/24"), AvailabilityZone: aws.String("us-west-2a")},
		},
		instanceTypeOfferings: map[string][]string{"t2.medium": {"us-west-2a", "us-west-2b", "us-west-2c"}},
	}
	// Only one subnet should remain after filtering (subnet-bad's AZ,
	// us-west-2d, doesn't support t2.medium) -- "1" now picks subnet-good.
	term, le, buf := newPipeEditor(t, "1\n")

	got, err := promptSubnetID(context.Background(), term, le, fake, "t2.medium")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-good" {
		t.Errorf("got %q, want %q", got.SubnetID, "subnet-good")
	}
	if strings.Contains(buf.String(), "subnet-bad") {
		t.Errorf("expected the incompatible subnet to be filtered out of the listing, got:\n%s", buf.String())
	}
}

func TestPromptSubnetID_ShowsAllSubnetsWhenAZLookupErrors(t *testing.T) {
	fake := &fakeEC2Client{
		subnets: []types.Subnet{
			{SubnetId: aws.String("subnet-1"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("us-west-2d")},
		},
		describeInstanceTypeOfferingsErr: errors.New("access denied"),
	}
	term, le, _ := newPipeEditor(t, "1\n")

	got, err := promptSubnetID(context.Background(), term, le, fake, "t2.medium")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-1" {
		t.Errorf("got %q, want %q (filtering should be skipped when the AZ lookup itself fails)", got.SubnetID, "subnet-1")
	}
}

func TestPromptSubnetID_ShowsAllSubnetsWhenFilteringWouldLeaveNone(t *testing.T) {
	fake := &fakeEC2Client{
		subnets: []types.Subnet{
			{SubnetId: aws.String("subnet-1"), VpcId: aws.String("vpc-1"), CidrBlock: aws.String("10.0.1.0/24"), AvailabilityZone: aws.String("us-west-2d")},
		},
		instanceTypeOfferings: map[string][]string{"t2.medium": {"us-west-2a"}}, // doesn't include us-west-2d
	}
	term, le, _ := newPipeEditor(t, "1\n")

	got, err := promptSubnetID(context.Background(), term, le, fake, "t2.medium")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-1" {
		t.Errorf("got %q, want %q (filtering to zero options should fall back to showing everything, not a dead end)", got.SubnetID, "subnet-1")
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

func TestPromptInstanceType_PicksFromCuratedList(t *testing.T) {
	term, le, buf := newPipeEditor(t, "1\n")

	got, err := promptInstanceType(term, le)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.micro" {
		t.Errorf("got %q, want %q", got, "t3.micro")
	}
	if !strings.Contains(buf.String(), "vCPU") {
		t.Errorf("expected vCPU/memory info in the listing, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_PicksALaterCuratedEntry(t *testing.T) {
	term, le, _ := newPipeEditor(t, "4\n")

	got, err := promptInstanceType(term, le)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.large" {
		t.Errorf("got %q, want %q", got, "t3.large")
	}
}

func TestPromptInstanceType_IncludesNonENARequiredEntries(t *testing.T) {
	// t2.micro/t2.medium are the curated list's only non-Nitro types --
	// every other entry requires ENA (DECISIONS.md, "Add non-ENA-
	// required options to the curated instance type list"), so an AMI
	// without ENA support needs one of these to launch at all.
	term, le, buf := newPipeEditor(t, "10\n")

	got, err := promptInstanceType(term, le)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t2.micro" {
		t.Errorf("got %q, want %q", got, "t2.micro")
	}
	if !strings.Contains(buf.String(), "no ENA required") {
		t.Errorf("expected the listing to call out non-ENA-required entries, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_OtherFallsBackToFreeText(t *testing.T) {
	term, le, _ := newPipeEditor(t, "12\nc6g.medium\n") // 12) Other

	got, err := promptInstanceType(term, le)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "c6g.medium" {
		t.Errorf("got %q, want %q", got, "c6g.medium")
	}
}

func TestPromptInstanceType_OtherRejectsBlank(t *testing.T) {
	term, le, buf := newPipeEditor(t, "12\n\nt4g.nano\n") // 12) Other, blank (rejected), retry

	got, err := promptInstanceType(term, le)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t4g.nano" {
		t.Errorf("got %q, want %q", got, "t4g.nano")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message, got:\n%s", buf.String())
	}
}
