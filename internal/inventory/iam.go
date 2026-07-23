package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
)

// iamTagFetchConcurrency bounds how many per-resource tag fetches
// (ListRoleTags/ListInstanceProfileTags/ListPolicyTags) run at once in
// fetchTagsConcurrently, below. An account can hold dozens to hundreds
// of IAM roles/profiles/policies (confirmed live, 2026-07-23 -- fetching
// tags one at a time made the IAM domain's discovery visibly slow to
// open, several seconds, in an account with many AWS-service-linked
// roles); unbounded concurrency risks IAM API throttling, so this caps
// it at a conservative, not-tuned value rather than firing every
// request at once.
const iamTagFetchConcurrency = 10

// fetchTagsConcurrently resolves tags for n resources concurrently,
// bounded to iamTagFetchConcurrency in flight at once -- the shared
// fan-out core for ListIAMRoleSummaries/ListIAMInstanceProfileSummaries/
// ListIAMPolicySummaries, mirroring inventory.ListImages' own
// concurrent per-region fan-out pattern (images.go), generalized here
// to per-resource-index rather than per-region. fetch(ctx, i) is called
// once for each index in [0,n); the returned slice is indexed the same
// way, so callers can zip it back against their own already-fetched
// list by position. The first error encountered (order not guaranteed
// under concurrency) is returned once every in-flight fetch has
// completed -- no goroutine is left running after this returns.
func fetchTagsConcurrently(ctx context.Context, n int, fetch func(ctx context.Context, i int) (map[string]string, error)) ([]map[string]string, error) {
	type result struct {
		index int
		tags  map[string]string
		err   error
	}
	results := make(chan result, n)
	sem := make(chan struct{}, iamTagFetchConcurrency)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tags, err := fetch(ctx, i)
			results <- result{index: i, tags: tags, err: err}
		}(i)
	}
	wg.Wait()
	close(results)

	tagsByIndex := make([]map[string]string, n)
	var firstErr error
	for res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		tagsByIndex[res.index] = res.tags
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return tagsByIndex, nil
}

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
	// Tags is the role's full tag set, kept at no extra API cost --
	// ListRoleTags is already called per role to resolve Origin above,
	// so retaining the full map costs nothing further and lets Tag
	// Management's "Show all tags"/"Manage tags" for IAM Role (PLAN.md
	// Phase 20.37) reuse this same summary rather than re-fetching.
	Tags map[string]string
}

