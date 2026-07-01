package workflow

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// DefaultLaunchPollInterval and DefaultLaunchTimeout are the production
// poll interval/timeout for WaitUntilRunning, matching the current Bash
// behavior (see PLAN.md, Phase 4).
const (
	DefaultLaunchPollInterval = 5 * time.Second
	DefaultLaunchTimeout      = 5 * time.Minute
)

// buildTagSpecification converts a plain tag map into a typed SDK
// TagSpecification -- this replaces ec2_ami_manager.bash's hand-built
// --tag-specifications shorthand string, the exact bug class (malformed
// AWS CLI shorthand that silently failed create-image) that motivated
// retargeting this project to Go (see DECISIONS.md, "Retarget
// implementation from Bash to Go"). Empty tag values are omitted.
func buildTagSpecification(resourceType types.ResourceType, tags map[string]string) types.TagSpecification {
	spec := types.TagSpecification{ResourceType: resourceType}
	for k, v := range tags {
		if v == "" {
			continue
		}
		spec.Tags = append(spec.Tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return spec
}

// Launch calls ec2:RunInstances for a single instance from params,
// returning the new instance's ID. Executing against AWS is kept
// separate from CollectLaunchInstanceParams so a future Recorded Script
// can produce the same LaunchInstanceParams without this code knowing
// the difference (see DECISIONS.md, "Structure workflows for future
// record/replay").
func Launch(ctx context.Context, client awsclient.EC2API, params LaunchInstanceParams) (string, error) {
	input := &ec2.RunInstancesInput{
		ImageId:           aws.String(params.ImageID),
		InstanceType:      types.InstanceType(params.InstanceType),
		KeyName:           aws.String(params.KeyName),
		SecurityGroupIds:  params.SecurityGroupIDs,
		SubnetId:          aws.String(params.SubnetID),
		MinCount:          aws.Int32(1),
		MaxCount:          aws.Int32(1),
		TagSpecifications: []types.TagSpecification{buildTagSpecification(types.ResourceTypeInstance, params.Tags)},
	}
	if params.UserData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(params.UserData)))
	}
	if params.IAMInstanceProfile != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{Name: aws.String(params.IAMInstanceProfile)}
	}

	out, err := client.RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	if len(out.Instances) == 0 {
		return "", errors.New("RunInstances returned no instances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// WaitUntilRunning polls ec2:DescribeInstances until instanceID reaches
// the running state or the timeout elapses, returning the instance (so
// callers can read its connection info) or a timeout error -- unlike
// WaitForSSMOnline, a timeout here is a real error: an instance that
// never reaches running needs the operator's attention.
func WaitUntilRunning(ctx context.Context, client awsclient.EC2API, instanceID string, timeout, pollInterval time.Duration) (types.Instance, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}
	for {
		out, err := client.DescribeInstances(deadline, input)
		if err != nil {
			return types.Instance{}, err
		}
		if inst, found := findInstance(out, instanceID); found && inst.State != nil && inst.State.Name == types.InstanceStateNameRunning {
			return inst, nil
		}
		select {
		case <-deadline.Done():
			return types.Instance{}, fmt.Errorf("timed out waiting for instance %s to reach running", instanceID)
		case <-time.After(pollInterval):
		}
	}
}

func findInstance(out *ec2.DescribeInstancesOutput, instanceID string) (types.Instance, bool) {
	for _, reservation := range out.Reservations {
		for _, inst := range reservation.Instances {
			if aws.ToString(inst.InstanceId) == instanceID {
				return inst, true
			}
		}
	}
	return types.Instance{}, false
}
