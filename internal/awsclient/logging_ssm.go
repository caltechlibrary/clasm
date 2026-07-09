package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/caltechlibrary/clasm/internal/debuglog"
)

type loggingSSMClient struct {
	inner  SSMAPI
	dl     *debuglog.DebugLog
	region string
}

// WrapSSM returns an SSMAPI that logs every call to dl before
// delegating to client. A nil dl returns client unchanged.
func WrapSSM(client SSMAPI, dl *debuglog.DebugLog, region string) SSMAPI {
	if dl == nil {
		return client
	}
	return &loggingSSMClient{inner: client, dl: dl, region: region}
}

func (w *loggingSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return logAWSCall(w.dl, "SSM.SendCommand", w.region, params, func() (*ssm.SendCommandOutput, error) {
		return w.inner.SendCommand(ctx, params, optFns...)
	})
}

func (w *loggingSSMClient) GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return logAWSCall(w.dl, "SSM.GetCommandInvocation", w.region, params, func() (*ssm.GetCommandInvocationOutput, error) {
		return w.inner.GetCommandInvocation(ctx, params, optFns...)
	})
}

func (w *loggingSSMClient) DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return logAWSCall(w.dl, "SSM.DescribeInstanceInformation", w.region, params, func() (*ssm.DescribeInstanceInformationOutput, error) {
		return w.inner.DescribeInstanceInformation(ctx, params, optFns...)
	})
}
