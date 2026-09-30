package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// MenuActions bundles the twenty-two workflow entry points the main menu
// dispatches to, as zero-arg-besides-ctx closures. main.go constructs
// each closure bound to the live AWS clients and the current instance/
// AMI/launch-template listing snapshot; this indirection is what lets
// menu dispatch itself be tested with fakes, without driving every
// workflow's full interactive prompt sequence.
type MenuActions struct {
	CreateInstanceFromAMI       func(ctx context.Context) error
	CreateInstanceFromCloudInit func(ctx context.Context) error
	StartEC2Instance            func(ctx context.Context) error
	StopEC2Instance             func(ctx context.Context) error
	TerminateEC2Instance        func(ctx context.Context) error
	// ResizeInstanceRootVolume grows a running instance's root EBS
	// volume and its OS-level partition/filesystem (DESIGN.md,
	// "Configurable EBS Root Volume Size", Part 2).
	ResizeInstanceRootVolume func(ctx context.Context) error
	// AssociateOrReplaceInstanceProfile attaches (or replaces) an IAM
	// instance profile on an already-running instance -- general-purpose,
	// not SSM-specific (DESIGN.md, "SSM-Capable Instance Profile
	// Enforcement + Retrofit", Part 3).
	AssociateOrReplaceInstanceProfile func(ctx context.Context) error
	ManageTags                        func(ctx context.Context) error
	CreateAMIFromInstance             func(ctx context.Context) error
	RemoveAMI                         func(ctx context.Context) error
	ShowCloudInit                     func(ctx context.Context) error
	// Launch-template actions (DESIGN.md, "Launch Templates").
	ShowLaunchTemplate                func(ctx context.Context) error
	CreateLaunchTemplateFromCloudInit func(ctx context.Context) error
	CreateInstanceFromLaunchTemplate  func(ctx context.Context) error
	SyncLaunchTemplate                func(ctx context.Context) error
	// ModifyLaunchTemplateSize changes an existing launch template's
	// instance type and/or EBS root volume size (and, if needed, its
	// base AMI, to match a new instance type's architecture) by
	// creating a new version -- distinct from Sync, which only ever
	// touches UserData (DESIGN.md, "Modify Launch Template Size").
	ModifyLaunchTemplateSize     func(ctx context.Context) error
	PromoteLaunchTemplateVersion func(ctx context.Context) error
	DeleteLaunchTemplateVersions func(ctx context.Context) error
	DeleteLaunchTemplate         func(ctx context.Context) error
	// Refresh re-fetches the instance/AMI/launch-template listings,
	// silently -- no display. Called once after every successful
	// dispatched action (DECISIONS.md, "Refresh data after each
	// operation") so instance/AMI/template-selection prompts elsewhere
	// stay current, and once on entering the Compute domain.
	Refresh func(ctx context.Context) error
	// ShowInstances/ShowAMIs/ShowLaunchTemplates each show one resource
	// type's already-fetched listing in the shared List-tier component
	// (DESIGN.md, "Terminal UI Architecture: Menus, Actions, Lists, and
	// Managers") -- three separate menu entries rather than one combined
	// "Show resource lists" paging through all three in sequence
	// (reported directly 2026-07-20: paging through Instances -> AMIs ->
	// Launch Templates to reach the one you actually wanted felt
	// awkward). Unlike Refresh, none of these run automatically after
	// other actions (tui.RunListView blocks on an interactive bubbletea
	// loop until 'q', so showing it after every action would force
	// pressing 'q' just to get back to the menu -- see S3Actions' own
	// Refresh/ShowResourceLists split, Phase 20.6, which this still
	// matches in spirit: fetch happens in Refresh, display is a
	// separate, explicit choice).
	ShowInstances       func(ctx context.Context) error
	ShowAMIs            func(ctx context.Context) error
	ShowLaunchTemplates func(ctx context.Context) error
	// ShowInstanceDetail/ShowAMIDetail each show one resource's curated
	// detail fields (DESIGN.md, "Instance/AMI Detail Views") -- appended
	// at the end of mainMenuItems, not placed near ShowInstances/ShowAMIs
	// above, so existing numeric-index tests for prior entries stay
	// valid unchanged (DECISIONS.md, "Instance/AMI Detail Views:
	// on-demand describe calls, appended menu placement").
	ShowInstanceDetail func(ctx context.Context) error
	ShowAMIDetail      func(ctx context.Context) error
}

