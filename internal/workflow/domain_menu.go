package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
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

// runMenuField runs field as a Menu-tier huh.Select (DESIGN.md's full
// conversion punch list): prints hint via w (huh's own footer can't
// show a custom "q: ..." entry -- its SelectKeyMap has no quit/back
// binding to add one to, and KeyBinds() isn't overridable without
// forking huh), binds 'q' alongside ctrl+c on Quit, and runs it.
// input/output are nil in production (interactive, real terminal) and
// supplied by tests for the accessible-mode pipe path. Returns the raw
// form error, including huh.ErrUserAborted, unmapped -- callers choose
// how to map it: mapMenuPickerErr for domain-loop menus that back out
// to ErrBackToDomainPicker, huhCancelledIsNil for one-off workflow
// sub-choices that just cancel the whole workflow cleanly.
func runMenuField(w io.Writer, hint string, field huh.Field, input io.Reader, output io.Writer) error {
	fmt.Fprintln(w, hint)

	form := huh.NewForm(huh.NewGroup(field)).WithKeyMap(menuQuitKeyMap()).WithTheme(tui.Theme())
	if input != nil {
		form = form.WithAccessible(true).WithInput(input).WithOutput(output)
	}
	return form.Run()
}

// pickString runs a Menu-tier huh.Select (DESIGN.md's full conversion
// punch list) over a fixed list of string options and returns the
// chosen value, via runMenuField. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickString(w io.Writer, title, hint string, options []string, input io.Reader, output io.Writer) (string, error) {
	return pickComparable(w, title, hint, options, func(s string) string { return s }, input, output)
}

// pickComparable runs a Menu-tier huh.Select (DESIGN.md's full
// conversion punch list) over a fixed list of comparable options,
// labelling each with label, and returns the chosen value, via
// runMenuField. input/output are nil in production (interactive, real
// terminal) and supplied by tests for the accessible-mode pipe path.
func pickComparable[T comparable](w io.Writer, title, hint string, options []T, label func(T) string, input io.Reader, output io.Writer) (T, error) {
	opts := make([]huh.Option[T], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(label(o), o)
	}

	var picked T
	field := huh.NewSelect[T]().
		Title(title).
		Options(opts...).
		Value(&picked)

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
