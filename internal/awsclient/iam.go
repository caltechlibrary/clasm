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
	// GetRole supports the IAM Role detail view (DESIGN.md, "IAM Profile
	// & Role Management Domain"; PLAN.md Phase 20.38) -- a single,
	// dedicated fetch for the trust policy (AssumeRolePolicyDocument)
	// and tags of the one role being inspected, rather than reusing the
	// bulk ListRoles/ListRoleTags calls the discovery view already made
	// and discarded. GetRole's response includes both AssumeRolePolicyDocument
	// and Tags (confirmed live, 2026-07-23), so this single call covers
	// both.
	GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	// ListRolePolicies/GetRolePolicy support the Role detail view's
	// inline-policy listing/content. GetPolicy/GetPolicyVersion support
	// viewing an attached managed policy's content (DefaultVersionId,
	// then the actual document). All three policy-document-bearing
	// fields (AssumeRolePolicyDocument, PolicyVersion.Document,
	// GetRolePolicyOutput.PolicyDocument) are URL-encoded per RFC 3986 --
	// confirmed live for all three, 2026-07-23 -- see
	// internal/workflow/iam_detail.go's decodePolicyDocument.
	ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
	GetRolePolicy(ctx context.Context, params *iam.GetRolePolicyInput, optFns ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error)
	GetPolicy(ctx context.Context, params *iam.GetPolicyInput, optFns ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(ctx context.Context, params *iam.GetPolicyVersionInput, optFns ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error)
	// GetInstanceProfile supports the IAM Instance Profile detail view
	// (PLAN.md Phase 20.38) -- a single, dedicated fetch (Tags and Roles
	// both included, confirmed live 2026-07-22) rather than reusing the
	// bulk ListInstanceProfiles/ListInstanceProfileTags calls the
	// discovery view already made and discarded, mirroring GetRole's
	// role in the Role detail view.
	GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error)
	// CreateRole/CreatePolicy/AttachRolePolicy support Phase 20.39's
	// curated per-use-case role/policy creation templates -- reversing
	// the 2026-07-02 "never creates a role, only attaches an existing
	// one" scope, deliberately and only through curated templates (see
	// DECISIONS.md, "IAM Profile & Role Management: seven scoping
	// decisions, bundled into v0.0.5", Decision 2).
	CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	CreatePolicy(ctx context.Context, params *iam.CreatePolicyInput, optFns ...func(*iam.Options)) (*iam.CreatePolicyOutput, error)
	AttachRolePolicy(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)
	// DeleteRole/DeleteRolePolicy/DetachRolePolicy/DeletePolicy/
	// DeletePolicyVersion/ListPolicyVersions/ListEntitiesForPolicy
	// support Phase 20.40's Delete Role and Attach/Detach Policy
	// actions -- confirmed live against the vendored SDK's own doc
	// comments that DeleteRole requires inline policies deleted and
	// managed policies detached first (and the role removed from any
	// instance profile, which this project handles by refusing to
	// proceed rather than automating -- see DECISIONS.md), and that
	// DeletePolicy requires every non-default version deleted first via
	// DeletePolicyVersion, and detaching from every remaining entity
	// (checked via ListEntitiesForPolicy) before that.
	DeleteRole(ctx context.Context, params *iam.DeleteRoleInput, optFns ...func(*iam.Options)) (*iam.DeleteRoleOutput, error)
	DeleteRolePolicy(ctx context.Context, params *iam.DeleteRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error)
	DetachRolePolicy(ctx context.Context, params *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error)
	DeletePolicy(ctx context.Context, params *iam.DeletePolicyInput, optFns ...func(*iam.Options)) (*iam.DeletePolicyOutput, error)
	DeletePolicyVersion(ctx context.Context, params *iam.DeletePolicyVersionInput, optFns ...func(*iam.Options)) (*iam.DeletePolicyVersionOutput, error)
	ListPolicyVersions(ctx context.Context, params *iam.ListPolicyVersionsInput, optFns ...func(*iam.Options)) (*iam.ListPolicyVersionsOutput, error)
	ListEntitiesForPolicy(ctx context.Context, params *iam.ListEntitiesForPolicyInput, optFns ...func(*iam.Options)) (*iam.ListEntitiesForPolicyOutput, error)
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
