package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// IAMAPI covers the IAM SDK methods awsops uses for the "IAM instance
// profile" launch parameter (DESIGN.md, Feature 2): listing existing
// instance profiles and roles to pick from, and creating a new instance
// profile attached to an existing role for operators who don't have one
// yet (see DECISIONS.md, "Support picking or creating an IAM instance
// profile from within awsops"). IAM's control plane is a global service,
// like STS -- one client suffices for the whole account.
type IAMAPI interface {
	ListInstanceProfiles(ctx context.Context, params *iam.ListInstanceProfilesInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error)
	ListRoles(ctx context.Context, params *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error)
	AddRoleToInstanceProfile(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error)
	// ListAttachedRolePolicies supports roleHasSSMPermissions
	// (ssm_iam_check.go, DESIGN.md "SSM-Capable Instance Profile
	// Enforcement + Retrofit"): checking whether a role has
	// AmazonSSMManagedInstanceCore attached.
	ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
	// ListPolicies supports the IAM domain's Policies discovery view
	// (DESIGN.md, "IAM Profile & Role Management Domain") -- listing
	// customer-managed ("Local" scope) policies by default.
	ListPolicies(ctx context.Context, params *iam.ListPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListPoliciesOutput, error)
	// ListRoleTags/ListInstanceProfileTags/ListPolicyTags resolve each
	// IAM domain resource's Origin tag. Confirmed live against a real
	// account (2026-07-23) that ListRoles/ListInstanceProfiles/
	// ListPolicies do NOT return Tags inline despite their SDK response
	// types declaring a Tags field -- that field is populated by other
	// operations (e.g. GetRole), not these list calls. A per-resource
	// tag fetch is required; see DECISIONS.md, "ListRoles/
	// ListInstanceProfiles/ListPolicies don't return tags inline".
	ListRoleTags(ctx context.Context, params *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error)
	ListInstanceProfileTags(ctx context.Context, params *iam.ListInstanceProfileTagsInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error)
	ListPolicyTags(ctx context.Context, params *iam.ListPolicyTagsInput, optFns ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error)
	// TagRole/UntagRole, TagInstanceProfile/UntagInstanceProfile,
	// TagPolicy/UntagPolicy support the Tag Management domain's
	// extension to IAM resources (DESIGN.md, "IAM Profile & Role
	// Management Domain"; PLAN.md Phase 20.37) -- add/remove one tag at
	// a time, the same fine-grained shape as EC2's CreateTags/DeleteTags,
	// not a whole-set replace like S3's PutBucketTagging.
	TagRole(ctx context.Context, params *iam.TagRoleInput, optFns ...func(*iam.Options)) (*iam.TagRoleOutput, error)
	UntagRole(ctx context.Context, params *iam.UntagRoleInput, optFns ...func(*iam.Options)) (*iam.UntagRoleOutput, error)
	TagInstanceProfile(ctx context.Context, params *iam.TagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error)
	UntagInstanceProfile(ctx context.Context, params *iam.UntagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.UntagInstanceProfileOutput, error)
	TagPolicy(ctx context.Context, params *iam.TagPolicyInput, optFns ...func(*iam.Options)) (*iam.TagPolicyOutput, error)
	UntagPolicy(ctx context.Context, params *iam.UntagPolicyInput, optFns ...func(*iam.Options)) (*iam.UntagPolicyOutput, error)
}

// NewIAMClient constructs an IAM client from the SDK's default credential
// chain. region only selects the signing endpoint -- IAM's data is
// account-wide, not region-scoped.
func NewIAMClient(ctx context.Context, region string) (IAMAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return iam.NewFromConfig(cfg), nil
}
