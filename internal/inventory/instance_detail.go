package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// InstanceDetail is one EC2 instance's curated detail fields, fetched
// on demand for the Show Instance Detail workflow -- deliberately a
// separate, single-resource fetch rather than added fields on Instance
// itself (DESIGN.md, "Instance/AMI Detail Views"; DECISIONS.md,
// "Instance/AMI Detail Views: on-demand describe calls, appended menu
// placement"), matching DescribeLaunchTemplateVersion's own shape.
// Volume sizes aren't included here -- the caller combines this with
// workflow.GatherVolumeInfo, which already fetches that via
// ec2:DescribeVolumes.
type InstanceDetail struct {
	InstanceID         string
	Name               string
	State              string
	InstanceType       string
	ImageID            string
	Region             string
	VPCID              string
	SubnetID           string
	SecurityGroupIDs   []string
	IAMInstanceProfile string
	KeyName            string
	PublicIP           string
	PrivateIP          string
	Project            string
	Environment        string
	Tags               map[string]string
}

// DescribeInstanceDetail fetches one instance's curated detail fields
// via a single ec2:DescribeInstances call scoped to instanceID. client
// must already be scoped to region.
func DescribeInstanceDetail(ctx context.Context, client awsclient.EC2API, region, instanceID string) (InstanceDetail, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return InstanceDetail{}, err
	}
	for _, reservation := range out.Reservations {
		for _, inst := range reservation.Instances {
			if aws.ToString(inst.InstanceId) == instanceID {
				return instanceDetailFromSDK(inst, region), nil
			}
		}
	}
	return InstanceDetail{}, fmt.Errorf("instance %s not found", instanceID)
}

func instanceDetailFromSDK(inst types.Instance, region string) InstanceDetail {
	name, project, environment := tagValues(inst.Tags)
	state := ""
	if inst.State != nil {
		state = string(inst.State.Name)
	}

	securityGroupIDs := make([]string, len(inst.SecurityGroups))
	for i, sg := range inst.SecurityGroups {
		securityGroupIDs[i] = aws.ToString(sg.GroupId)
	}

	iamInstanceProfile := ""
	if inst.IamInstanceProfile != nil {
		iamInstanceProfile = instanceProfileNameFromARN(aws.ToString(inst.IamInstanceProfile.Arn))
	}

	return InstanceDetail{
		InstanceID:         aws.ToString(inst.InstanceId),
		Name:               name,
		State:              state,
		InstanceType:       string(inst.InstanceType),
		ImageID:            aws.ToString(inst.ImageId),
		Region:             region,
		VPCID:              aws.ToString(inst.VpcId),
		SubnetID:           aws.ToString(inst.SubnetId),
		SecurityGroupIDs:   securityGroupIDs,
		IAMInstanceProfile: iamInstanceProfile,
		KeyName:            aws.ToString(inst.KeyName),
		PublicIP:           aws.ToString(inst.PublicIpAddress),
		PrivateIP:          aws.ToString(inst.PrivateIpAddress),
		Project:            project,
		Environment:        environment,
		Tags:               tagsToMap(inst.Tags),
	}
}

// instanceProfileNameFromARN extracts an instance profile's name from
// its ARN (e.g. "arn:aws:iam::111122223333:instance-profile/my-profile"
// -> "my-profile") -- ec2.types.IamInstanceProfile (unlike the launch
// template/create-time specification types) only ever carries an
// Arn/Id, never a Name, directly from AWS.
func instanceProfileNameFromARN(arn string) string {
	if arn == "" {
		return ""
	}
	if idx := strings.LastIndex(arn, "/"); idx != -1 {
		return arn[idx+1:]
	}
	return arn
}
