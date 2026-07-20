package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// TagMgmtActions bundles the Tag Management domain's menu entry
// points, mirroring MenuActions/KeyMgmtActions' shape for the other
// domains (DESIGN.md, "Tag Management Domain"; DECISIONS.md, "Tag
// Management: a fourth domain..."). S3 Bucket isn't wired in yet
// (PLAN.md Phase 20.30's remaining work) -- Refresh below only
// re-fetches the four EC2-backed resource types for now.
type TagMgmtActions struct {
	ManageTags  func(ctx context.Context) error
	ShowAllTags func(ctx context.Context) error
	// Refresh re-fetches instance/AMI/launch-template/key-pair data,
	// silently -- no display. Called once after every successful
	// dispatched action (DECISIONS.md, "Refresh data after each
	// operation") and once on entering the Tag Management domain.
	// Independent of Compute's/Key Management's own refresh closures --
	// an operator may reach this domain before visiting either of
	// those (see refreshKeyMgmt's own comment in main.go for the same
	// reasoning).
	Refresh func(ctx context.Context) error
}

// tagMgmtItem pairs a Tag Management menu label with the
// TagMgmtActions field it dispatches to.
type tagMgmtItem struct {
	label  string
	action func(TagMgmtActions, context.Context) error
}

// tagMgmtMenuItems is DESIGN.md's Tag Management menu, in order. No
// "Back to domain picker" entry -- DECISIONS.md, "TUI keybinding
// conventions": 'q' is the universal back key everywhere, so a
// redundant menu item would just be a second way to do the same thing.
var tagMgmtMenuItems = []tagMgmtItem{
	{"Manage tags", func(a TagMgmtActions, ctx context.Context) error { return a.ManageTags(ctx) }},
	{"Show all tags", func(a TagMgmtActions, ctx context.Context) error { return a.ShowAllTags(ctx) }},
}

// pickTagMgmtItem runs the Tag Management menu's huh.Select and returns
// the chosen tagMgmtItem. Selects by index into tagMgmtMenuItems, not
// by tagMgmtItem itself -- huh.Select's T must be comparable, and
// tagMgmtItem.action (a func) isn't. input/output are nil in
// production (interactive, real terminal) and supplied by tests for
// the accessible-mode pipe path.
func pickTagMgmtItem(w io.Writer, input io.Reader, output io.Writer) (tagMgmtItem, error) {
	opts := make([]huh.Option[int], len(tagMgmtMenuItems))
	for i, item := range tagMgmtMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("Manage tags on an EC2 instance, AMI, launch template, or key pair, or list every tag on all resources of one kind.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, "(q to go back)", field, input, output); err != nil {
		return tagMgmtItem{}, err
	}
	return tagMgmtMenuItems[idx], nil
}

// RunTagMgmtMenu runs the Tag Management domain's interactive menu
// loop, the same shape as RunKeyMgmtMenu: show the menu, dispatch the
// chosen action, refresh the listings after a successful dispatch, and
// repeat -- until the picker is aborted ('q'/ctrl+c, reported as
// ErrBackToDomainPicker) or an exit signal is hit (reported as nil,
// which RunDomainPicker treats as "exit the whole program"). A single
// action's error is shown and the loop continues.
func RunTagMgmtMenu(ctx context.Context, w io.Writer, actions TagMgmtActions) error {
	return runTagMgmtMenu(ctx, w, actions, nil, nil)
}

// runTagMgmtMenu is RunTagMgmtMenu's testable core: menuInput/
// menuOutput are nil in production and supplied by tests to drive the
// same huh.Select through its accessible-mode pipe path instead.
func runTagMgmtMenu(ctx context.Context, w io.Writer, actions TagMgmtActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickTagMgmtItem(w, menuInput, menuOutput)
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
