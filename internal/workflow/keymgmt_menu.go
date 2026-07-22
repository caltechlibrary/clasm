package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// KeyMgmtActions bundles the Key Management domain's menu entry points,
// mirroring MenuActions' shape for the Compute domain.
type KeyMgmtActions struct {
	CreateKeyPair func(ctx context.Context) error
	ImportKeyPair func(ctx context.Context) error
	DeleteKeyPair func(ctx context.Context) error
	// Refresh re-fetches the key pair listing, silently -- no display.
	// Called once after every successful dispatched action (DECISIONS.md,
	// "Refresh data after each operation") so key-pair-selection prompts
	// elsewhere stay current, and once on entering the Key Management
	// domain.
	Refresh func(ctx context.Context) error
	// ShowResourceLists shows the already-fetched key pair listing in the
	// shared List-tier component (DESIGN.md, "Terminal UI Architecture:
	// Menus, Actions, Lists, and Managers"). Called only by "Show resource
	// lists" -- unlike Refresh, it never runs automatically after other
	// actions (see MenuActions' own Refresh/ShowResourceLists split for
	// why).
	ShowResourceLists func(ctx context.Context) error
}

// keyMgmtItem pairs a Key Management menu label with the KeyMgmtActions
// field it dispatches to.
type keyMgmtItem struct {
	label  string
	action func(KeyMgmtActions, context.Context) error
}

// keyMgmtMenuItems is DESIGN.md's Key Management menu, in order. "Show
// resource lists" leads the menu, same convention as the Compute domain
// (DECISIONS.md, "Move Show resource lists to the top of the Compute
// menu; rename from Refresh"). No "Back to domain picker" entry --
// DECISIONS.md, "TUI keybinding conventions": 'q' is the universal back
// key everywhere, so a redundant menu item would just be a second way
// to do the same thing (matching s3MenuItems' own drop of "Back to
// domain picker" in Phase 20.7).
var keyMgmtMenuItems = []keyMgmtItem{
	{"Show resource lists", func(a KeyMgmtActions, ctx context.Context) error { return a.ShowResourceLists(ctx) }},
	{"Create Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.CreateKeyPair(ctx) }},
	{"Import Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.ImportKeyPair(ctx) }},
	{"Delete Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.DeleteKeyPair(ctx) }},
}

// pickKeyMgmtItem runs the Key Management menu's huh.Select and returns
// the chosen keyMgmtItem. Selects by index into keyMgmtMenuItems, not by
// keyMgmtItem itself -- huh.Select's T must be comparable, and
// keyMgmtItem.action (a func) isn't. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the accessible-
// mode pipe path.
func pickKeyMgmtItem(w io.Writer, input io.Reader, output io.Writer) (keyMgmtItem, error) {
	opts := make([]huh.Option[int], len(keyMgmtMenuItems))
	for i, item := range keyMgmtMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("Manage SSH key pairs used to launch and access EC2 instances.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintGoBack, field, input, output); err != nil {
		return keyMgmtItem{}, err
	}
	return keyMgmtMenuItems[idx], nil
}

// RunKeyMgmtMenu runs the Key Management domain's interactive menu loop,
// the same shape as RunMainMenu: show the menu, dispatch the chosen
// action, refresh the listing after a successful dispatch, and repeat --
// until the picker is aborted ('q'/ctrl+c, reported as
// ErrBackToDomainPicker) or an exit signal is hit (reported as nil,
// which RunDomainPicker treats as "exit the whole program"). A single
// action's error is shown and the loop continues.
//
// The menu picker itself is huh.Select (DECISIONS.md, "Convert RunS3Menu
// to huh.Select").
func RunKeyMgmtMenu(ctx context.Context, w io.Writer, actions KeyMgmtActions) error {
	return runKeyMgmtMenu(ctx, w, actions, nil, nil)
}

// runKeyMgmtMenu is RunKeyMgmtMenu's testable core: menuInput/menuOutput
// are nil in production and supplied by tests to drive the same
// huh.Select through its accessible-mode pipe path instead
// (DECISIONS.md, "huh fields are pipe-testable...").
func runKeyMgmtMenu(ctx context.Context, w io.Writer, actions KeyMgmtActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickKeyMgmtItem(w, menuInput, menuOutput)
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