// menuItem pairs a main-menu label with the MenuActions field it
// dispatches to.
type menuItem struct {
	label string
	// cliSlug is this leaf's stable CLI path segment under `clasm compute`
	// (DR-0177): the mechanical rule applied to the label. Empty means the
	// leaf has no CLI form and is unreachable from the CLI path.
	cliSlug string
	action  func(MenuActions, context.Context) error
}

// The Compute read-only leaves' cliSlug values, exported so the dispatch
// switch, usage messages and tests share one literal.
const (
	ShowInstancesCLISlug            = "show-instances"
	ShowInstanceDetailCLISlug       = "show-instance-detail"
	ShowAMIsCLISlug                 = "show-amis"
	ShowAMIDetailCLISlug            = "show-ami-detail"
	ShowLaunchTemplatesCLISlug      = "show-launch-templates"
	ShowLaunchTemplateDetailCLISlug = "show-launch-template-detail"
	// ShowExportCloudInitCLISlug is awkward (the label is); both will be
	// shortened together later.
	ShowExportCloudInitCLISlug = "show-export-cloud-init-for-an-instance-or-ami"

	// The first two mutating Compute leaves with a form (DR-0177, same rule).
	CreateEC2InstanceFromLaunchTemplateCLISlug = "create-ec2-instance-from-launch-template"
	CreateLaunchTemplateFromCloudInitCLISlug   = "create-launch-template-from-cloud-init-yaml"
)

// mainMenuItems is DESIGN.md's Main Menu, grouped View/Inspect -> Instance
// lifecycle -> AMI lifecycle -> Launch Template lifecycle -> Maintenance
// (MENU_REVIEW.md, 2026-07-24; DECISIONS.md, "Regroup the Compute menu").
// The view/inspect group leads (DECISIONS.md, "Move Show resource lists
// to the top of the Compute menu; rename from Refresh") -- it's the
// natural first move on entering the domain (orient before acting), not
// just one action among many. Each list-view/detail-view pair sits
// together (Show instances + Show instance detail, Show AMIs + Show AMI
// detail, Show launch templates + Show launch template detail), matching
// the IAM domain menu's own List-then-Detail shape. No "Back to domain
// picker" entry -- DECISIONS.md, "TUI keybinding conventions": 'q' is
// the universal back key everywhere, so a redundant menu item would
// just be a second way to do the same thing (matching s3MenuItems' own
// drop of "Back to domain picker" in Phase 20.7).
var mainMenuItems = []menuItem{
	// View/Inspect
	{label: "Show instances", cliSlug: ShowInstancesCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowInstances(ctx) }},
	{label: "Show instance detail", cliSlug: ShowInstanceDetailCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowInstanceDetail(ctx) }},
	{label: "Show AMIs", cliSlug: ShowAMIsCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowAMIs(ctx) }},
	{label: "Show AMI detail", cliSlug: ShowAMIDetailCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowAMIDetail(ctx) }},
	{label: "Show launch templates", cliSlug: ShowLaunchTemplatesCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowLaunchTemplates(ctx) }},
	{label: "Show launch template detail", cliSlug: ShowLaunchTemplateDetailCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowLaunchTemplate(ctx) }},
	{label: "Show/export cloud-init for an instance or AMI", cliSlug: ShowExportCloudInitCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.ShowCloudInit(ctx) }},
	// Instance lifecycle
	{label: "Create EC2 instance from AMI", action: func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromAMI(ctx) }},
	{label: "Create EC2 instance from cloud-init YAML", action: func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromCloudInit(ctx) }},
	{label: "Create EC2 instance from launch template", cliSlug: CreateEC2InstanceFromLaunchTemplateCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromLaunchTemplate(ctx) }},
	{label: "Start EC2 instance", action: func(a MenuActions, ctx context.Context) error { return a.StartEC2Instance(ctx) }},
	{label: "Stop EC2 instance", action: func(a MenuActions, ctx context.Context) error { return a.StopEC2Instance(ctx) }},
	{label: "Terminate EC2 instance", action: func(a MenuActions, ctx context.Context) error { return a.TerminateEC2Instance(ctx) }},
	{label: "Resize instance's root volume", action: func(a MenuActions, ctx context.Context) error { return a.ResizeInstanceRootVolume(ctx) }},
	{label: "Associate/replace IAM instance profile", action: func(a MenuActions, ctx context.Context) error { return a.AssociateOrReplaceInstanceProfile(ctx) }},
	{label: "Manage tags for an instance or AMI", action: func(a MenuActions, ctx context.Context) error { return a.ManageTags(ctx) }},
	// AMI lifecycle
	{label: "Create AMI from EC2 instance (running or stopped)", action: func(a MenuActions, ctx context.Context) error { return a.CreateAMIFromInstance(ctx) }},
	{label: "Remove AMI", action: func(a MenuActions, ctx context.Context) error { return a.RemoveAMI(ctx) }},
	// Launch Template lifecycle
	{label: "Create launch template from cloud-init YAML", cliSlug: CreateLaunchTemplateFromCloudInitCLISlug, action: func(a MenuActions, ctx context.Context) error { return a.CreateLaunchTemplateFromCloudInit(ctx) }},
	{label: "Sync cloud-init YAML to a launch template", action: func(a MenuActions, ctx context.Context) error { return a.SyncLaunchTemplate(ctx) }},
	{label: "Modify launch template's instance type / EBS root volume size", action: func(a MenuActions, ctx context.Context) error { return a.ModifyLaunchTemplateSize(ctx) }},
	{label: "Promote a launch template version to default", action: func(a MenuActions, ctx context.Context) error { return a.PromoteLaunchTemplateVersion(ctx) }},
	{label: "Delete launch template version(s)", action: func(a MenuActions, ctx context.Context) error { return a.DeleteLaunchTemplateVersions(ctx) }},
	{label: "Delete a launch template", action: func(a MenuActions, ctx context.Context) error { return a.DeleteLaunchTemplate(ctx) }},
}

