package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// decodePolicyDocument URL-decodes an IAM policy document (AWS returns
// AssumeRolePolicyDocument, PolicyVersion.Document, and
// GetRolePolicyOutput.PolicyDocument all URL-encoded per RFC 3986 --
// confirmed live against a real account, 2026-07-23, for all three)
// and pretty-prints the result as JSON. Falls back to the plain decoded
// text, without erroring, if the decoded content isn't valid JSON --
// showing the raw text is still useful, and failing the whole detail
// view over a cosmetic formatting step would be the wrong trade-off.
// Only malformed URL-encoding itself is a real error.
func decodePolicyDocument(encoded string) (string, error) {
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(decoded), "", "  "); err != nil {
		return decoded, nil
	}
	return buf.String(), nil
}

// IAMPolicyRef names one policy attached to a role, by name and ARN --
// enough to offer it in a "view this policy's document" picker without
// fetching every attached policy's content upfront.
type IAMPolicyRef struct {
	Name string
	ARN  string
}

// IAMRoleDetail is the full detail for one IAM role (DESIGN.md, "IAM
// Profile & Role Management Domain"; PLAN.md Phase 20.38). Policy
// *documents* aren't fetched here -- only names/ARNs -- so opening a
// role's detail doesn't cost one API call per attached/inline policy;
// fetchAttachedPolicyDocument/fetchInlinePolicyDocument fetch a specific
// policy's content on demand, once the operator picks one to inspect.
type IAMRoleDetail struct {
	Name                 string
	CreateDate           time.Time
	Tags                 map[string]string
	TrustPolicy          string
	AttachedPolicies     []IAMPolicyRef
	InlinePolicyNames    []string
	SSMCapable           bool
	ReferencedByProfiles []string
}

// fetchIAMRoleDetail assembles roleName's full detail: a single GetRole
// call covers the trust policy and tags together (both URL-encoded/
// plain respectively, confirmed live -- GetRole's response includes
// Tags, unlike ListRoles), then ListAttachedRolePolicies/ListRolePolicies
// for policy names, roleHasSSMPermissions (Phase 20.33) for SSM
// capability, and a cheap cross-reference against every instance
// profile's already-inline Roles list (listInstanceProfiles,
// resource_lists.go -- no per-profile tag fetch needed for this,
// unlike inventory.ListIAMInstanceProfileSummaries) to find which
// profiles reference this role. Deliberately does not look up which
// running instances use those profiles (deferred, per the 2026-07-23
// scoping discussion -- DESIGN.md's "best-effort" cross-reference is
// split into a cheap half shipped now and a costlier half deferred).
func fetchIAMRoleDetail(ctx context.Context, client awsclient.IAMAPI, roleName string) (IAMRoleDetail, error) {
	getOut, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return IAMRoleDetail{}, err
	}
	role := getOut.Role

	trustPolicy, err := decodePolicyDocument(aws.ToString(role.AssumeRolePolicyDocument))
	if err != nil {
		return IAMRoleDetail{}, err
	}

	attachedOut, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
	if err != nil {
		return IAMRoleDetail{}, err
	}
	attached := make([]IAMPolicyRef, 0, len(attachedOut.AttachedPolicies))
	for _, p := range attachedOut.AttachedPolicies {
		attached = append(attached, IAMPolicyRef{Name: aws.ToString(p.PolicyName), ARN: aws.ToString(p.PolicyArn)})
	}

	inlineOut, err := client.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{RoleName: aws.String(roleName)})
	if err != nil {
		return IAMRoleDetail{}, err
	}

	capable, err := roleHasSSMPermissions(ctx, client, roleName)
	if err != nil {
		return IAMRoleDetail{}, err
	}

	profiles, err := listInstanceProfiles(ctx, client)
	if err != nil {
		return IAMRoleDetail{}, err
	}
	var referencedBy []string
	for _, p := range profiles {
		if slices.Contains(p.Roles, roleName) {
			referencedBy = append(referencedBy, p.Name)
		}
	}

	return IAMRoleDetail{
		Name:                 roleName,
		CreateDate:           aws.ToTime(role.CreateDate),
		Tags:                 iamTagsToMap(role.Tags),
		TrustPolicy:          trustPolicy,
		AttachedPolicies:     attached,
		InlinePolicyNames:    inlineOut.PolicyNames,
		SSMCapable:           capable,
		ReferencedByProfiles: referencedBy,
	}, nil
}

