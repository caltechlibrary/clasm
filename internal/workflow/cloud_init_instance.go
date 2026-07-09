package workflow

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// ShowCloudInitFromInstance reads the exact cloud-init/user-data that
// launched instanceID via ec2:DescribeInstanceAttribute -- free, instant,
// read-only. set is false (not an error) if no user-data was set at
// launch (see DESIGN.md, Feature 10).
func ShowCloudInitFromInstance(ctx context.Context, client awsclient.EC2API, instanceID string) (userData string, set bool, err error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(instanceID),
		Attribute:  types.InstanceAttributeNameUserData,
	})
	if err != nil {
		return "", false, err
	}
	if out.UserData == nil || aws.ToString(out.UserData.Value) == "" {
		return "", false, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(aws.ToString(out.UserData.Value))
	if err != nil {
		return "", false, fmt.Errorf("decoding user-data: %w", err)
	}
	return string(decoded), true, nil
}
