package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

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