// fetchAttachedPolicyDocument fetches a managed policy's current
// default-version document, decoded -- GetPolicy first (to resolve
// DefaultVersionId), then GetPolicyVersion for the actual document.
func fetchAttachedPolicyDocument(ctx context.Context, client awsclient.IAMAPI, policyARN string) (string, error) {
	getOut, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(policyARN)})
	if err != nil {
		return "", err
	}
	verOut, err := client.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
		PolicyArn: aws.String(policyARN),
		VersionId: getOut.Policy.DefaultVersionId,
	})
	if err != nil {
		return "", err
	}
	return decodePolicyDocument(aws.ToString(verOut.PolicyVersion.Document))
}

// fetchInlinePolicyDocument fetches roleName's inline policyName's
// document, decoded.
func fetchInlinePolicyDocument(ctx context.Context, client awsclient.IAMAPI, roleName, policyName string) (string, error) {
	out, err := client.GetRolePolicy(ctx, &iam.GetRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		return "", err
	}
	return decodePolicyDocument(aws.ToString(out.PolicyDocument))
}

// displayIAMRoleDetail writes detail's sections to w in a fixed order:
// identity/SSM-capability, tags, trust policy, attached policies, inline
// policies, and the "referenced by instance profiles" cross-reference.
// Each list-shaped section shows "(none)" when empty rather than an
// empty heading, matching displayTags' own "(no tags)" convention
// (manage_tags.go).
func displayIAMRoleDetail(w io.Writer, detail IAMRoleDetail) {
	fmt.Fprintf(w, "\nRole: %s\n", detail.Name)
	fmt.Fprintf(w, "Created: %s\n", detail.CreateDate.Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "SSM-capable: %s\n", yesNo(detail.SSMCapable))

	fmt.Fprintln(w, "\nTags:")
	keys := sortedKeys(detail.Tags)
	if len(keys) == 0 {
		fmt.Fprintln(w, "  (no tags)")
	}
	for _, k := range keys {
		fmt.Fprintf(w, "  %s = %s\n", k, detail.Tags[k])
	}

	fmt.Fprintln(w, "\nTrust Policy:")
	fmt.Fprintln(w, detail.TrustPolicy)

	fmt.Fprintln(w, "\nAttached Managed Policies:")
	if len(detail.AttachedPolicies) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, p := range detail.AttachedPolicies {
		fmt.Fprintf(w, "  %s (%s)\n", p.Name, p.ARN)
	}

	fmt.Fprintln(w, "\nInline Policies:")
	if len(detail.InlinePolicyNames) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, n := range detail.InlinePolicyNames {
		fmt.Fprintf(w, "  %s\n", n)
	}

	fmt.Fprintln(w, "\nReferenced by Instance Profiles:")
	if len(detail.ReferencedByProfiles) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, p := range detail.ReferencedByProfiles {
		fmt.Fprintf(w, "  %s\n", p)
	}
}

// yesNo renders a bool as "yes"/"no", matching this package's other
// boolean-ish output (internal/ui's own yesNo does the same for List-tier
// columns; this is the plain-text-output equivalent).
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// policyDocChoice is one entry in runPolicyDocLoop's "view a policy's
// document" picker -- attached and inline policies need different
// fetch calls (fetchAttachedPolicyDocument vs. fetchInlinePolicyDocument),
// so each choice carries what its own fetch needs.
type policyDocChoice struct {
	label    string
	isInline bool
	roleName string
	name     string
	arn      string
}

