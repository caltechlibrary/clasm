package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// MenuActions bundles the ten workflow entry points the main menu
// dispatches to, as zero-arg-besides-ctx closures. main.go constructs
// each closure bound to the live AWS clients and the current instance/
// AMI listing snapshot; this indirection is what lets menu dispatch
// itself be tested with fakes, without driving every workflow's full
// interactive prompt sequence.
type MenuActions struct {
	CreateInstanceFromAMI       func(ctx context.Context) error
	CreateInstanceFromCloudInit func(ctx context.Context) error
	StartEC2Instance            func(ctx context.Context) error
	StopEC2Instance             func(ctx context.Context) error
	TerminateEC2Instance        func(ctx context.Context) error
	ManageTags                  func(ctx context.Context) error
	CreateAMIFromInstance       func(ctx context.Context) error
	RemoveAMI                   func(ctx context.Context) error
	ShowCloudInit               func(ctx context.Context) error
	BackupArchiveAndTrim        func(ctx context.Context) error
	// Refresh re-fetches the instance/AMI listings, silently -- no
	// display. Called once after every successful dispatched action
	// (DECISIONS.md, "Refresh data after each operation") so instance/AMI-
	// selection prompts elsewhere stay current, and once on entering the
	// Compute domain.
	Refresh func(ctx context.Context) error
	// ShowResourceLists shows the already-fetched instance/AMI listings in
	// the shared List-tier component (DESIGN.md, "Terminal UI
	// Architecture: Menus, Actions, Lists, and Managers"). Called only by
	// "Show resource lists" -- unlike Refresh, it never runs automatically
	// after other actions (tui.RunListView blocks on an interactive
	// bubbletea loop until 'q', so showing it after every action would
	// force pressing 'q' just to get back to the menu -- see S3Actions'
	// own Refresh/ShowResourceLists split, Phase 20.6).
	ShowResourceLists func(ctx context.Context) error
}

// menuItem pairs a main-menu label with the MenuActions field it
// dispatches to.
type menuItem struct {
	label  string
	action func(MenuActions, context.Context) error
}

// mainMenuItems is DESIGN.md's Main Menu, in order. "Show resource
// lists" leads the menu (DECISIONS.md, "Move Show resource lists to the
// top of the Compute menu; rename from Refresh") -- it's the natural
// first move on entering the domain (orient before acting), not just
// one action among ten. No "Back to domain picker" entry -- DECISIONS.md,
// "TUI keybinding conventions": 'q' is the universal back key
// everywhere, so a redundant menu item would just be a second way to do
// the same thing (matching s3MenuItems' own drop of "Back to domain
// picker" in Phase 20.7).
var mainMenuItems = []menuItem{
	{"Show resource lists", func(a MenuActions, ctx context.Context) error { return a.ShowResourceLists(ctx) }},
	{"Create EC2 instance from AMI", func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromAMI(ctx) }},
	{"Create EC2 instance from cloud-init YAML", func(a MenuActions, ctx context.Context) error { return a.CreateInstanceFromCloudInit(ctx) }},
	{"Start EC2 instance", func(a MenuActions, ctx context.Context) error { return a.StartEC2Instance(ctx) }},
	{"Stop EC2 instance", func(a MenuActions, ctx context.Context) error { return a.StopEC2Instance(ctx) }},
	{"Terminate EC2 instance", func(a MenuActions, ctx context.Context) error { return a.TerminateEC2Instance(ctx) }},
	{"Manage tags for an instance or AMI", func(a MenuActions, ctx context.Context) error { return a.ManageTags(ctx) }},
	{"Create AMI from EC2 instance (running or stopped)", func(a MenuActions, ctx context.Context) error { return a.CreateAMIFromInstance(ctx) }},
	{"Remove AMI", func(a MenuActions, ctx context.Context) error { return a.RemoveAMI(ctx) }},
	{"Show/export cloud-init for an instance or AMI", func(a MenuActions, ctx context.Context) error { return a.ShowCloudInit(ctx) }},
	{"Archive stale backups to S3 and trim disk space", func(a MenuActions, ctx context.Context) error { return a.BackupArchiveAndTrim(ctx) }},
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
		Description("Manage EC2 instances and AMIs, or archive stale backups to S3.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, "(q to go back)", field, input, output); err != nil {
		return menuItem{}, err
	}
	return mainMenuItems[idx], nil
}

// RunMainMenu runs the Compute domain's interactive menu loop (DESIGN.md,
// "Compute Domain (EC2 & AMI)"): show the 11-option menu, dispatch the
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
			continue
		}

		if err := actions.Refresh(ctx); err != nil {
			fmt.Fprintf(w, "Error refreshing listings: %s\n", formatError(err))
		}
	}
}

func printExiting(w io.Writer) {
	fmt.Fprintln(w, "\nExiting.")
}

func isExitSignal(err error) bool {
	return errors.Is(err, ui.ErrCancelled) || errors.Is(err, huh.ErrUserAborted) || errors.Is(err, io.EOF)
}