// ListIAMRoleSummaries lists every IAM role in the account, resolving
// each one's Origin tag and DLD-ownership (per originTag), sorted by
// CreateDate descending -- answers "what's recently added" directly
// from the IAM API, no separate tracking mechanism.
//
// ListRoles does NOT return each role's Tags inline, despite the SDK's
// Role struct declaring a Tags field -- confirmed live against a real
// account, 2026-07-23 (DECISIONS.md, "ListRoles/ListInstanceProfiles/
// ListPolicies don't return tags inline"): that field is populated by
// other operations (e.g. GetRole), not this one. A separate
// ListRoleTags call per role is required.
func ListIAMRoleSummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMRoleSummary, error) {
	out, err := client.ListRoles(ctx, &iam.ListRolesInput{})
	if err != nil {
		return nil, err
	}
	tagsByIndex, err := fetchTagsConcurrently(ctx, len(out.Roles), func(ctx context.Context, i int) (map[string]string, error) {
		tagsOut, err := client.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: out.Roles[i].RoleName})
		if err != nil {
			return nil, err
		}
		return iamTagsToMap(tagsOut.Tags), nil
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMRoleSummary, 0, len(out.Roles))
	for i, r := range out.Roles {
		tags := tagsByIndex[i]
		summaries = append(summaries, IAMRoleSummary{
			Name:       aws.ToString(r.RoleName),
			CreateDate: aws.ToTime(r.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
			Tags:       tags,
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
	// Tags is the profile's full tag set -- see IAMRoleSummary.Tags' doc
	// comment.
	Tags map[string]string
	// RoleNames is the role(s) this instance profile contains,
	// populated at no extra API cost -- ListInstanceProfiles already
	// returns each profile's attached Roles inline (unlike Tags, which
	// requires a separate per-resource call). Used by the Role detail
	// view's "referenced by instance profiles" cross-reference
	// (PLAN.md Phase 20.38).
	RoleNames []string
}

// ListIAMInstanceProfileSummaries lists every IAM instance profile in
// the account, resolving each one's Origin tag and DLD-ownership,
// sorted by CreateDate descending -- same shape as ListIAMRoleSummaries,
// including the same ListInstanceProfiles-doesn't-return-Tags-inline gap
// (a separate ListInstanceProfileTags call per profile is required).
func ListIAMInstanceProfileSummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMInstanceProfileSummary, error) {
	out, err := client.ListInstanceProfiles(ctx, &iam.ListInstanceProfilesInput{})
	if err != nil {
		return nil, err
	}
	tagsByIndex, err := fetchTagsConcurrently(ctx, len(out.InstanceProfiles), func(ctx context.Context, i int) (map[string]string, error) {
		tagsOut, err := client.ListInstanceProfileTags(ctx, &iam.ListInstanceProfileTagsInput{InstanceProfileName: out.InstanceProfiles[i].InstanceProfileName})
		if err != nil {
			return nil, err
		}
		return iamTagsToMap(tagsOut.Tags), nil
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMInstanceProfileSummary, 0, len(out.InstanceProfiles))
	for i, p := range out.InstanceProfiles {
		tags := tagsByIndex[i]
		roleNames := make([]string, 0, len(p.Roles))
		for _, r := range p.Roles {
			roleNames = append(roleNames, aws.ToString(r.RoleName))
		}
		summaries = append(summaries, IAMInstanceProfileSummary{
			Name:       aws.ToString(p.InstanceProfileName),
			CreateDate: aws.ToTime(p.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
			Tags:       tags,
			RoleNames:  roleNames,
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
	// Tags is the policy's full tag set -- see IAMRoleSummary.Tags' doc
	// comment.
	Tags map[string]string
}

// ListIAMPolicySummaries lists customer-managed ("Local" scope) IAM
// policies in the account -- AWS-managed policies are a separate,
// larger catalog offered behind its own toggle (DESIGN.md, "IAM Profile
// & Role Management Domain"), not fetched here -- resolving each one's
// Origin tag and DLD-ownership, sorted by CreateDate descending. Same
// ListPolicies-doesn't-return-Tags-inline gap as the other two list
// functions -- a separate ListPolicyTags call per policy is required.
func ListIAMPolicySummaries(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) ([]IAMPolicySummary, error) {
	out, err := client.ListPolicies(ctx, &iam.ListPoliciesInput{Scope: iamtypes.PolicyScopeTypeLocal})
	if err != nil {
		return nil, err
	}
	tagsByIndex, err := fetchTagsConcurrently(ctx, len(out.Policies), func(ctx context.Context, i int) (map[string]string, error) {
		tagsOut, err := client.ListPolicyTags(ctx, &iam.ListPolicyTagsInput{PolicyArn: out.Policies[i].Arn})
		if err != nil {
			return nil, err
		}
		return iamTagsToMap(tagsOut.Tags), nil
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]IAMPolicySummary, 0, len(out.Policies))
	for i, p := range out.Policies {
		tags := tagsByIndex[i]
		summaries = append(summaries, IAMPolicySummary{
			Name:       aws.ToString(p.PolicyName),
			ARN:        aws.ToString(p.Arn),
			CreateDate: aws.ToTime(p.CreateDate),
			Origin:     ResolveOrigin(tags, originTag.Key),
			DLDOwned:   IsDLDOwned(tags, originTag),
			Tags:       tags,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreateDate.After(summaries[j].CreateDate)
	})
	return summaries, nil
}
