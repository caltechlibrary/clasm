package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/caltechlibrary/awstools/internal/debuglog"
)

type loggingSTSClient struct {
	inner  STSAPI
	dl     *debuglog.DebugLog
	region string
}

// WrapSTS returns an STSAPI that logs every call to dl before
// delegating to client. A nil dl returns client unchanged.
func WrapSTS(client STSAPI, dl *debuglog.DebugLog, region string) STSAPI {
	if dl == nil {
		return client
	}
	return &loggingSTSClient{inner: client, dl: dl, region: region}
}

func (w *loggingSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return logAWSCall(w.dl, "STS.GetCallerIdentity", w.region, params, func() (*sts.GetCallerIdentityOutput, error) {
		return w.inner.GetCallerIdentity(ctx, params, optFns...)
	})
}
