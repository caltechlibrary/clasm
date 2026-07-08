package workflow

import (
	"context"
	"errors"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/ui"
)

// ErrBackToDomainPicker is returned by a domain's own menu loop (e.g.
// RunMainMenu) when the operator deliberately chooses "Back to domain
// picker", as distinct from a genuine exit signal (Ctrl+C, EOF,
// cancelled ctx), which is still reported as nil -- see RunDomainPicker.
var ErrBackToDomainPicker = errors.New("back to domain picker")

// DomainActions bundles the four domain-loop entry points RunDomainPicker
// dispatches to (DESIGN.md, "Navigation: Domain Picker"). CloudFront is
// not yet implemented (PLAN.md Phase 21); main.go wires it to
// NotYetImplemented until then.
type DomainActions struct {
	Compute       func(ctx context.Context) error
	KeyManagement func(ctx context.Context) error
	S3            func(ctx context.Context) error
	CloudFront    func(ctx context.Context) error
}

// domainItem pairs a domain-picker label with the DomainActions field it
// dispatches to; action is nil for "Exit".
type domainItem struct {
	label  string
	action func(DomainActions, context.Context) error
}

// domainItems is DESIGN.md's domain picker, in order.
var domainItems = []domainItem{
	{"Compute (EC2 & AMI)", func(a DomainActions, ctx context.Context) error { return a.Compute(ctx) }},
	{"Key Management", func(a DomainActions, ctx context.Context) error { return a.KeyManagement(ctx) }},
	{"S3 (Buckets & Static Websites)", func(a DomainActions, ctx context.Context) error { return a.S3(ctx) }},
	{"CloudFront", func(a DomainActions, ctx context.Context) error { return a.CloudFront(ctx) }},
	{"Exit", nil},
}

func domainItemLabel(item domainItem) string { return item.label }

// RunDomainPicker runs the top-level domain picker loop (DESIGN.md,
// "Navigation: Domain Picker"): show the domain choices, dispatch to the
// chosen domain's own menu loop, and return to the picker when that loop
// reports ErrBackToDomainPicker. A domain loop returning nil (not
// ErrBackToDomainPicker) means it hit a genuine exit signal itself --
// RunDomainPicker propagates that as a clean exit of the whole program
// rather than looping back to the picker, so an operator inside, say,
// the S3 domain never has to back out twice.
func RunDomainPicker(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, actions DomainActions) error {
	for {
		if ctx.Err() != nil {
			printExiting(t)
			return nil
		}

		choice, err := ui.PickList(t, le, domainItems, domainItemLabel, "Pick a domain")
		if err != nil {
			if isExitSignal(err) {
				printExiting(t)
				return nil
			}
			return err
		}

		if choice.action == nil {
			printExiting(t)
			return nil
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
func NotYetImplemented(t *termlib.Terminal, domainName string) error {
	t.Printf("%s is not yet implemented -- coming in a later phase.\n", domainName)
	t.Refresh()
	return ErrBackToDomainPicker
}
