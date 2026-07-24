package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/config"
)

// ConfigureActions bundles the Configure clasm domain's menu entry
// points (DESIGN.md, "Configure clasm Domain"), mirroring TagMgmtActions/
// MenuActions' shape. Unlike those, every action here closes over the
// same in-memory config.Config working copy and a shared "unsaved
// changes" flag -- constructed fresh each time RunConfigureMenu is
// entered, not threaded in from main.go's own long-lived state.
type ConfigureActions struct {
	ShowCurrentConfig        func(ctx context.Context) error
	EditRegions              func(ctx context.Context) error
	EditBackupDirectoryRules func(ctx context.Context) error
	EditOriginTag            func(ctx context.Context) error
	// Save persists the working copy to disk (config.Save) and clears the
	// unsaved-changes flag on success.
	Save func(ctx context.Context) error
	// Refresh is a no-op in production -- this domain has nothing to
	// re-fetch from AWS -- but keeps this loop's shape identical to every
	// other domain's (matches RunTagMgmtMenu/RunKeyMgmtMenu's own
	// per-iteration Refresh call), which is also what makes the loop's
	// normal exit path testable via ctx cancellation the same way every
	// other domain menu already is (see menu_test.go's cancelingAction).
	Refresh func(ctx context.Context) error
	// Dirty reports whether there are unsaved changes pending -- checked
	// when the operator quits ('q'/ctrl+c) so the loop can warn before
	// discarding (DECISIONS.md, "Configure clasm domain: explicit Save,
	// region changes deferred to next launch").
	Dirty func() bool
}

// configureItem pairs a Configuration menu label with the
// ConfigureActions field it dispatches to.
type configureItem struct {
	label  string
	action func(ConfigureActions, context.Context) error
}

// configureMenuItems is DESIGN.md's Configure clasm menu, in order. No
// "Back to domain picker" entry -- DECISIONS.md, "TUI keybinding
// conventions": 'q' is the universal back key everywhere.
var configureMenuItems = []configureItem{
	{"Show current config", func(a ConfigureActions, ctx context.Context) error { return a.ShowCurrentConfig(ctx) }},
	{"Edit regions", func(a ConfigureActions, ctx context.Context) error { return a.EditRegions(ctx) }},
	{"Edit backup directory rules", func(a ConfigureActions, ctx context.Context) error { return a.EditBackupDirectoryRules(ctx) }},
	{"Edit Origin tag config", func(a ConfigureActions, ctx context.Context) error { return a.EditOriginTag(ctx) }},
	{"Save", func(a ConfigureActions, ctx context.Context) error { return a.Save(ctx) }},
}

// pickConfigureItem runs the Configuration menu's huh.Select and returns
// the chosen configureItem. Selects by index into configureMenuItems,
// not by configureItem itself -- huh.Select's T must be comparable, and
// configureItem.action (a func) isn't. input/output are nil in
// production (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickConfigureItem(w io.Writer, input io.Reader, output io.Writer) (configureItem, error) {
	opts := make([]huh.Option[int], len(configureMenuItems))
	for i, item := range configureMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Configuration").
		Description("View or edit clasm's own settings (~/.clasm). Nothing is written until Save.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintGoBack, field, input, output); err != nil {
		return configureItem{}, err
	}
	return configureMenuItems[idx], nil
}

// RunConfigureMenu runs the Configure clasm domain's interactive menu
// loop (DESIGN.md, "Configure clasm Domain"): load configPath into an
// in-memory working copy, let the operator view/edit it, and persist
// only when Save is explicitly chosen.
func RunConfigureMenu(ctx context.Context, w io.Writer, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	var dirty bool

	actions := ConfigureActions{
		ShowCurrentConfig: func(ctx context.Context) error {
			displayConfig(w, cfg)
			return nil
		},
		EditRegions: func(ctx context.Context) error {
			changed, err := editRegions(w, &cfg, nil, nil)
			if changed {
				dirty = true
			}
			return err
		},
		EditBackupDirectoryRules: func(ctx context.Context) error {
			changed, err := editBackupDirectoryRules(w, &cfg, nil, nil)
			if changed {
				dirty = true
			}
			return err
		},
		EditOriginTag: func(ctx context.Context) error {
			changed, err := editOriginTag(w, &cfg, nil, nil)
			if changed {
				dirty = true
			}
			return err
		},
		Save: func(ctx context.Context) error {
			if err := config.Save(configPath, cfg); err != nil {
				return err
			}
			dirty = false
			fmt.Fprintln(w, "Saved. Region changes take effect the next time clasm is launched.")
			return nil
		},
		Refresh: func(ctx context.Context) error { return nil },
		Dirty:   func() bool { return dirty },
	}
	return runConfigureMenu(ctx, w, actions, nil, nil)
}

// runConfigureMenu is RunConfigureMenu's testable core, same shape as
// runTagMgmtMenu: show the menu, dispatch the chosen action, refresh
// (a no-op here) after a successful dispatch, and repeat -- until the
// picker is aborted ('q'/ctrl+c, mapped to ErrBackToDomainPicker, warning
// first if actions.Dirty() is true) or an exit signal is hit. A single
// action's error is shown and the loop continues.
func runConfigureMenu(ctx context.Context, w io.Writer, actions ConfigureActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickConfigureItem(w, menuInput, menuOutput)
		if err != nil {
			warnIfDirtyOnQuit(w, actions.Dirty())
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

		pauseForAcknowledgment(menuInput, menuOutput)

		if err := actions.Refresh(ctx); err != nil {
			fmt.Fprintf(w, "Error refreshing: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
		}
	}
}
