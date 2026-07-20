package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// awsAPIError builds a fake AWS API error with the given error code, for
// exercising isS3ErrorCode-style branches (NoSuchWebsiteConfiguration,
// NoSuchTagSet, NoSuchLifecycleConfiguration) without a real AWS call.
func awsAPIError(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func (f *fakeS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listBucketsErr != nil {
		return nil, f.listBucketsErr
	}
	return &s3.ListBucketsOutput{Buckets: f.buckets}, nil
}

func (f *fakeS3Client) CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	f.createBucketCalls = append(f.createBucketCalls, *params)
	if f.createBucketErr != nil {
		return nil, f.createBucketErr
	}
	return &s3.CreateBucketOutput{}, nil
}

func (f *fakeS3Client) PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	f.putPublicAccessBlockCalls = append(f.putPublicAccessBlockCalls, *params)
	if f.putPublicAccessBlockErr != nil {
		return nil, f.putPublicAccessBlockErr
	}
	return &s3.PutPublicAccessBlockOutput{}, nil
}

func (f *fakeS3Client) PutBucketTagging(ctx context.Context, params *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error) {
	f.putBucketTaggingCalls = append(f.putBucketTaggingCalls, *params)
	if f.putBucketTaggingErr != nil {
		return nil, f.putBucketTaggingErr
	}
	return &s3.PutBucketTaggingOutput{}, nil
}

func (f *fakeS3Client) GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	if f.getBucketTaggingErr != nil {
		return nil, f.getBucketTaggingErr
	}
	return &s3.GetBucketTaggingOutput{TagSet: f.tagSet}, nil
}

func (f *fakeS3Client) DeleteBucketTagging(ctx context.Context, params *s3.DeleteBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketTaggingOutput, error) {
	f.deleteBucketTaggingCalls = append(f.deleteBucketTaggingCalls, *params)
	if f.deleteBucketTaggingErr != nil {
		return nil, f.deleteBucketTaggingErr
	}
	return &s3.DeleteBucketTaggingOutput{}, nil
}

func (f *fakeS3Client) GetBucketWebsite(ctx context.Context, params *s3.GetBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.GetBucketWebsiteOutput, error) {
	if f.getBucketWebsiteErr != nil {
		return nil, f.getBucketWebsiteErr
	}
	if f.websiteIndexSuffix == "" {
		return nil, awsAPIError("NoSuchWebsiteConfiguration")
	}
	out := &s3.GetBucketWebsiteOutput{IndexDocument: &types.IndexDocument{Suffix: aws.String(f.websiteIndexSuffix)}}
	if f.websiteErrorKey != "" {
		out.ErrorDocument = &types.ErrorDocument{Key: aws.String(f.websiteErrorKey)}
	}
	return out, nil
}

func (f *fakeS3Client) PutBucketWebsite(ctx context.Context, params *s3.PutBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.PutBucketWebsiteOutput, error) {
	f.putBucketWebsiteCalls = append(f.putBucketWebsiteCalls, *params)
	if f.putBucketWebsiteErr != nil {
		return nil, f.putBucketWebsiteErr
	}
	return &s3.PutBucketWebsiteOutput{}, nil
}

func (f *fakeS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putObjectCalls = append(f.putObjectCalls, *params)
	if f.putObjectErr != nil {
		return nil, f.putObjectErr
	}
	return &s3.PutObjectOutput{}, nil
}

// GetObject returns the canned body for params.Key from getObjectContent,
// or a NoSuchKey-style error if the key isn't present -- mirrors
// HeadObject's not-found simulation above.
func (f *fakeS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.getObjectCalls = append(f.getObjectCalls, *params)
	if f.getObjectErr != nil {
		return nil, f.getObjectErr
	}
	body, ok := f.getObjectContent[aws.ToString(params.Key)]
	if !ok {
		return nil, errors.New("NoSuchKey: key does not exist")
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: aws.Int64(int64(len(body))),
	}, nil
}

func (f *fakeS3Client) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.deleteObjectCalls = append(f.deleteObjectCalls, *params)
	if f.deleteObjectErr != nil {
		return nil, f.deleteObjectErr
	}
	return &s3.DeleteObjectOutput{}, nil
}

// ListObjectsV2 paginates allObjects (filtered by Prefix) in pages of
// listObjectsPageSize, using the page's end index as a fake continuation
// token -- enough to exercise real pagination-following code without
// reproducing S3's actual opaque token format.
func (f *fakeS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.listObjectsV2Calls = append(f.listObjectsV2Calls, *params)
	if f.listObjectsV2Err != nil {
		return nil, f.listObjectsV2Err
	}

	prefix := aws.ToString(params.Prefix)
	var filtered []types.Object
	for _, o := range f.allObjects {
		if strings.HasPrefix(aws.ToString(o.Key), prefix) {
			filtered = append(filtered, o)
		}
	}

	pageSize := f.listObjectsPageSize
	if pageSize <= 0 {
		pageSize = len(filtered)
	}
	start := 0
	if tok := aws.ToString(params.ContinuationToken); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := min(start+pageSize, len(filtered))

	out := &s3.ListObjectsV2Output{Contents: filtered[start:end]}
	if end < len(filtered) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(strconv.Itoa(end))
	}
	return out, nil
}

func (f *fakeS3Client) GetBucketLifecycleConfiguration(ctx context.Context, params *s3.GetBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	if f.getBucketLifecycleErr != nil {
		return nil, f.getBucketLifecycleErr
	}
	return &s3.GetBucketLifecycleConfigurationOutput{Rules: f.lifecycleRules}, nil
}

func (f *fakeS3Client) PutBucketLifecycleConfiguration(ctx context.Context, params *s3.PutBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
	f.putBucketLifecycleCalls = append(f.putBucketLifecycleCalls, *params)
	if f.putBucketLifecycleErr != nil {
		return nil, f.putBucketLifecycleErr
	}
	return &s3.PutBucketLifecycleConfigurationOutput{}, nil
}

func (f *fakeS3Client) DeleteBucketLifecycle(ctx context.Context, params *s3.DeleteBucketLifecycleInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketLifecycleOutput, error) {
	f.deleteBucketLifecycleCalls = append(f.deleteBucketLifecycleCalls, *params)
	if f.deleteBucketLifecycleErr != nil {
		return nil, f.deleteBucketLifecycleErr
	}
	return &s3.DeleteBucketLifecycleOutput{}, nil
}

func (f *fakeS3Client) DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	f.deleteBucketCalls = append(f.deleteBucketCalls, *params)
	if f.deleteBucketErr != nil {
		return nil, f.deleteBucketErr
	}
	return &s3.DeleteBucketOutput{}, nil
}

// sameS3Client (backup_archive_test.go) already covers "one fake, any
// region"; newRegionS3Client additionally records which region each call
// was requested for, when a bucket_*_test.go needs to assert a workflow
// resolved the right region before touching bucket content.
func newRegionS3Client(byRegion map[string]awsclient.S3API) func(context.Context, string) (awsclient.S3API, error) {
	return func(_ context.Context, region string) (awsclient.S3API, error) {
		c, ok := byRegion[region]
		if !ok {
			return nil, fmt.Errorf("no client configured for region %s", region)
		}
		return c, nil
	}
}
