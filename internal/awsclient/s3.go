package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3API covers the one S3 method awsops uses: HeadObject, for Backup
// Archive & Trim's independent verification step, called with the
// operator's own credentials -- distinct from the target instance's own
// IAM instance profile, which does the actual upload (see DESIGN.md,
// "Assumptions").
type S3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// NewS3Client constructs an S3 client from the SDK's default credential
// chain, scoped to the given region.
func NewS3Client(ctx context.Context, region string) (S3API, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg), nil
}