// pickMainMenuItem runs the Compute main menu's huh.Select and returns
// the chosen menuItem. Selects by index into mainMenuItems, not by
// menuItem itself -- huh.Select's T must be comparable, and
// menuItem.action (a func) isn't -- the same constraint pickS3MenuItem
// already works around. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickMainMenuItem(w io.Writer, input io.Reader, output io.Writer) (menuItem, error) {
	opts := make([]huh.Option[int], len(mainMenuItems))
	for i, item := range mainMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("Manage EC2 instances, AMIs, and launch templates.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintGoBack, field, input, output); err != nil {
		return menuItem{}, err
	}
	return mainMenuItems[idx], nil
}

// RunMainMenu runs the Compute domain's interactive menu loop (DESIGN.md,
// "Compute Domain (EC2 & AMI)"): show the 20-option menu, dispatch the
// chosen action, refresh listings after a successful dispatch, and
// repeat -- until the picker is aborted ('q'/ctrl+c, reported as
// ErrBackToDomainPicker), a cancelled ctx (e.g. Ctrl+C delivered as
// os.Interrupt between prompts), or an aborted/EOF prompt from a
// dispatched action (e.g. Ctrl+C during an active huh field, which
// surfaces as an error rather than a process signal) -- the latter two
// report nil, which RunDomainPicker treats as "exit the whole program",
// not "return to the picker". A single action's error is shown and the
// loop continues -- one failed operation shouldn't force restarting the
// whole CLI.
//
// The menu picker itself is huh.Select (DECISIONS.md, "Convert RunS3Menu
// to huh.Select").
func RunMainMenu(ctx context.Context, w io.Writer, actions MenuActions) error {
	return runMainMenu(ctx, w, actions, nil, nil)
}

// runMainMenu is RunMainMenu's testable core: menuInput/menuOutput are
// nil in production and supplied by tests to drive the same huh.Select
// through its accessible-mode pipe path instead (DECISIONS.md, "huh
// fields are pipe-testable...").
func runMainMenu(ctx context.Context, w io.Writer, actions MenuActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickMainMenuItem(w, menuInput, menuOutput)
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
		// to every action, not just errors") -- pause before Refresh's
		// own (silent, no-display) work and the next redraw.
		pauseForAcknowledgment(menuInput, menuOutput)

		if err := actions.Refresh(ctx); err != nil {
			fmt.Fprintf(w, "Error refreshing listings: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
		}
	}
}

func printExiting(w io.Writer) {
	fmt.Fprintln(w, "\nExiting.")
}

// pauseForAcknowledgment blocks on a plain, content-sized prompt (not
// full-height, per TUI_REFERENCE.md's "Plain prompts" tier) until the
// operator presses Enter -- called immediately after printing text a
// menu loop's next full-height Select redraw would otherwise wipe from
// the screen before it could be read (DECISIONS.md, "Pause for
// acknowledgment before every menu-loop redraw"). Best-effort: huh.Input's
// accessible-mode path (accessibility.PromptString) never returns an
// error, even on EOF, so there's nothing meaningful to propagate; any
// interactive-mode error (e.g. ctrl-C) is ignored here too -- dismissing
// the pause is a reasonable response to it, and the loop's own
// cancellation/exit-signal handling still applies to whatever comes next.
func pauseForAcknowledgment(input io.Reader, output io.Writer) {
	_, _ = ui.Prompt("Press Enter to continue", ui.WithIO(input, output))
}

