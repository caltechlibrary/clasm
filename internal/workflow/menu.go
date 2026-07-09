package workflow

import (
	"context"
	"errors"
	"io"

	"github.com/rsdoiel/termlib"

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
	// Refresh re-fetches and re-displays the instance/AMI listings. Called
	// once after every successful dispatched action (DECISIONS.md,
	// "Refresh data after each operation"), and directly for the
	// "Show resource lists" menu item itself.
	Refresh func(ctx context.Context) error
}

// menuItem pairs a main-menu label with the MenuActions field it
// dispatches to; action is nil for "Back to domain picker".
type menuItem struct {
	label  string
	action func(MenuActions, context.Context) error
}

// mainMenuItems is DESIGN.md's Main Menu, in order. "Show resource
// lists" leads the menu (DECISIONS.md, "Move Show resource lists to the
// top of the Compute menu; rename from Refresh") -- it's the natural
// first move on entering the domain (orient before acting), not just
// one action among ten.
var mainMenuItems = []menuItem{
	{"Show resource lists", func(a MenuActions, ctx context.Context) error { return a.Refresh(ctx) }},
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
	{"Back to domain picker", nil},
}

func menuItemLabel(item menuItem) string { return item.label }

// RunMainMenu runs the Compute domain's interactive menu loop (DESIGN.md,
// "Compute Domain (EC2 & AMI)"): show the 12-option menu, dispatch the
// chosen action, refresh listings after a successful dispatch, and
// repeat -- until "Back to domain picker" is chosen (returns
// ErrBackToDomainPicker), a cancelled ctx (e.g. Ctrl+C delivered as
// os.Interrupt between prompts), or an interrupted/EOF prompt (e.g.
// Ctrl+C/Ctrl+D during an active prompt, which termlib surfaces as an
// error rather than a process signal) -- the latter two report nil,
// which RunDomainPicker treats as "exit the whole program", not "return
// to the picker". A single action's error is shown and the loop
// continues -- one failed operation shouldn't force restarting the
// whole CLI.
func RunMainMenu(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, actions MenuActions) error {
	for {
		if ctx.Err() != nil {
			printExiting(t)
			return nil
		}

		choice, err := ui.PickList(t, le, mainMenuItems, menuItemLabel, "Choose an option")
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

func printExiting(t *termlib.Terminal) {
	t.Println("\nExiting.")
	t.Refresh()
}

func isExitSignal(err error) bool {
	return errors.Is(err, ui.ErrCancelled) || errors.Is(err, termlib.ErrInterrupted) || errors.Is(err, io.EOF)
}
