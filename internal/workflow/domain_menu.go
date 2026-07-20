package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/tui"
)

// ErrBackToDomainPicker is returned by a domain's own menu loop (e.g.
// RunMainMenu) when the operator deliberately chooses "Back to domain
// picker", as distinct from a genuine exit signal (Ctrl+C, EOF,
// cancelled ctx), which is still reported as nil -- see RunDomainPicker.
var ErrBackToDomainPicker = errors.New("back to domain picker")

// mapMenuPickerErr maps huh.ErrUserAborted (q/ctrl+c on a domain menu's
// own huh.Select) to ErrBackToDomainPicker, so aborting a domain's menu
// (RunS3Menu, RunMainMenu, RunKeyMgmtMenu, ...) backs up one level like
// explicitly choosing "Back to domain picker," instead of propagating
// out and exiting the whole program. Shared across every huh-converted
// domain menu rather than duplicated per file. Accessible mode (the
// tested path, via each menu's own pickXxxMenuItem menuInput/menuOutput)
// has no way to signal this abort -- there's no keyboard to interrupt a
// plain io.Reader/io.Writer pair -- so this mapping is only exercised
// for real in interactive use; it's covered here as a standalone pure
// function instead (DECISIONS.md, "huh fields are pipe-testable...").
func mapMenuPickerErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrBackToDomainPicker
	}
	return err
}

// menuQuitKeyMap returns a huh.KeyMap with 'q' bound alongside the
// default ctrl+c on Quit (DECISIONS.md, "TUI keybinding conventions"),
// shared by every huh.Select-based Menu-tier choice in this package
// (DESIGN.md's full conversion punch list) instead of duplicated per
// call site.
func menuQuitKeyMap() *huh.KeyMap {
	keymap := huh.NewDefaultKeyMap()
	keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "q"))
	return keymap
}

// filteringField is satisfied by every Select field this package builds
// via pickComparable/pickString/runMenuField's other call sites --
// huh.Select's own GetFiltering, reporting whether the field is
// currently accepting filter text ('/' was pressed, Esc/Enter hasn't
// ended it yet). Used by quitKeyGuard below; a field that doesn't
// satisfy this (none currently exist among runMenuField's callers) just
// skips the guard and runs exactly as before.
type filteringField interface {
	GetFiltering() bool
}

// menuHintReservedLines is the number of terminal rows that sit outside
// whatever height a Menu-tier huh.Form is told to be, and so must be
// subtracted from the real terminal height before calling WithHeight
// (DESIGN.md, "Full-height Menu Tier": "Reserved chrome"). Two lines,
// confirmed empirically (not assumed) by rendering a real form at a
// known WithHeight and counting the actual output:
//   - 1 for runMenuField's own hint (e.g. "(q to go back)"), printed via
//     a plain fmt.Fprintln(w, hint) *before* the form's own bubbletea
//     Program starts, always exactly one short, non-wrapping line.
//   - 1 for huh.Form's own trailing help/keybindings footer line (e.g.
//     "↑ up • ↓ down • / filter • enter submit"), rendered *below*
//     whatever height WithHeight(n) was given -- a form asked for
//     height n renders n+1 lines total, not n, so this must be
//     accounted for separately from the hint above.
const menuHintReservedLines = 2

// quitKeyGuard wraps a *huh.Form, giving every Menu-tier huh.Select two
// things bubbletea's default Form.Update doesn't provide on its own
// (DESIGN.md, "Full-height Menu Tier"):
//
//  1. Disabling the Quit keybinding (ctrl+c and, via menuQuitKeyMap,
//     'q') for exactly as long as the active field reports it's
//     filtering. Without this, huh.Form.Update checks its top-level
//     Quit binding *before* the keystroke ever reaches the field -- so
//     typing a bucket/option name containing the letter 'q' into a
//     filter (e.g. "sql") aborts the whole form on that letter instead
//     of it becoming filter text (reported directly: typing "sql" into
//     Backup Archive & Trim's bucket-filter picker exited clasm
//     outright, every time, on the 'q'). tui/filter.go's own
//     Picker-tier filterState avoids this same class of bug by
//     checking filtering state before its own quit-key case; huh's
//     Form has no such hook, so this instead reaches into the same
//     *key.Binding WithKeyMap already installed and toggles Enabled()
//     before every keystroke -- key.Matches (bubbles/key) honors a
//     disabled binding, so a disabled Quit simply never matches, and
//     the keystroke falls through to the field like any other
//     character. This also means ctrl+c is swallowed as a no-op while
//     filtering rather than quitting -- matching, not diverging from,
//     tui/filter.go's own documented precedent for the Picker tier.
//  2. Keeping the form's own height pinned to the real terminal height
//     (minus menuHintReservedLines), live, on every resize --
//     huh.Form's own tea.WindowSizeMsg handling only *shrinks* a group
//     to fit and only when nothing has ever called WithHeight; it
//     never grows short content to fill unused space. Intercepting the
//     message here and calling WithHeight ourselves, every time, is
//     what makes a 3-option menu still render as a full-terminal-height
//     box instead of a compact one -- the same live-resize mechanism
//     internal/tui/picker.go and listview.go already use for the
//     Picker/List tier.
type quitKeyGuard struct {
	*huh.Form
	setQuitEnabled func(bool)
	filtering      func() bool
}

