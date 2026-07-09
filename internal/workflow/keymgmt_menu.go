package workflow

import (
	"context"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// KeyMgmtActions bundles the Key Management domain's menu entry points,
// mirroring MenuActions' shape for the Compute domain.
type KeyMgmtActions struct {
	CreateKeyPair func(ctx context.Context) error
	ImportKeyPair func(ctx context.Context) error
	DeleteKeyPair func(ctx context.Context) error
	// Refresh re-fetches and re-displays the key pair listing. Called
	// once after every successful dispatched action (DECISIONS.md,
	// "Refresh data after each operation"), and directly for the
	// "Show resource lists" menu item itself.
	Refresh func(ctx context.Context) error
}

// keyMgmtItem pairs a Key Management menu label with the KeyMgmtActions
// field it dispatches to; action is nil for "Back to domain picker".
type keyMgmtItem struct {
	label  string
	action func(KeyMgmtActions, context.Context) error
}

// keyMgmtMenuItems is DESIGN.md's Key Management menu, in order. "Show
// resource lists" leads the menu, same convention as the Compute domain
// (DECISIONS.md, "Move Show resource lists to the top of the Compute
// menu; rename from Refresh").
var keyMgmtMenuItems = []keyMgmtItem{
	{"Show resource lists", func(a KeyMgmtActions, ctx context.Context) error { return a.Refresh(ctx) }},
	{"Create Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.CreateKeyPair(ctx) }},
	{"Import Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.ImportKeyPair(ctx) }},
	{"Delete Key Pair", func(a KeyMgmtActions, ctx context.Context) error { return a.DeleteKeyPair(ctx) }},
	{"Back to domain picker", nil},
}

func keyMgmtItemLabel(item keyMgmtItem) string { return item.label }

// RunKeyMgmtMenu runs the Key Management domain's interactive menu loop,
// the same shape as RunMainMenu: show the menu, dispatch the chosen
// action, refresh the listing after a successful dispatch, and repeat --
// until "Back to domain picker" is chosen (returns ErrBackToDomainPicker)
// or an exit signal is hit (reported as nil, which RunDomainPicker treats
// as "exit the whole program"). A single action's error is shown and the
// loop continues.
func RunKeyMgmtMenu(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, actions KeyMgmtActions) error {
	for {
		if ctx.Err() != nil {
			printExiting(t)
			return nil
		}

		choice, err := ui.PickList(t, le, keyMgmtMenuItems, keyMgmtItemLabel, "Choose an option")
		if err != nil {
			if isExitSignal(err) {
				printExiting(t)
				return nil
			}
			return err
		}

		if choice.action == nil {
			return ErrBackToDomainPicker
		}

		if err := choice.action(actions, ctx); err != nil {
			if isExitSignal(err) {
				printExiting(t)
				return nil
			}
			t.Printf("Error: %s\n", formatError(err))
			t.Refresh()
			continue
		}

		if err := actions.Refresh(ctx); err != nil {
			t.Printf("Error refreshing listings: %s\n", formatError(err))
			t.Refresh()
		}
	}
}
