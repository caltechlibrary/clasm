package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// SSMAPI covers the SSM SDK methods awsops uses: the fstrim pre-snapshot
// step (Create AMI from Instance, Backup Archive & Trim), cloud-init AMI
// extraction (Show/Export Cloud-Init), and the backup upload/verify/
// delete sequence (Backup Archive & Trim).
type SSMAPI interface {
	SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
	DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
}

// NewSSMClient constructs a region-scoped SSM client from the SDK's
// default credential chain.
func NewSSMClient(ctx context.Context, region string) (SSMAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return ssm.NewFromConfig(cfg), nil
}
