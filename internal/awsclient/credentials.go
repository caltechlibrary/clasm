package awsclient

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSAPI covers the one STS method awsops uses: GetCallerIdentity, for the
// startup credential check.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// NewSTSClient constructs an STS client from the SDK's default
// credential chain.
func NewSTSClient(ctx context.Context, region string) (STSAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return sts.NewFromConfig(cfg), nil
}

// CheckCredentials calls sts:GetCallerIdentity, retrying on throttling
// errors, and fails fast with a clear message otherwise -- this is
// awsops' startup credential check, replacing ec2_ami_manager.bash's
// check_dependencies AWS CLI/jq checks (there's no external binary to
// check for anymore).
func CheckCredentials(ctx context.Context, client STSAPI) (string, error) {
	out, err := callWithBackoff(ctx, 3, func() (*sts.GetCallerIdentityOutput, error) {
		return client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	})
	if err != nil {
		return "", fmt.Errorf("could not resolve AWS credentials (sts:GetCallerIdentity failed): %w", err)
	}
	return aws.ToString(out.Account), nil
}
