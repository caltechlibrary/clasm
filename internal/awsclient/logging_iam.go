package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/awstools/internal/debuglog"
)

type loggingIAMClient struct {
	inner  IAMAPI
	dl     *debuglog.DebugLog
	region string
}

// WrapIAM returns an IAMAPI that logs every call to dl before delegating
// to client. A nil dl returns client unchanged.
func WrapIAM(client IAMAPI, dl *debuglog.DebugLog, region string) IAMAPI {
	if dl == nil {
		return client
	}
	return &loggingIAMClient{inner: client, dl: dl, region: region}
}

func (w *loggingIAMClient) ListInstanceProfiles(ctx context.Context, params *iam.ListInstanceProfilesInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error) {
	return logAWSCall(w.dl, "IAM.ListInstanceProfiles", w.region, params, func() (*iam.ListInstanceProfilesOutput, error) {
		return w.inner.ListInstanceProfiles(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListRoles(ctx context.Context, params *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return logAWSCall(w.dl, "IAM.ListRoles", w.region, params, func() (*iam.ListRolesOutput, error) {
		return w.inner.ListRoles(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "IAM.CreateInstanceProfile", w.region, params, func() (*iam.CreateInstanceProfileOutput, error) {
		return w.inner.CreateInstanceProfile(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) AddRoleToInstanceProfile(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "IAM.AddRoleToInstanceProfile", w.region, params, func() (*iam.AddRoleToInstanceProfileOutput, error) {
		return w.inner.AddRoleToInstanceProfile(ctx, params, optFns...)
	})
}
