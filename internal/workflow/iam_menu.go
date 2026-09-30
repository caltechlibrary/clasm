package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// IAMActions bundles the IAM domain's menu entry points, mirroring
// TagMgmtActions/KeyMgmtActions' shape for the other domains
// (DESIGN.md, "IAM Profile & Role Management Domain"). Unlike the other
// domains, there is no Refresh field: each Show action fetches its own
// data fresh from the IAM API on every dispatch (IAM's ListRoles/
// ListInstanceProfiles/ListPolicies are single, account-wide, un-fanned
// calls, not per-region state worth caching between menu redraws).
type IAMActions struct {
	ShowRoles            func(ctx context.Context) error
	ShowInstanceProfiles func(ctx context.Context) error
	ShowPolicies         func(ctx context.Context) error
	// ViewRoleDetail/ViewInstanceProfileDetail are the detail-view
	// actions (PLAN.md Phase 20.38): pick one role/profile and show its
	// full detail (trust policy, attached/inline policies, tags,
	// SSM-capability, cross-references), matching Compute's own
	// "Show launch template detail" vs. "Show launch templates" split
	// between a single-resource detail view and a bulk list. Labeled
	// "Show Role Detail"/"Show Instance Profile Detail" in the menu as of
	// 2026-07-24 (MENU_REVIEW.md; DECISIONS.md, "Regroup the Compute
	// menu") -- unifying on "Show" rather than "View" to match every
	// other detail view in the app; the Go field names are unchanged.
	ViewRoleDetail            func(ctx context.Context) error
	ViewInstanceProfileDetail func(ctx context.Context) error
	// CreateRoleFromTemplate is Phase 20.39's guided creation action --
	// the only IAM-domain action that mutates account state by creating
	// new resources, reversing the 2026-07-02 "never creates a role"
	// scope deliberately and only through curated templates.
	CreateRoleFromTemplate func(ctx context.Context) error
	// DeleteRole/AttachPolicyToRole/DetachPolicyFromRole are Phase
	// 20.40's CRUD-completion actions (DECISIONS.md, "IAM Profile & Role
	// Management: support CRUD for DLD-owned roles"): every one of them
	// is scoped to DLD-owned roles only (RequireDLDOwned), matching this
	// domain's "IMSS/AWS-provided is read-only" boundary everywhere else.
	DeleteRole           func(ctx context.Context) error
	AttachPolicyToRole   func(ctx context.Context) error
	DetachPolicyFromRole func(ctx context.Context) error
	// RemoveRoleFromInstanceProfile/DeleteInstanceProfile are Phase
	// 20.55's additions (DECISIONS.md, "Delete Role: correct the
	// wrong-remedy message, add the missing instance-profile-membership
	// actions") -- the actual remedy for Delete Role's "still a member of
	// instance profile(s)" refusal, which Associate/replace IAM instance
	// profile (Compute domain) can never satisfy, since that action only
	// ever changes an EC2 instance's *association* to a profile, not a
	// profile's own role *membership*.
	RemoveRoleFromInstanceProfile func(ctx context.Context) error
	DeleteInstanceProfile         func(ctx context.Context) error
}

// iamMenuItem pairs an IAM menu label with the IAMActions field it
// dispatches to.
type iamMenuItem struct {
	label string
	// cliSlug is this leaf's stable CLI path segment (DR-0177); empty means it has
	// no CLI form yet.
	cliSlug string
	action  func(IAMActions, context.Context) error
}

// iamMenuItems is DESIGN.md's IAM domain menu, in order. Attach/Detach
// Policy and Remove Role from Instance Profile all precede Delete Role/
// Delete Instance Profile (MENU_REVIEW.md, 2026-07-24; DECISIONS.md,
// "Regroup the Compute menu") -- each of the former is trivially
// reversible via its paired action or by re-adding the role, unlike
// either delete, so the two truly irreversible actions sit last, in the
// natural remedy order (DECISIONS.md, "Delete Role: correct the
// wrong-remedy message, add the missing instance-profile-membership
// actions"): remove the role from a profile before deleting that now-
// empty profile, before deleting the role itself. No "Back to domain
// picker" entry -- DECISIONS.md, "TUI keybinding conventions": 'q' is
// the universal back key everywhere.
var iamMenuItems = []iamMenuItem{
	{label: "Show Roles", cliSlug: ShowRolesCLISlug, action: func(a IAMActions, ctx context.Context) error { return a.ShowRoles(ctx) }},
	{label: "Show Instance Profiles", cliSlug: ShowInstanceProfilesCLISlug, action: func(a IAMActions, ctx context.Context) error { return a.ShowInstanceProfiles(ctx) }},
	{label: "Show Policies", cliSlug: ShowPoliciesCLISlug, action: func(a IAMActions, ctx context.Context) error { return a.ShowPolicies(ctx) }},
	{label: "Show Role Detail", cliSlug: ShowRoleDetailCLISlug, action: func(a IAMActions, ctx context.Context) error { return a.ViewRoleDetail(ctx) }},
	{label: "Show Instance Profile Detail", cliSlug: ShowInstanceProfileDetailCLISlug, action: func(a IAMActions, ctx context.Context) error { return a.ViewInstanceProfileDetail(ctx) }},
	{label: "Create Role from Template", action: func(a IAMActions, ctx context.Context) error { return a.CreateRoleFromTemplate(ctx) }},
	{label: "Attach Policy to Role", action: func(a IAMActions, ctx context.Context) error { return a.AttachPolicyToRole(ctx) }},
	{label: "Detach Policy from Role", action: func(a IAMActions, ctx context.Context) error { return a.DetachPolicyFromRole(ctx) }},
	{label: "Remove Role from Instance Profile", action: func(a IAMActions, ctx context.Context) error { return a.RemoveRoleFromInstanceProfile(ctx) }},
	{label: "Delete Instance Profile", action: func(a IAMActions, ctx context.Context) error { return a.DeleteInstanceProfile(ctx) }},
	{label: "Delete Role", action: func(a IAMActions, ctx context.Context) error { return a.DeleteRole(ctx) }},
}

