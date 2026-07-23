package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
)

// OriginUnset is shown in the IAM domain's browse lists for a resource
// whose Origin tag isn't set -- itself informative, signaling that
// nobody's made a categorization call on this resource yet (DESIGN.md,
// "IAM Profile & Role Management Domain"). Deliberately not a fixed
// multi-way category (DLD/IMSS/AWS/Unknown): clasm has no reliable way
// to distinguish those before the tagging vocabulary is settled, so it
// shows the tag's literal value instead of guessing at one.
const OriginUnset = "(unset)"

// iamTagsToMap converts IAM's tag shape to a plain map, mirroring
// internal/workflow/manage_tags.go's tagsToMap (EC2-typed) -- kept
// separate since Go's type system doesn't let the two SDKs'
// otherwise-identical Tag structs share one conversion function.
func iamTagsToMap(tags []iamtypes.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// ResolveOrigin returns tags[originTagKey], or OriginUnset if the tag is
// absent or present-but-empty -- there's no meaningful difference
// between "not there" and "there but blank" for display purposes.
func ResolveOrigin(tags map[string]string, originTagKey string) string {
	if v := tags[originTagKey]; v != "" {
		return v
	}
	return OriginUnset
}

// IsDLDOwned reports whether tags carries the configured Origin tag's
// DLD-recognized value. Used only to gate policy-content mutations
// (attach/detach a managed policy, edit a trust policy, delete) --
// tagging itself is never gated by this (DECISIONS.md, "IAM Profile &
// Role Management: Origin tag revision..."). Always false when
// originTag.DLDValue is unset ("no value is recognized as DLD-owned
// yet"), regardless of what any resource is actually tagged.
func IsDLDOwned(tags map[string]string, originTag config.OriginTagConfig) bool {
	if originTag.DLDValue == "" {
		return false
	}
	return tags[originTag.Key] == originTag.DLDValue
}

// ErrNotDLDOwned is wrapped by RequireDLDOwned's returned error.
var ErrNotDLDOwned = errors.New("not recognized as DLD-owned")

// RequireDLDOwned is the read-only guard for any future action that
// would change an IAM resource's actual permissions -- attach/detach a
// managed policy, edit a trust policy, delete (DESIGN.md, "IAM Profile
// & Role Management Domain"; DECISIONS.md, "IAM Profile & Role
// Management: Origin tag revision..."). Returns nil iff tags carries
// the configured Origin tag's DLD-recognized value (via IsDLDOwned),
// otherwise a clear error naming kind and name (e.g. `role
// "imss-crowdstrike-agent-role"`), wrapping ErrNotDLDOwned so callers
// can match it with errors.Is.
//
// Deliberately does NOT gate tagging itself: tagging must remain
// possible on IMSS-/AWS-owned resources too, for support-contact
// recording, so a call site that only sets/edits a tag must never call
// this. No caller exists yet -- Phases 20.36-20.39 don't add any action
// that mutates an existing role's/profile's/policy's actual
// permissions, only discovery, tagging (exempt), and creating brand-new
// roles from templates. This is deliberately built ahead of that need
// so the decision (tagging exempt, always-refuse-until-recognized, no
// hardcoded vocabulary) is captured once here rather than re-derived
// when a real mutating action is eventually designed.
func RequireDLDOwned(tags map[string]string, originTag config.OriginTagConfig, kind, name string) error {
	if IsDLDOwned(tags, originTag) {
		return nil
	}
	return fmt.Errorf("%s %q is not recognized as DLD-owned (its %q tag must equal the configured value): %w", kind, name, originTag.Key, ErrNotDLDOwned)
}

// IAMRoleSummary is one row in the IAM domain's Roles list. SSM
// capability isn't included here -- it's a workflow-layer concern
// (internal/workflow/ssm_iam_check.go's roleHasSSMPermissions),
// layered on top of this basic listing rather than fetched here, to
// keep this package's IAM support limited to listing/shaping AWS data
// (matching Image/Instance's own scope), not IAM-policy interpretation.
type IAMRoleSummary struct {
	Name       string
	CreateDate time.Time
	Origin     string
	DLDOwned   bool
}

// ListIAMRoleSummaries lists every IAM role in the account, resolving
// each one's Origin tag and DLD-ownership (per originTag), sorted by
// CreateDate descending -- answers "what's recently added" directly
// from the IAM API, no separate tracking mechanism.
func ListIAMRoleSummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMRoleSummary, error) {
	out, err := client.ListRoles(ctx, &iam.ListRolesInput{})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMRoleSummary, 0, len(out.Roles))
	for _, r := range out.Roles {
		tags := iamTagsToMap(r.Tags)
		summaries = append(summaries, IAMRoleSummary{
			Name:       aws.ToString(r.RoleName),
			CreateDate: aws.ToTime(r.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreateDate.After(summaries[j].CreateDate)
	})
	return summaries, nil
}

// IAMInstanceProfileSummary is one row in the IAM domain's Instance
// Profiles list.
type IAMInstanceProfileSummary struct {
	Name       string
	CreateDate time.Time
	Origin     string
	DLDOwned   bool
}

// ListIAMInstanceProfileSummaries lists every IAM instance profile in
// the account, resolving each one's Origin tag and DLD-ownership,
// sorted by CreateDate descending -- same shape as ListIAMRoleSummaries.
func ListIAMInstanceProfileSummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMInstanceProfileSummary, error) {
	out, err := client.ListInstanceProfiles(ctx, &iam.ListInstanceProfilesInput{})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMInstanceProfileSummary, 0, len(out.InstanceProfiles))
	for _, p := range out.InstanceProfiles {
		tags := iamTagsToMap(p.Tags)
		summaries = append(summaries, IAMInstanceProfileSummary{
			Name:       aws.ToString(p.InstanceProfileName),
			CreateDate: aws.ToTime(p.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreateDate.After(summaries[j].CreateDate)
	})
	return summaries, nil
}

// IAMPolicySummary is one row in the IAM domain's Policies list.
type IAMPolicySummary struct {
	Name       string
	ARN        string
	CreateDate time.Time
	Origin     string
	DLDOwned   bool
}

// ListIAMPolicySummaries lists customer-managed ("Local" scope) IAM
// policies in the account -- AWS-managed policies are a separate,
// larger catalog offered behind its own toggle (DESIGN.md, "IAM Profile
// & Role Management Domain"), not fetched here -- resolving each one's
// Origin tag and DLD-ownership, sorted by CreateDate descending.
func ListIAMPolicySummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMPolicySummary, error) {
	out, err := client.ListPolicies(ctx, &iam.ListPoliciesInput{Scope: iamtypes.PolicyScopeTypeLocal})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMPolicySummary, 0, len(out.Policies))
	for _, p := range out.Policies {
		tags := iamTagsToMap(p.Tags)
		summaries = append(summaries, IAMPolicySummary{
			Name:       aws.ToString(p.PolicyName),
			ARN:        aws.ToString(p.Arn),
			CreateDate: aws.ToTime(p.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreateDate.After(summaries[j].CreateDate)
	})
	return summaries, nil
}