func isExitSignal(err error) bool {
	return errors.Is(err, ui.ErrCancelled) || errors.Is(err, huh.ErrUserAborted) || errors.Is(err, io.EOF)
}

// mainMenuItemBySlug finds the menuItem whose cliSlug matches slug.
func mainMenuItemBySlug(slug string) (menuItem, bool) {
	for _, item := range mainMenuItems {
		if item.cliSlug != "" && item.cliSlug == slug {
			return item, true
		}
	}
	return menuItem{}, false
}

// RunMainMenuFromSlug runs slug's leaf once, then falls into the Compute menu,
// exactly as RunRDMBackupRestoreMenuFromSlug does for RDM: `clasm compute
// show-instances` with no further words behaves as if the operator had chosen
// that entry themselves. ok is false, and nothing runs, for an unregistered slug.
func RunMainMenuFromSlug(ctx context.Context, w io.Writer, actions MenuActions, slug string) (ok bool, err error) {
	return runMainMenuFromSlug(ctx, w, actions, slug, nil, nil)
}

func runMainMenuFromSlug(ctx context.Context, w io.Writer, actions MenuActions, slug string, menuInput io.Reader, menuOutput io.Writer) (ok bool, err error) {
	item, found := mainMenuItemBySlug(slug)
	if !found {
		return false, nil
	}
	if err := item.action(actions, ctx); err != nil {
		if isExitSignal(err) {
			printExiting(w)
			return true, nil
		}
		fmt.Fprintf(w, "Error: %s\n", formatError(err))
		pauseForAcknowledgment(menuInput, menuOutput)
		return true, runMainMenu(ctx, w, actions, menuInput, menuOutput)
	}
	pauseForAcknowledgment(menuInput, menuOutput)
	if refreshErr := actions.Refresh(ctx); refreshErr != nil {
		fmt.Fprintf(w, "Error refreshing listings: %s\n", formatError(refreshErr))
		pauseForAcknowledgment(menuInput, menuOutput)
	}
	return true, runMainMenu(ctx, w, actions, menuInput, menuOutput)
}

// ComputeDomainCLISlug is the Compute domain's cliSlug.
const ComputeDomainCLISlug = "compute"

// LeafCLISlugExists reports whether leafSlug is a registered leaf CLI slug under
// domainSlug. A slug is only valid under the domain that registered it.
func LeafCLISlugExists(domainSlug, leafSlug string) bool {
	switch domainSlug {
	case ComputeDomainCLISlug:
		_, found := mainMenuItemBySlug(leafSlug)
		return found
	case RDMBackupRestoreDomainCLISlug:
		return RDMLeafCLISlugExists(leafSlug)
	case KeyManagementDomainCLISlug:
		_, found := keyMgmtItemBySlug(leafSlug)
		return found
	case IAMDomainCLISlug:
		_, found := iamItemBySlug(leafSlug)
		return found
	case S3DomainCLISlug:
		_, found := s3ItemBySlug(leafSlug)
		return found
	case TagManagementDomainCLISlug:
		_, found := tagMgmtItemBySlug(leafSlug)
		return found
	}
	return false
}

// DomainHasLeafCLIForms reports whether any leaf under domainSlug has a CLI
// form yet; a path under a domain that has none is refused as a usage error.
func DomainHasLeafCLIForms(domainSlug string) bool {
	switch domainSlug {
	case ComputeDomainCLISlug, RDMBackupRestoreDomainCLISlug, KeyManagementDomainCLISlug, IAMDomainCLISlug, S3DomainCLISlug, TagManagementDomainCLISlug:
		return true
	}
	return false
}

// runLeafThenMenu is the deep-link shared by the newer domains: run the leaf
// once as one iteration of the domain's own loop would (print and pause on
// error, pause and refresh on success), then fall into that domain's menu. An
// exit signal from the leaf ends the run without the menu. refresh may be nil.
func runLeafThenMenu(ctx context.Context, w io.Writer, leaf, refresh func(context.Context) error, menu func() error, menuInput io.Reader, menuOutput io.Writer) error {
	if err := leaf(ctx); err != nil {
		if isExitSignal(err) {
			printExiting(w)
			return nil
		}
		fmt.Fprintf(w, "Error: %s\n", formatError(err))
		pauseForAcknowledgment(menuInput, menuOutput)
		return menu()
	}
	pauseForAcknowledgment(menuInput, menuOutput)
	if refresh != nil {
		if err := refresh(ctx); err != nil {
			fmt.Fprintf(w, "Error refreshing: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
		}
	}
	return menu()
}