// pickIAMItem runs the IAM domain menu's huh.Select and returns the
// chosen iamMenuItem. Selects by index into iamMenuItems, not by
// iamMenuItem itself -- huh.Select's T must be comparable, and
// iamMenuItem.action (a func) isn't. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickIAMItem(w io.Writer, input io.Reader, output io.Writer) (iamMenuItem, error) {
	opts := make([]huh.Option[int], len(iamMenuItems))
	for i, item := range iamMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("IAM").
		Description("Browse IAM roles, instance profiles, and customer-managed policies -- each annotated with its Origin tag and DLD-ownership.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintGoBack, field, input, output); err != nil {
		return iamMenuItem{}, err
	}
	return iamMenuItems[idx], nil
}

// RunIAMMenu runs the IAM domain's interactive menu loop, the same
// shape as RunTagMgmtMenu minus a Refresh step: show the menu, dispatch
// the chosen action, and repeat -- until the picker is aborted
// ('q'/ctrl+c, reported as ErrBackToDomainPicker) or an exit signal is
// hit (reported as nil, which RunDomainPicker treats as "exit the whole
// program"). A single action's error is shown and the loop continues.
func RunIAMMenu(ctx context.Context, w io.Writer, actions IAMActions) error {
	return runIAMMenu(ctx, w, actions, nil, nil)
}

// runIAMMenu is RunIAMMenu's testable core: menuInput/menuOutput are
// nil in production and supplied by tests to drive the same huh.Select
// through its accessible-mode pipe path instead.
func runIAMMenu(ctx context.Context, w io.Writer, actions IAMActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickIAMItem(w, menuInput, menuOutput)
		if err != nil {
			return mapMenuPickerErr(err)
		}

		if err := choice.action(actions, ctx); err != nil {
			if isExitSignal(err) {
				printExiting(w)
				return nil
			}
			fmt.Fprintf(w, "Error: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
			continue
		}

		// The dispatched action succeeded and may have printed its own
		// status output (DECISIONS.md, "Widen 'pause for acknowledgment'
		// to every action, not just errors") -- pause before the next
		// redraw.
		pauseForAcknowledgment(menuInput, menuOutput)
	}
}

// IAMDomainCLISlug is the IAM domain's cliSlug.
const IAMDomainCLISlug = "iam"

// The IAM read-only leaves' cliSlug values (DR-0177's mechanical rule).
const (
	ShowRolesCLISlug                 = "show-roles"
	ShowInstanceProfilesCLISlug      = "show-instance-profiles"
	ShowPoliciesCLISlug              = "show-policies"
	ShowRoleDetailCLISlug            = "show-role-detail"
	ShowInstanceProfileDetailCLISlug = "show-instance-profile-detail"
)

func iamItemBySlug(slug string) (iamMenuItem, bool) {
	for _, item := range iamMenuItems {
		if item.cliSlug != "" && item.cliSlug == slug {
			return item, true
		}
	}
	return iamMenuItem{}, false
}

// RunIAMMenuFromSlug runs slug's leaf once, then falls into the IAM menu. The
// IAM domain has no Refresh: its listings are fetched when shown. ok is false,
// and nothing runs, for an unregistered slug.
func RunIAMMenuFromSlug(ctx context.Context, w io.Writer, actions IAMActions, slug string) (ok bool, err error) {
	return runIAMMenuFromSlug(ctx, w, actions, slug, nil, nil)
}

func runIAMMenuFromSlug(ctx context.Context, w io.Writer, actions IAMActions, slug string, menuInput io.Reader, menuOutput io.Writer) (ok bool, err error) {
	item, found := iamItemBySlug(slug)
	if !found {
		return false, nil
	}
	return true, runLeafThenMenu(ctx, w, func(ctx context.Context) error { return item.action(actions, ctx) }, nil,
		func() error { return runIAMMenu(ctx, w, actions, menuInput, menuOutput) }, menuInput, menuOutput)
}