func (g *quitKeyGuard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		g.setQuitEnabled(!g.filtering())
	case tea.WindowSizeMsg:
		// A non-positive result (a terminal shorter than the reserved
		// hint line -- vanishingly unlikely) is a safe no-op:
		// Form.WithHeight leaves f.height at 0, and Form.Update's own
		// WindowSizeMsg handling (which runs next, via g.Form.Update
		// below) falls back to its own shrink-to-fit sizing.
		g.Form.WithHeight(msg.Height - menuHintReservedLines)
	}
	m, cmd := g.Form.Update(msg)
	g.Form = m.(*huh.Form)
	return g, cmd
}

// runMenuField runs field as a Menu-tier huh.Select (DESIGN.md's full
// conversion punch list): prints hint via w (huh's own footer can't
// show a custom "q: ..." entry -- its SelectKeyMap has no quit/back
// binding to add one to, and KeyBinds() isn't overridable without
// forking huh), binds 'q' alongside ctrl+c on Quit, and runs it through
// quitKeyGuard in real interactive use -- every Menu-tier huh.Select
// this package builds gets both the quit-while-filtering guard (a
// no-op for a field that doesn't implement filteringField -- none
// currently exist among this package's call sites, all of which build
// *huh.Select, but the fallback costs nothing) and live full-height
// sizing (DESIGN.md, "Full-height Menu Tier") uniformly, with no
// per-call-site change needed. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path -- accessible mode has no bubbletea Update
// loop to guard or resize (it reads whole lines via a plain io.Reader),
// so it always runs unguarded, full-height sizing included. Returns
// the raw form error, including huh.ErrUserAborted, unmapped --
// callers choose how to map it: mapMenuPickerErr for domain-loop menus
// that back out to ErrBackToDomainPicker, huhCancelledIsNil for one-off
// workflow sub-choices that just cancel the whole workflow cleanly.
func runMenuField(w io.Writer, hint string, field huh.Field, input io.Reader, output io.Writer) error {
	fmt.Fprintln(w, hint)

	keymap := menuQuitKeyMap()
	form := huh.NewForm(huh.NewGroup(field)).WithKeyMap(keymap).WithTheme(tui.Theme())
	if input != nil {
		form = form.WithAccessible(true).WithInput(input).WithOutput(output)
		return form.Run()
	}

	filtering := func() bool { return false }
	if ff, ok := field.(filteringField); ok {
		filtering = ff.GetFiltering
	}

	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit
	guard := &quitKeyGuard{Form: form, setQuitEnabled: keymap.Quit.SetEnabled, filtering: filtering}
	if _, err := tea.NewProgram(guard).Run(); err != nil {
		return fmt.Errorf("huh: %w", err)
	}
	if guard.Form.State == huh.StateAborted {
		return huh.ErrUserAborted
	}
	return nil
}

// pickString runs a Menu-tier huh.Select (DESIGN.md's full conversion
// punch list) over a fixed list of string options and returns the
// chosen value, via runMenuField. description is optional contextual
// text shown between the title and the options ("" for none -- DESIGN.
// md, "Contextual description text on Menu/Picker-tier screens").
// input/output are nil in production (interactive, real terminal) and
// supplied by tests for the accessible-mode pipe path.
func pickString(w io.Writer, title, description, hint string, options []string, input io.Reader, output io.Writer) (string, error) {
	return pickComparable(w, title, description, hint, options, func(s string) string { return s }, input, output)
}

// maxUnboundedSelectHeight caps a Menu-tier huh.Select's viewport once its
// option list grows past this many rows. Left at huh's own zero value, a
// Select sizes itself to fit every option with no scrolling at all (fine
// for this package's usual fixed, hand-written lists of a handful of
// entries) -- but promptBackupBucket's list is a live listing of every S3
// bucket in the account, unbounded, and without a cap a large account's
// bucket list would render taller than the terminal itself with no way to
// scroll (unlike every Picker-tier tui.RunPicker screen, which always
// windows to the terminal height for exactly this reason). Matches huh's
// own defaultHeight (used when it auto-sizes an OptionsFunc-backed
// Select), so a capped list looks the same as huh's own convention
// elsewhere, not a new one.
const maxUnboundedSelectHeight = 10

