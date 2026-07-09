package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestCollectLaunchInstanceParams(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base-ubuntu", Region: "us-east-1", CreationDate: "2026-01-15"},
		{ImageID: "ami-2", Name: "invenio-rdm", Region: "us-east-1", CreationDate: "2026-02-01", Project: "caltechauthors"},
	}

	input := "2\n" + // pick ami-2
		"authorstest\n" + // Name tag
		"4\n" + // instance type: t3.large
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-keypair\n" + // New key pair name
		"sg-1, sg-2\n" + // security groups (no groups fetched -> free-text fallback)
		"subnet-abc\n" + // subnet (no subnets fetched -> free-text fallback)
		"1\n" + // IAM profile: select (none)
		"#cloud-config\n" + // user data (inline)
		"\n" + // Project tag (blank -> default from ami-2)
		"test\n" // Environment tag

	term, le, _ := newPipeEditor(t, input)
	fake := &fakeEC2Client{}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ImageID != "ami-2" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-2")
	}
	if got.InstanceType != "t3.large" {
		t.Errorf("InstanceType = %q, want %q", got.InstanceType, "t3.large")
	}
	if got.KeyName != "my-keypair" {
		t.Errorf("KeyName = %q, want %q", got.KeyName, "my-keypair")
	}
	wantSGs := []string{"sg-1", "sg-2"}
	if len(got.SecurityGroupIDs) != 2 || got.SecurityGroupIDs[0] != wantSGs[0] || got.SecurityGroupIDs[1] != wantSGs[1] {
		t.Errorf("SecurityGroupIDs = %v, want %v", got.SecurityGroupIDs, wantSGs)
	}
	if got.SubnetID != "subnet-abc" {
		t.Errorf("SubnetID = %q, want %q", got.SubnetID, "subnet-abc")
	}
	if got.IAMInstanceProfile != "" {
		t.Errorf("IAMInstanceProfile = %q, want empty", got.IAMInstanceProfile)
	}
	if got.UserData != "#cloud-config" {
		t.Errorf("UserData = %q, want %q", got.UserData, "#cloud-config")
	}
	if got.Tags["Name"] != "authorstest" {
		t.Errorf("Tags[Name] = %q, want %q", got.Tags["Name"], "authorstest")
	}
	if got.Tags["Project"] != "caltechauthors" {
		t.Errorf("Tags[Project] = %q, want default %q", got.Tags["Project"], "caltechauthors")
	}
	if got.Tags["Environment"] != "test" {
		t.Errorf("Tags[Environment] = %q, want %q", got.Tags["Environment"], "test")
	}
}

func TestCollectLaunchInstanceParams_NamePromptedRightAfterAMIPick(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	input := "1\n" + // pick ami-1
		"web\n" + // Name tag, right after the AMI pick
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-1\n" + // subnet
		"1\n" + // IAM profile: select (none)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Name"] != "web" {
		t.Errorf("Tags[Name] = %q, want %q", got.Tags["Name"], "web")
	}

	out := buf.String()
	nameIdx := strings.Index(out, "Name tag")
	instanceTypeIdx := strings.Index(out, "Select an instance type")
	if nameIdx < 0 || instanceTypeIdx < 0 || nameIdx > instanceTypeIdx {
		t.Errorf("expected the Name tag prompt to precede the Instance type prompt in output:\n%s", out)
	}
}

