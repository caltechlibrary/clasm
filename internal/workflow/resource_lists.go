package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// listKeyPairs lists key pair names in client's region, for
// promptKeyPairNameOrCreate's region-scoped pick list -- key pairs are
// per-region, and a name that exists in a different region than the
// picked AMI fails distantly at ec2:RunInstances with
// InvalidKeyPair.NotFound (see DECISIONS.md, "Validate key pair name
// against the AMI's region").
func listKeyPairs(ctx context.Context, client awsclient.EC2API) ([]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.KeyPairs))
	for _, kp := range out.KeyPairs {
		names = append(names, aws.ToString(kp.KeyName))
	}
	return names, nil
}

// SecurityGroupInfo is one security group offered by promptSecurityGroupIDs.
type SecurityGroupInfo struct {
	GroupID     string
	GroupName   string
	Description string
	VpcID       string
}

// listSecurityGroups lists security groups in the client's region, for
// Feature 2/3's "list available security groups" (DESIGN.md).
func listSecurityGroups(ctx context.Context, client awsclient.EC2API) ([]SecurityGroupInfo, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{})
	if err != nil {
		return nil, err
	}
	groups := make([]SecurityGroupInfo, 0, len(out.SecurityGroups))
	for _, g := range out.SecurityGroups {
		groups = append(groups, SecurityGroupInfo{
			GroupID:     aws.ToString(g.GroupId),
			GroupName:   aws.ToString(g.GroupName),
			Description: aws.ToString(g.Description),
			VpcID:       aws.ToString(g.VpcId),
		})
	}
	return groups, nil
}

// SubnetInfo is one subnet offered by promptSubnetID.
type SubnetInfo struct {
	SubnetID         string
	VpcID            string
	CIDR             string
	AvailabilityZone string
}

// listSubnets lists subnets in the client's region, for Feature 2/3's
// "list available subnets" (DESIGN.md).
func listSubnets(ctx context.Context, client awsclient.EC2API) ([]SubnetInfo, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		return nil, err
	}
	subnets := make([]SubnetInfo, 0, len(out.Subnets))
	for _, s := range out.Subnets {
		subnets = append(subnets, SubnetInfo{
			SubnetID:         aws.ToString(s.SubnetId),
			VpcID:            aws.ToString(s.VpcId),
			CIDR:             aws.ToString(s.CidrBlock),
			AvailabilityZone: aws.ToString(s.AvailabilityZone),
		})
	}
	return subnets, nil
}

// InstanceProfileInfo is one IAM instance profile offered by
// promptIAMInstanceProfileOrCreate.
type InstanceProfileInfo struct {
	Name  string
	Roles []string // attached role names, if any
}

// listInstanceProfiles lists IAM instance profiles in the account (IAM
// is a global service, unlike EC2/SSM's per-region clients), for
// DESIGN.md Feature 2's "IAM instance profile (optional)" pick list.
func listInstanceProfiles(ctx context.Context, client awsclient.IAMAPI) ([]InstanceProfileInfo, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListInstanceProfiles(ctx, &iam.ListInstanceProfilesInput{})
	if err != nil {
		return nil, err
	}
	profiles := make([]InstanceProfileInfo, 0, len(out.InstanceProfiles))
	for _, p := range out.InstanceProfiles {
		roles := make([]string, 0, len(p.Roles))
		for _, r := range p.Roles {
			roles = append(roles, aws.ToString(r.RoleName))
		}
		profiles = append(profiles, InstanceProfileInfo{
			Name:  aws.ToString(p.InstanceProfileName),
			Roles: roles,
		})
	}
	return profiles, nil
}

// RoleInfo is one IAM role offered when creating a new instance profile
// (promptIAMInstanceProfileOrCreate's "create new" sub-flow).
type RoleInfo struct {
	Name        string
	Description string
}

// listRoles lists IAM roles in the account, for attaching to a newly
// created instance profile (DESIGN.md, Feature 2).
func listRoles(ctx context.Context, client awsclient.IAMAPI) ([]RoleInfo, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListRoles(ctx, &iam.ListRolesInput{})
	if err != nil {
		return nil, err
	}
	roles := make([]RoleInfo, 0, len(out.Roles))
	for _, r := range out.Roles {
		roles = append(roles, RoleInfo{
			Name:        aws.ToString(r.RoleName),
			Description: aws.ToString(r.Description),
		})
	}
	return roles, nil
}
