package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/awstools/internal/debuglog"
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
