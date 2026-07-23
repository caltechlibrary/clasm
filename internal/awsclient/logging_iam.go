package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/debuglog"
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

func (w *loggingIAMClient) ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return logAWSCall(w.dl, "IAM.ListAttachedRolePolicies", w.region, params, func() (*iam.ListAttachedRolePoliciesOutput, error) {
		return w.inner.ListAttachedRolePolicies(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListPolicies(ctx context.Context, params *iam.ListPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListPoliciesOutput, error) {
	return logAWSCall(w.dl, "IAM.ListPolicies", w.region, params, func() (*iam.ListPoliciesOutput, error) {
		return w.inner.ListPolicies(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListRoleTags(ctx context.Context, params *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	return logAWSCall(w.dl, "IAM.ListRoleTags", w.region, params, func() (*iam.ListRoleTagsOutput, error) {
		return w.inner.ListRoleTags(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListInstanceProfileTags(ctx context.Context, params *iam.ListInstanceProfileTagsInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error) {
	return logAWSCall(w.dl, "IAM.ListInstanceProfileTags", w.region, params, func() (*iam.ListInstanceProfileTagsOutput, error) {
		return w.inner.ListInstanceProfileTags(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListPolicyTags(ctx context.Context, params *iam.ListPolicyTagsInput, optFns ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error) {
	return logAWSCall(w.dl, "IAM.ListPolicyTags", w.region, params, func() (*iam.ListPolicyTagsOutput, error) {
		return w.inner.ListPolicyTags(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) TagRole(ctx context.Context, params *iam.TagRoleInput, optFns ...func(*iam.Options)) (*iam.TagRoleOutput, error) {
	return logAWSCall(w.dl, "IAM.TagRole", w.region, params, func() (*iam.TagRoleOutput, error) {
		return w.inner.TagRole(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) UntagRole(ctx context.Context, params *iam.UntagRoleInput, optFns ...func(*iam.Options)) (*iam.UntagRoleOutput, error) {
	return logAWSCall(w.dl, "IAM.UntagRole", w.region, params, func() (*iam.UntagRoleOutput, error) {
		return w.inner.UntagRole(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) TagInstanceProfile(ctx context.Context, params *iam.TagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "IAM.TagInstanceProfile", w.region, params, func() (*iam.TagInstanceProfileOutput, error) {
		return w.inner.TagInstanceProfile(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) UntagInstanceProfile(ctx context.Context, params *iam.UntagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.UntagInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "IAM.UntagInstanceProfile", w.region, params, func() (*iam.UntagInstanceProfileOutput, error) {
		return w.inner.UntagInstanceProfile(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) TagPolicy(ctx context.Context, params *iam.TagPolicyInput, optFns ...func(*iam.Options)) (*iam.TagPolicyOutput, error) {
	return logAWSCall(w.dl, "IAM.TagPolicy", w.region, params, func() (*iam.TagPolicyOutput, error) {
		return w.inner.TagPolicy(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) UntagPolicy(ctx context.Context, params *iam.UntagPolicyInput, optFns ...func(*iam.Options)) (*iam.UntagPolicyOutput, error) {
	return logAWSCall(w.dl, "IAM.UntagPolicy", w.region, params, func() (*iam.UntagPolicyOutput, error) {
		return w.inner.UntagPolicy(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return logAWSCall(w.dl, "IAM.GetRole", w.region, params, func() (*iam.GetRoleOutput, error) {
		return w.inner.GetRole(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	return logAWSCall(w.dl, "IAM.ListRolePolicies", w.region, params, func() (*iam.ListRolePoliciesOutput, error) {
		return w.inner.ListRolePolicies(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) GetRolePolicy(ctx context.Context, params *iam.GetRolePolicyInput, optFns ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	return logAWSCall(w.dl, "IAM.GetRolePolicy", w.region, params, func() (*iam.GetRolePolicyOutput, error) {
		return w.inner.GetRolePolicy(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) GetPolicy(ctx context.Context, params *iam.GetPolicyInput, optFns ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	return logAWSCall(w.dl, "IAM.GetPolicy", w.region, params, func() (*iam.GetPolicyOutput, error) {
		return w.inner.GetPolicy(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) GetPolicyVersion(ctx context.Context, params *iam.GetPolicyVersionInput, optFns ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	return logAWSCall(w.dl, "IAM.GetPolicyVersion", w.region, params, func() (*iam.GetPolicyVersionOutput, error) {
		return w.inner.GetPolicyVersion(ctx, params, optFns...)
	})
}

func (w *loggingIAMClient) GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "IAM.GetInstanceProfile", w.region, params, func() (*iam.GetInstanceProfileOutput, error) {
		return w.inner.GetInstanceProfile(ctx, params, optFns...)
	})
}
