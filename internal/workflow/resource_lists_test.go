package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestListKeyPairs_Success(t *testing.T) {
	fake := &fakeEC2Client{keyPairs: []types.KeyPairInfo{
		{KeyName: aws.String("etd-ami-test")},
		{KeyName: aws.String("other-key")},
	}}
	got, err := listKeyPairs(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "etd-ami-test" || got[1] != "other-key" {
		t.Errorf("got %v, want [etd-ami-test other-key]", got)
	}
}

func TestListKeyPairs_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("boom")}
	_, err := listKeyPairs(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error")
	}
}

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

func TestListInstanceProfiles_Success(t *testing.T) {
	fake := &fakeIAMClient{instanceProfiles: []iamtypes.InstanceProfile{
		{InstanceProfileName: aws.String("ec2-invenio-profile"), Roles: []iamtypes.Role{{RoleName: aws.String("ec2-invenio-role")}}},
	}}
	got, err := listInstanceProfiles(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ec2-invenio-profile" || len(got[0].Roles) != 1 || got[0].Roles[0] != "ec2-invenio-role" {
		t.Errorf("got %+v", got)
	}
}

func TestListInstanceProfiles_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listInstanceProfilesErr: errors.New("boom")}
	_, err := listInstanceProfiles(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestListInstanceProfiles_PaginatesAcrossMultiplePages reproduces a
// real bug found via live use, 2026-08-12: a single un-paginated
// ListInstanceProfiles call silently drops every instance profile past
// the first page, the same truncation inventory.listAllInstanceProfiles
// was already fixed for, 2026-07-23 (internal/inventory/iam.go) -- this
// sibling helper (used by promptIAMInstanceProfileOrCreate's picker,
// not the IAM domain's "Show Instance Profiles" screen) never got the
// same fix.
func TestListInstanceProfiles_PaginatesAcrossMultiplePages(t *testing.T) {
	fake := &fakeIAMClient{
		instanceProfiles:      []iamtypes.InstanceProfile{{InstanceProfileName: aws.String("page1-profile")}},
		instanceProfilesPage2: []iamtypes.InstanceProfile{{InstanceProfileName: aws.String("page2-profile")}},
	}
	got, err := listInstanceProfiles(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "page1-profile" || got[1].Name != "page2-profile" {
		t.Errorf("got %+v, want both page1-profile and page2-profile", got)
	}
}

func TestListRoles_Success(t *testing.T) {
	fake := &fakeIAMClient{roles: []iamtypes.Role{
		{RoleName: aws.String("ec2-invenio-role"), Description: aws.String("EC2 instance role for Invenio RDM")},
	}}
	got, err := listRoles(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ec2-invenio-role" || got[0].Description != "EC2 instance role for Invenio RDM" {
		t.Errorf("got %+v", got)
	}
}

func TestListRoles_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listRolesErr: errors.New("boom")}
	_, err := listRoles(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestListRoles_PaginatesAcrossMultiplePages reproduces a real bug
// found via live use, 2026-08-12: a single un-paginated ListRoles call
// silently drops every role past the first page -- an account with
// enough roles that a newly-created one (IAM doesn't sort ListRoles by
// creation date) lands on a later page never sees it in "Select a role
// to attach" (createInstanceProfileInteractive), even though the IAM
// domain's "Show Roles" screen (inventory.listAllRoles, fixed
// 2026-07-23) shows it correctly.
func TestListRoles_PaginatesAcrossMultiplePages(t *testing.T) {
	fake := &fakeIAMClient{
		roles:      []iamtypes.Role{{RoleName: aws.String("page1-role")}},
		rolesPage2: []iamtypes.Role{{RoleName: aws.String("page2-role")}},
	}
	got, err := listRoles(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "page1-role" || got[1].Name != "page2-role" {
		t.Errorf("got %+v, want both page1-role and page2-role", got)
	}
}
