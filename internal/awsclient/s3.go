package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3API covers the three S3 methods awsops uses, all called with the
// operator's own credentials -- distinct from the target instance's own
// IAM instance profile, which does the actual upload (see DESIGN.md,
// "Assumptions"): HeadObject for Backup Archive & Trim's independent
// verification step, HeadBucket for its upfront bucket-access preflight
// check, and GetBucketLocation to discover which region a bucket
// actually lives in before either of those (see DECISIONS.md, "Resolve
// a bucket's actual region before Backup Archive & Trim's access
// check").
type S3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
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
