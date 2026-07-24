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
	BackupArchiveAndTrim              func(ctx context.Context) error
	// Launch-template actions (DESIGN.md, "Launch Templates").
	ShowLaunchTemplate                func(ctx context.Context) error
	CreateLaunchTemplateFromCloudInit func(ctx context.Context) error
	CreateInstanceFromLaunchTemplate  func(ctx context.Context) error
	SyncLaunchTemplate                func(ctx context.Context) error
	PromoteLaunchTemplateVersion      func(ctx context.Context) error
	DeleteLaunchTemplateVersions      func(ctx context.Context) error
	DeleteLaunchTemplate              func(ctx context.Context) error
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
	label  string
	action func(MenuActions, context.Context) error
}

// mainMenuItems is DESIGN.md's Main Menu, in order. The three "Show"
// entries lead the menu (DECISIONS.md, "Move Show resource lists to the
// top of the Compute menu; rename from Refresh") -- it's the natural
// first move on entering the domain (orient before acting), not just
// one action among many -- split into three separate entries rather
// than one combined listing (DECISIONS.md, "Split Show resource lists
// into per-resource-type Compute menu entries"). No "Back to domain
// picker" entry -- DECISIONS.md, "TUI keybinding conventions": 'q' is
// the universal back key everywhere, so a redundant menu item would
// just be a second way to do the same thing (matching s3MenuItems' own
// drop of "Back to domain picker" in Phase 20.7).
var mainMenuItems = []menuItem{
	{"Show instances", func(a MenuActions, ctx context.Context) error { return a.ShowInstances(ctx) }},
	{"Show AMIs", func(a MenuActions, ctx context.Context) error { return a.ShowAMIs(ctx) }},
	{"Show launch templates", func(a MenuActions, ctx context.Context) error { return a.ShowLaunchTemplates(ctx) }},
	{"Create EC2 instance from AMI", func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromAMI(ctx) }},
	{"Create EC2 instance from cloud-init YAML", func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromCloudInit(ctx) }},
	{"Create EC2 instance from launch template", func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromLaunchTemplate(ctx) }},
	{"Start EC2 instance", func(a MenuActions, ctx context.Context) error { return a.StartEC2Instance(ctx) }},
	{"Stop EC2 instance", func(a MenuActions, ctx context.Context) error { return a.StopEC2Instance(ctx) }},
	{"Terminate EC2 instance", func(a MenuActions, ctx context.Context) error { return a.TerminateEC2Instance(ctx) }},
	{"Resize instance's root volume", func(a MenuActions, ctx context.Context) error { return a.ResizeInstanceRootVolume(ctx) }},
	{"Associate/replace IAM instance profile", func(a MenuActions, ctx context.Context) error { return a.AssociateOrReplaceInstanceProfile(ctx) }},
	{"Manage tags for an instance or AMI", func(a MenuActions, ctx context.Context) error { return a.ManageTags(ctx) }},
	{"Create AMI from EC2 instance (running or stopped)", func(a MenuActions, ctx context.Context) error { return a.CreateAMIFromInstance(ctx) }},
	{"Remove AMI", func(a MenuActions, ctx context.Context) error { return a.RemoveAMI(ctx) }},
	{"Show/export cloud-init for an instance or AMI", func(a MenuActions, ctx context.Context) error { return a.ShowCloudInit(ctx) }},
	{"Show a launch template", func(a MenuActions, ctx context.Context) error { return a.ShowLaunchTemplate(ctx) }},
	{"Create launch template from cloud-init YAML", func(a MenuActions, ctx context.Context) error { return a.CreateLaunchTemplateFromCloudInit(ctx) }},
	{"Sync cloud-init YAML to a launch template", func(a MenuActions, ctx context.Context) error { return a.SyncLaunchTemplate(ctx) }},
	{"Promote a launch template version to default", func(a MenuActions, ctx context.Context) error { return a.PromoteLaunchTemplateVersion(ctx) }},
	{"Delete launch template version(s)", func(a MenuActions, ctx context.Context) error { return a.DeleteLaunchTemplateVersions(ctx) }},
	{"Delete a launch template", func(a MenuActions, ctx context.Context) error { return a.DeleteLaunchTemplate(ctx) }},
	{"Archive stale backups to S3 and trim disk space", func(a MenuActions, ctx context.Context) error { return a.BackupArchiveAndTrim(ctx) }},
	{"Show instance detail", func(a MenuActions, ctx context.Context) error { return a.ShowInstanceDetail(ctx) }},
	{"Show AMI detail", func(a MenuActions, ctx context.Context) error { return a.ShowAMIDetail(ctx) }},
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
		Description("Manage EC2 instances, AMIs, and launch templates, or archive stale backups to S3.").
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
