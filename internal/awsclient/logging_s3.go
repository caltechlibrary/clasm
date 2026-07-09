package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/debuglog"
)

type loggingS3Client struct {
	inner  S3API
	dl     *debuglog.DebugLog
	region string
}

// WrapS3 returns an S3API that logs every call to dl before delegating
// to client. A nil dl returns client unchanged.
func WrapS3(client S3API, dl *debuglog.DebugLog, region string) S3API {
	if dl == nil {
		return client
	}
	return &loggingS3Client{inner: client, dl: dl, region: region}
}

func (w *loggingS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return logAWSCall(w.dl, "S3.HeadObject", w.region, params, func() (*s3.HeadObjectOutput, error) {
		return w.inner.HeadObject(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return logAWSCall(w.dl, "S3.HeadBucket", w.region, params, func() (*s3.HeadBucketOutput, error) {
		return w.inner.HeadBucket(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return logAWSCall(w.dl, "S3.GetBucketLocation", w.region, params, func() (*s3.GetBucketLocationOutput, error) {
		return w.inner.GetBucketLocation(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return logAWSCall(w.dl, "S3.ListBuckets", w.region, params, func() (*s3.ListBucketsOutput, error) {
		return w.inner.ListBuckets(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) GetBucketWebsite(ctx context.Context, params *s3.GetBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.GetBucketWebsiteOutput, error) {
	return logAWSCall(w.dl, "S3.GetBucketWebsite", w.region, params, func() (*s3.GetBucketWebsiteOutput, error) {
		return w.inner.GetBucketWebsite(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	return logAWSCall(w.dl, "S3.CreateBucket", w.region, params, func() (*s3.CreateBucketOutput, error) {
		return w.inner.CreateBucket(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	return logAWSCall(w.dl, "S3.PutPublicAccessBlock", w.region, params, func() (*s3.PutPublicAccessBlockOutput, error) {
		return w.inner.PutPublicAccessBlock(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) PutBucketWebsite(ctx context.Context, params *s3.PutBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.PutBucketWebsiteOutput, error) {
	return logAWSCall(w.dl, "S3.PutBucketWebsite", w.region, params, func() (*s3.PutBucketWebsiteOutput, error) {
		return w.inner.PutBucketWebsite(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return logAWSCall(w.dl, "S3.PutObject", w.region, params, func() (*s3.PutObjectOutput, error) {
		return w.inner.PutObject(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return logAWSCall(w.dl, "S3.ListObjectsV2", w.region, params, func() (*s3.ListObjectsV2Output, error) {
		return w.inner.ListObjectsV2(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return logAWSCall(w.dl, "S3.DeleteObject", w.region, params, func() (*s3.DeleteObjectOutput, error) {
		return w.inner.DeleteObject(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) PutBucketTagging(ctx context.Context, params *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error) {
	return logAWSCall(w.dl, "S3.PutBucketTagging", w.region, params, func() (*s3.PutBucketTaggingOutput, error) {
		return w.inner.PutBucketTagging(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	return logAWSCall(w.dl, "S3.GetBucketTagging", w.region, params, func() (*s3.GetBucketTaggingOutput, error) {
		return w.inner.GetBucketTagging(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) GetBucketLifecycleConfiguration(ctx context.Context, params *s3.GetBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	return logAWSCall(w.dl, "S3.GetBucketLifecycleConfiguration", w.region, params, func() (*s3.GetBucketLifecycleConfigurationOutput, error) {
		return w.inner.GetBucketLifecycleConfiguration(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) PutBucketLifecycleConfiguration(ctx context.Context, params *s3.PutBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
	return logAWSCall(w.dl, "S3.PutBucketLifecycleConfiguration", w.region, params, func() (*s3.PutBucketLifecycleConfigurationOutput, error) {
		return w.inner.PutBucketLifecycleConfiguration(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) DeleteBucketLifecycle(ctx context.Context, params *s3.DeleteBucketLifecycleInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketLifecycleOutput, error) {
	return logAWSCall(w.dl, "S3.DeleteBucketLifecycle", w.region, params, func() (*s3.DeleteBucketLifecycleOutput, error) {
		return w.inner.DeleteBucketLifecycle(ctx, params, optFns...)
	})
}

func (w *loggingS3Client) DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	return logAWSCall(w.dl, "S3.DeleteBucket", w.region, params, func() (*s3.DeleteBucketOutput, error) {
		return w.inner.DeleteBucket(ctx, params, optFns...)
	})
}