func TestCollectLaunchInstanceParams_PicksSecurityGroupsAndSubnetFromLists(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}
	fake := &fakeEC2Client{
		securityGroups: []types.SecurityGroup{
			{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
			{GroupId: aws.String("sg-2"), GroupName: aws.String("db")},
		},
		subnets: []types.Subnet{
			{SubnetId: aws.String("subnet-1"), VpcId: aws.String("vpc-1"), AvailabilityZone: aws.String("us-east-1a"), CidrBlock: aws.String("10.0.1.0/24")},
		},
		instanceTypeOfferings: map[string][]string{"t3.micro": {"us-east-1a"}}, // matches the chosen instance type (curated list entry 1)
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	input := "1\n" + // pick ami-1
		"web\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"1,2\n" + // pick both security groups by number
		"1\n" + // pick the only subnet
		"1\n" + // IAM profile: select (none)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, le, _ := newPipeEditor(t, input)
	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.KeyName != "my-key" {
		t.Errorf("KeyName = %q, want %q", got.KeyName, "my-key")
	}
	if len(got.SecurityGroupIDs) != 2 || got.SecurityGroupIDs[0] != "sg-1" || got.SecurityGroupIDs[1] != "sg-2" {
		t.Errorf("SecurityGroupIDs = %v, want [sg-1 sg-2]", got.SecurityGroupIDs)
	}
	if got.SubnetID != "subnet-1" {
		t.Errorf("SubnetID = %q, want %q", got.SubnetID, "subnet-1")
	}
}

func TestCollectLaunchInstanceParams_RejectsInvalidEnvironment(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}

	input := "1\n" + // pick ami-1
		"web\n" + // Name tag
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-keypair\n" + // New key pair name
		"sg-1\n" + // security groups
		"subnet-abc\n" + // subnet
		"1\n" + // IAM profile: select (none)
		"\n" + // user data
		"caltechdata\n" + // Project tag
		"prod\n" + // Environment tag (invalid)
		"production\n" // Environment tag (retry, valid)

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Environment"] != "production" {
		t.Errorf("Tags[Environment] = %q, want %q", got.Tags["Environment"], "production")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message in output, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParams_RejectsBlankRequiredFields(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Region: "us-east-1"}}

	input := "1\n" + // pick ami-1
		"\n" + // Name tag (blank -- rejected)
		"web\n" + // Name tag (retry, valid)
		"1\n" + // instance type: t3.micro
		"\n" + // Key pair pick list (blank -- invalid selection, rejected)
		"1\n" + // Key pair: Create new key pair (zero existing keys)
		"my-keypair\n" + // New key pair name
		"\n" + // Security groups (blank -- rejected)
		"sg-1\n" + // Security groups (retry, valid)
		"\n" + // Subnet ID (blank -- rejected)
		"subnet-abc\n" + // Subnet ID (retry, valid)
		"1\n" + // IAM profile: select (none)
		"\n" + // user data (optional, blank is fine)
		"caltechdata\n" + // Project tag
		"test\n" // Environment tag

	term, le, buf := newPipeEditor(t, input)
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tags["Name"] != "web" || got.KeyName != "my-keypair" || len(got.SecurityGroupIDs) != 1 || got.SubnetID != "subnet-abc" {
		t.Errorf("got %+v, want all required fields filled after retry", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected validation error messages in output, got:\n%s", buf.String())
	}
}

func TestCollectLaunchInstanceParams_OfficialUbuntuAMIIsSelectableFromThePickList(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-owned", Region: "us-east-1"}}
	fake := &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
		nobleNamePattern: {{ImageId: aws.String("ami-noble"), CreationDate: aws.String("2026-06-01T00:00:00.000Z"), EnaSupport: aws.Bool(true)}},
	}}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": fake}
	ssmClients := map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}

	input := "2\n" + // owned AMI (1), then the appended official Ubuntu AMI (2)
		"web\n" +
		"1\n" + // instance type: t3.micro
		"1\n" + // key pair: Create new key pair (zero existing keys)
		"my-key\n" + // New key pair name
		"sg-1\n" +
		"subnet-1\n" +
		"1\n" + // IAM profile: select (none)
		"\n" +
		"caltechdata\n" +
		"test\n"

	term, le, buf := newPipeEditor(t, input)
	got, _, _, err := CollectLaunchInstanceParams(context.Background(), term, le, ec2Clients, ssmClients, &fakeIAMClient{}, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ImageID != "ami-noble" {
		t.Errorf("ImageID = %q, want %q", got.ImageID, "ami-noble")
	}
	if !strings.Contains(buf.String(), "Ubuntu 24.04 LTS") {
		t.Errorf("expected the official Ubuntu AMI to appear in the pick list, got:\n%s", buf.String())
	}
}