// pickComparable runs a Menu-tier huh.Select (DESIGN.md's full
// conversion punch list) over a fixed list of comparable options,
// labelling each with label, and returns the chosen value, via
// runMenuField. description is optional contextual text shown between
// the title and the options ("" for none). input/output are nil in
// production (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickComparable[T comparable](w io.Writer, title, description, hint string, options []T, label func(T) string, input io.Reader, output io.Writer) (T, error) {
	opts := make([]huh.Option[T], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(label(o), o)
	}

	var picked T
	field := huh.NewSelect[T]().
		Title(title).
		Description(description).
		Options(opts...).
		Value(&picked)
	if len(opts) > maxUnboundedSelectHeight {
		field = field.Height(maxUnboundedSelectHeight)
	}

	err := runMenuField(w, hint, field, input, output)
	return picked, err
}

// DomainActions bundles the domain-loop entry points RunDomainPicker
// dispatches to (DESIGN.md, "Navigation: Domain Picker"). CloudFront
// (PLAN.md Phase 21) is postponed to a much later version and
// deliberately not wired into the picker.
type DomainActions struct {
	Compute       func(ctx context.Context) error
	KeyManagement func(ctx context.Context) error
	S3            func(ctx context.Context) error
	// TagManagement is the fourth domain (DESIGN.md, "Tag Management
	// Domain"; DECISIONS.md, "Tag Management: a fourth domain..."):
	// manage or list tags across resource kinds from one place, distinct
	// from Compute's/Key Management's own narrower, resource-scoped tag
	// entry points.
	TagManagement func(ctx context.Context) error
}

// domainItem pairs a domain-picker label with the DomainActions field it
// dispatches to.
type domainItem struct {
	label  string
	action func(DomainActions, context.Context) error
}

// domainItems is DESIGN.md's domain picker, in order. No "Exit" entry --
// DECISIONS.md, "TUI keybinding conventions": 'q' is the universal
// back/quit key everywhere, and at the root menu "back" naturally means
// "exit the program" (there's no parent level to return to), so a
// separate "Exit" choice would just be a second way to do what 'q'
// already does (matching s3MenuItems' own drop of "Back to domain
// picker" in Phase 20.7).
var domainItems = []domainItem{
	{"Compute (EC2 & AMI)", func(a DomainActions, ctx context.Context) error { return a.Compute(ctx) }},
	{"Key Management", func(a DomainActions, ctx context.Context) error { return a.KeyManagement(ctx) }},
	{"S3 (Buckets & Static Websites)", func(a DomainActions, ctx context.Context) error { return a.S3(ctx) }},
	{"Tag Management", func(a DomainActions, ctx context.Context) error { return a.TagManagement(ctx) }},
}

// pickDomainItem runs the domain picker's huh.Select and returns the
// chosen domainItem. Selects by index into domainItems, not by
// domainItem itself -- huh.Select's T must be comparable, and
// domainItem.action (a func) isn't -- the same constraint
// pickS3MenuItem already works around. input/output are nil in
// production (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickDomainItem(w io.Writer, input io.Reader, output io.Writer) (domainItem, error) {
	opts := make([]huh.Option[int], len(domainItems))
	for i, item := range domainItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Pick a domain").
		Description("Choose which part of the AWS account to work in -- EC2 instances and AMIs, SSH key pairs, S3 buckets and static websites, or tags across all of them.").
		Options(opts...).
		Value(&idx)

	// "Exit" (not "back") since this is the root menu.
	if err := runMenuField(w, "(q to exit)", field, input, output); err != nil {
		return domainItem{}, err
	}
	return domainItems[idx], nil
}

// RunDomainPicker runs the top-level domain picker loop (DESIGN.md,
// "Navigation: Domain Picker"): show the domain choices, dispatch to the
// chosen domain's own menu loop, and return to the picker when that loop
// reports ErrBackToDomainPicker. A domain loop returning nil (not
// ErrBackToDomainPicker) means it hit a genuine exit signal itself --
// RunDomainPicker propagates that as a clean exit of the whole program
// rather than looping back to the picker, so an operator inside, say,
// the S3 domain never has to back out twice.
//
// The picker itself is huh.Select (DECISIONS.md, "Convert RunS3Menu to
// huh.Select").
func RunDomainPicker(ctx context.Context, w io.Writer, actions DomainActions) error {
	return runDomainPicker(ctx, w, actions, nil, nil)
}

// runDomainPicker is RunDomainPicker's testable core: menuInput/
// menuOutput are nil in production and supplied by tests to drive the
// same huh.Select through its accessible-mode pipe path instead
// (DECISIONS.md, "huh fields are pipe-testable...").
func runDomainPicker(ctx context.Context, w io.Writer, actions DomainActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickDomainItem(w, menuInput, menuOutput)
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				printExiting(w)
				return nil
			}
			return err
		}

		err = choice.action(actions, ctx)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrBackToDomainPicker) {
			continue
		}
		return err
	}
}

// NotYetImplemented prints a short placeholder message for a domain
// whose menu loop hasn't been built yet (PLAN.md Phases 19-21) and
// returns to the domain picker.
func NotYetImplemented(w io.Writer, domainName string) error {
	fmt.Fprintf(w, "%s is not yet implemented -- coming in a later phase.\n", domainName)
	return ErrBackToDomainPicker
}
