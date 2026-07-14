package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// S3Actions bundles the S3 domain's menu entry points, mirroring
// KeyMgmtActions' shape. "Sync Local Directory to Bucket," "Browse/
// Manage Objects," and the standalone bulk-delete-by-prefix entry are
// gone as of DESIGN.md 21.2/PLAN.md Phase 20.1 -- all three are now
// reachable from BrowseAndManageObjects, the interactive file manager
// (internal/filemanager).
type S3Actions struct {
	CreateBucket            func(ctx context.Context) error
	ConfigureWebsite        func(ctx context.Context) error
	BrowseAndManageObjects  func(ctx context.Context) error
	ManageLifecyclePolicies func(ctx context.Context) error
	DeleteBucket            func(ctx context.Context) error
	// Refresh re-fetches the bucket listing, silently -- no display.
	// Called once after every successful dispatched action (DECISIONS.md,
	// "Refresh data after each operation") so bucket-selection prompts
	// elsewhere stay current, and once on entering the S3 domain.
	Refresh func(ctx context.Context) error
	// ShowResourceLists shows the already-fetched bucket listing in the
	// shared List-tier component (DESIGN.md, "Terminal UI Architecture:
	// Menus, Actions, Lists, and Managers"). Called only by "List S3
	// Buckets" -- unlike Refresh, it never runs automatically after
	// other actions.
	ShowResourceLists func(ctx context.Context) error
}

// s3Item pairs an S3 menu label with the S3Actions field it dispatches
// to.
type s3Item struct {
	label  string
	action func(S3Actions, context.Context) error
}

// s3MenuItems is DESIGN.md 21.2's S3 domain menu, in order. "List S3
// Buckets" leads the menu, same convention as Compute and Key
// Management's own "Show resource lists" entries. No "Back to domain
// picker" entry -- DECISIONS.md, "TUI keybinding conventions": 'q' is
// the universal back key everywhere, so a redundant menu item would
// just be a second way to do the same thing.
var s3MenuItems = []s3Item{
	{"List S3 Buckets", func(a S3Actions, ctx context.Context) error { return a.ShowResourceLists(ctx) }},
	{"Create Bucket", func(a S3Actions, ctx context.Context) error { return a.CreateBucket(ctx) }},
	{"Configure Static Website Hosting", func(a S3Actions, ctx context.Context) error { return a.ConfigureWebsite(ctx) }},
	{"Browse & Manage Objects", func(a S3Actions, ctx context.Context) error { return a.BrowseAndManageObjects(ctx) }},
	{"Manage Bucket Lifecycle Policies", func(a S3Actions, ctx context.Context) error { return a.ManageLifecyclePolicies(ctx) }},
	{"Delete Bucket", func(a S3Actions, ctx context.Context) error { return a.DeleteBucket(ctx) }},
}

// RunS3Menu runs the S3 domain's interactive menu loop, the same shape as
// RunKeyMgmtMenu: show the menu, dispatch the chosen action, refresh the
// listing after a successful dispatch, and repeat -- until the picker is
// aborted ('q'/ctrl+c, reported as ErrBackToDomainPicker) or a dispatched
// action hits an exit signal (reported as nil). A single action's error
// is shown and the loop continues.
//
// The menu picker itself is huh.Select (DECISIONS.md, "Convert RunS3Menu
// to huh.Select").
func RunS3Menu(ctx context.Context, w io.Writer, actions S3Actions) error {
	return runS3Menu(ctx, w, actions, nil, nil)
}

// runS3Menu is RunS3Menu's testable core: menuInput/menuOutput are nil in
// production (the picker runs interactively on the real terminal, exactly
// like object_browser.go's runFieldWithHelp call sites) and are supplied
// by tests to drive the same huh.Select through its accessible-mode pipe
// path instead (DECISIONS.md, "huh fields are pipe-testable via
// WithAccessible(true).WithInput/WithOutput" -- see that entry's
// lineAtATimeReader caveat: a reader that returns more than one buffered
// line per Read call starves every field after the first).
func runS3Menu(ctx context.Context, w io.Writer, actions S3Actions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickS3MenuItem(w, menuInput, menuOutput)
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

// pickS3MenuItem runs the S3 domain menu's huh.Select and returns the
// chosen s3Item. It selects by index into s3MenuItems, not by s3Item
// itself -- huh.Select's T must be comparable, and s3Item's action field
// (a func) isn't.
//
// This can only be exercised in real interactive use: accessible mode
// (the tested path here) has no keyboard to simulate an abort with --
// see mapMenuPickerErr's own doc comment for the same limitation.
func pickS3MenuItem(w io.Writer, input io.Reader, output io.Writer) (s3Item, error) {
	opts := make([]huh.Option[int], len(s3MenuItems))
	for i, item := range s3MenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("Manage S3 buckets: create, browse and manage objects, configure static websites, and lifecycle policies.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, "(q to go back)", field, input, output); err != nil {
		return s3Item{}, err
	}
	return s3MenuItems[idx], nil
}
