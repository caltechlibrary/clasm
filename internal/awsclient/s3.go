package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3API covers the S3 methods awsops uses, all called with the operator's
// own credentials -- distinct from the target instance's own IAM instance
// profile, which does the actual upload (see DESIGN.md, "Assumptions"):
// HeadObject for Backup Archive & Trim's independent verification step,
// HeadBucket for its upfront bucket-access preflight check, and
// GetBucketLocation to discover which region a bucket actually lives in
// before either of those (see DECISIONS.md, "Resolve a bucket's actual
// region before Backup Archive & Trim's access check"). The remaining
// methods support Phase 20's S3 domain (DESIGN.md, Features 17-21, 21.1):
// ListBuckets/GetBucketWebsite/GetBucketTagging for inventory; CreateBucket/
// PutPublicAccessBlock/PutBucketTagging for Create Bucket; PutBucketWebsite
// for Configure Static Website Hosting; PutObject/ListObjectsV2/DeleteObject
// for Sync Local Directory to Bucket and Browse/Manage Objects;
// GetBucketLifecycleConfiguration/PutBucketLifecycleConfiguration/
// DeleteBucketLifecycle for Manage Bucket Lifecycle Policies --
// DeleteBucketLifecycle added after real-AWS verification surfaced that
// PutBucketLifecycleConfiguration rejects an empty Rules list client-side
// (a required field), so clearing the last remaining rule must go through
// this separate operation instead (see DECISIONS.md). DeleteBucket
// supports Delete Bucket; Delete Bucket also reuses ListObjectsV2 to
// confirm a bucket is empty before calling it. GetObject supports the
// file manager's Download action (DESIGN.md 21.6, Phase 20.1) --
// completes Create/Update/Read/Delete parity, previously deferred in
// Phase 20. Not adding PutBucketPolicy -- only needed by the deferred
// public-read opt-out.
type S3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketWebsite(ctx context.Context, params *s3.GetBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.GetBucketWebsiteOutput, error)
	CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
	PutBucketWebsite(ctx context.Context, params *s3.PutBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.PutBucketWebsiteOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	PutBucketTagging(ctx context.Context, params *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error)
	GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
	GetBucketLifecycleConfiguration(ctx context.Context, params *s3.GetBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error)
	PutBucketLifecycleConfiguration(ctx context.Context, params *s3.PutBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error)
	DeleteBucketLifecycle(ctx context.Context, params *s3.DeleteBucketLifecycleInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketLifecycleOutput, error)
	DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
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