// policyDocChoices builds runPolicyDocLoop's choice list from detail's
// already-fetched policy names/ARNs (no API calls here -- those happen
// only once a specific policy is picked).
func policyDocChoices(detail IAMRoleDetail) []policyDocChoice {
	choices := make([]policyDocChoice, 0, len(detail.AttachedPolicies)+len(detail.InlinePolicyNames))
	for _, p := range detail.AttachedPolicies {
		choices = append(choices, policyDocChoice{label: "Attached: " + p.Name, name: p.Name, arn: p.ARN})
	}
	for _, n := range detail.InlinePolicyNames {
		choices = append(choices, policyDocChoice{label: "Inline: " + n, isInline: true, roleName: detail.Name, name: n})
	}
	return choices
}

// runPolicyDocLoop is ViewIAMRoleDetail's drill-down sub-action: pick
// one of detail's already-listed attached/inline policies, fetch and
// display its document, and repeat until the operator cancels ('q') or
// ctx is cancelled -- same loop shape as manageTagsForResource
// (manage_tags.go), pausing for acknowledgment after both errors and
// successful output before the next redraw (DECISIONS.md, "Pause for
// acknowledgment before every menu-loop redraw"). A role with no
// attached or inline policies at all has nothing to drill into, so this
// returns immediately rather than showing an empty picker. Unlike the
// role picker itself (Picker-tier, tui.RunPicker, not pipe-testable),
// this loop only uses a Menu-tier huh.Select (pickComparable), so it is
// fully pipe-testable.
func runPolicyDocLoop(ctx context.Context, w io.Writer, client awsclient.IAMAPI, detail IAMRoleDetail, menuInput io.Reader, menuOutput io.Writer) error {
	choices := policyDocChoices(detail)
	if len(choices) == 0 {
		return nil
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		choice, err := pickComparable(w, "View a policy's document", "", hintCancel, choices, func(c policyDocChoice) string { return c.label }, menuInput, menuOutput)
		if err != nil {
			return cancelledIsNil(w, err)
		}

		var doc string
		if choice.isInline {
			doc, err = fetchInlinePolicyDocument(ctx, client, choice.roleName, choice.name)
		} else {
			doc, err = fetchAttachedPolicyDocument(ctx, client, choice.arn)
		}
		if err != nil {
			fmt.Fprintf(w, "Error: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
			continue
		}
		fmt.Fprintf(w, "\nPolicy: %s\n%s\n", choice.name, doc)
		pauseForAcknowledgment(menuInput, menuOutput)
	}
}

// IAMRoleRef names one role attached to an instance profile, along with
// its SSM-capability -- enough for the Instance Profile detail view,
// without duplicating the role's full detail (trust policy, attached/
// inline policies) here; the operator uses View Role Detail for that.
type IAMRoleRef struct {
	Name       string
	SSMCapable bool
}

// IAMInstanceProfileDetail is the full detail for one IAM instance
// profile (DESIGN.md, "IAM Profile & Role Management Domain"; PLAN.md
// Phase 20.38) -- deliberately simpler than IAMRoleDetail: an instance
// profile has no trust policy or attached/inline policies of its own
// (those belong to its role(s)), so this just names the contained
// role(s) and each one's SSM-capability.
type IAMInstanceProfileDetail struct {
	Name       string
	CreateDate time.Time
	Tags       map[string]string
	Roles      []IAMRoleRef
}

// fetchIAMInstanceProfileDetail assembles profileName's full detail: a
// single GetInstanceProfile call covers Tags and the contained Roles
// together (confirmed live, 2026-07-22 -- GetInstanceProfile, like
// GetRole, includes Tags unlike the List* calls), then
// roleHasSSMPermissions (Phase 20.33) per contained role.
func fetchIAMInstanceProfileDetail(ctx context.Context, client awsclient.IAMAPI, profileName string) (IAMInstanceProfileDetail, error) {
	out, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
	if err != nil {
		return IAMInstanceProfileDetail{}, err
	}
	profile := out.InstanceProfile

	roles := make([]IAMRoleRef, 0, len(profile.Roles))
	for _, r := range profile.Roles {
		name := aws.ToString(r.RoleName)
		capable, err := roleHasSSMPermissions(ctx, client, name)
		if err != nil {
			return IAMInstanceProfileDetail{}, err
		}
		roles = append(roles, IAMRoleRef{Name: name, SSMCapable: capable})
	}

	return IAMInstanceProfileDetail{
		Name:       aws.ToString(profile.InstanceProfileName),
		CreateDate: aws.ToTime(profile.CreateDate),
		Tags:       iamTagsToMap(profile.Tags),
		Roles:      roles,
	}, nil
}

// displayIAMInstanceProfileDetail writes detail's sections to w --
// same shape as displayIAMRoleDetail, minus the sections that don't
// apply to an instance profile (trust policy, attached/inline policies).
func displayIAMInstanceProfileDetail(w io.Writer, detail IAMInstanceProfileDetail) {
	fmt.Fprintf(w, "\nInstance Profile: %s\n", detail.Name)
	fmt.Fprintf(w, "Created: %s\n", detail.CreateDate.Format("2006-01-02 15:04"))

	fmt.Fprintln(w, "\nTags:")
	keys := sortedKeys(detail.Tags)
	if len(keys) == 0 {
		fmt.Fprintln(w, "  (no tags)")
	}
	for _, k := range keys {
		fmt.Fprintf(w, "  %s = %s\n", k, detail.Tags[k])
	}

	fmt.Fprintln(w, "\nRoles:")
	if len(detail.Roles) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, r := range detail.Roles {
		fmt.Fprintf(w, "  %s (SSM-capable: %s)\n", r.Name, yesNo(r.SSMCapable))
	}
	if len(detail.Roles) > 0 {
		fmt.Fprintln(w, "\nFor trust policy and attached/inline policies, use View Role Detail on the role(s) above.")
	}
}

// ViewIAMInstanceProfileDetail runs the IAM domain's "View Instance
// Profile Detail" action: pick a profile, fetch and display its full
// detail. No policy-document drill-down here -- an instance profile has
// no policies of its own to drill into.
func ViewIAMInstanceProfileDetail(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return viewIAMInstanceProfileDetail(ctx, w, client, originTag)
}

// viewIAMInstanceProfileDetail is ViewIAMInstanceProfileDetail's
// testable core for the one path reachable before pickIAMInstanceProfile
// (Picker-tier, not pipe-testable) -- same accepted limitation as
// viewIAMRoleDetail.
func viewIAMInstanceProfileDetail(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	profiles, err := inventory.ListIAMInstanceProfileSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Fprintln(w, "No IAM instance profiles found.")
		return nil
	}
	profile, err := pickIAMInstanceProfile(ctx, "Select an instance profile to view", "", profiles)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	detail, err := fetchIAMInstanceProfileDetail(ctx, client, profile.Name)
	if err != nil {
		return err
	}
	displayIAMInstanceProfileDetail(w, detail)
	return nil
}

// ViewIAMRoleDetail runs the IAM domain's "View Role Detail" action
// (DESIGN.md, "IAM Profile & Role Management Domain"; PLAN.md Phase
// 20.38): pick a role, fetch and display its full detail, then offer
// the policy-document drill-down loop.
func ViewIAMRoleDetail(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return viewIAMRoleDetail(ctx, w, client, originTag, nil, nil)
}

// viewIAMRoleDetail is ViewIAMRoleDetail's testable core: menuInput/
// menuOutput are nil in production and supplied by tests to drive
// runPolicyDocLoop's Menu-tier picker through its accessible-mode path.
// pickIAMRole itself (Picker-tier, tui.RunPicker) can't be pipe-tested --
// the same accepted limitation as manageResourceTags/showAllTags -- so
// only the "no roles found" early return is exercised by an automated
// test; the rest is covered by fetchIAMRoleDetail/displayIAMRoleDetail/
// runPolicyDocLoop's own direct tests.
func viewIAMRoleDetail(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
	roles, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		fmt.Fprintln(w, "No IAM roles found.")
		return nil
	}
	role, err := pickIAMRole(ctx, "Select a role to view", "", roles)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	detail, err := fetchIAMRoleDetail(ctx, client, role.Name)
	if err != nil {
		return err
	}
	displayIAMRoleDetail(w, detail)

	return runPolicyDocLoop(ctx, w, client, detail, menuInput, menuOutput)
}
