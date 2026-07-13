package workflow

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/tui"
)

type confirmConfig struct {
	input  io.Reader
	output io.Writer
}

// ConfirmOption configures Confirm/ConfirmDestructive's (for tests)
// accessible-mode I/O.
type ConfirmOption func(*confirmConfig)

// WithConfirmIO drives Confirm/ConfirmDestructive's field through huh's
// accessible-mode pipe path (WithAccessible(true).WithInput/WithOutput)
// instead of a real terminal. nil in production; callers with their own
// menuInput/menuOutput (or similar) pass them through here so tests can
// drive a confirmation the same way they already drive a Menu-tier
// huh.Select or a ui.Prompt (see ui.WithIO).
func WithConfirmIO(input io.Reader, output io.Writer) ConfirmOption {
	return func(c *confirmConfig) { c.input, c.output = input, output }
}

// Confirm asks a simple yes/no question via huh.Confirm -- this is the
// lightweight confirmation gate for reversible actions (Create Instance,
// Create AMI, Start/Stop, Manage Tags); a heavier dry-run + type-to-
// confirm gate is added when Terminate/Remove AMI/Backup Delete need it.
// Kept as its own reusable step -- not inlined into each workflow -- so
// a future replay engine can route through the identical gate rather
// than a second implementation (see DECISIONS.md, "Structure workflows
// for future record/replay").
func Confirm(question string, opts ...ConfirmOption) (bool, error) {
	cfg := &confirmConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var ok bool
	field := huh.NewConfirm().Title(question).Value(&ok)

	form := huh.NewForm(huh.NewGroup(field)).WithTheme(tui.Theme())
	if cfg.input != nil {
		form = form.WithAccessible(true).WithInput(cfg.input).WithOutput(cfg.output)
	}
	if err := form.Run(); err != nil {
		return false, err
	}
	return ok, nil
}

// ConfirmDestructive is the heavier confirmation tier for genuinely
// destructive operations (Terminate, Remove AMI, Backup Delete): the
// user must type one of mustMatch's non-empty values exactly, in a
// single attempt. A mismatch cancels rather than re-prompting --
// unlimited retries would undermine the point of requiring a
// deliberate, correct action (matches ec2_ami_manager.bash's
// type_to_confirm). Empty accepted values (e.g. an untagged instance's
// blank Name) never match, even against blank input. Built on
// huh.NewInput() with no validator -- a validator would make huh
// re-prompt until correct, which would change these single-attempt
// semantics.
func ConfirmDestructive(mustMatch []string, opts ...ConfirmOption) (bool, error) {
	cfg := &confirmConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var accepted []string
	for _, m := range mustMatch {
		if m != "" {
			accepted = append(accepted, m)
		}
	}

	var typed string
	field := huh.NewInput().
		Title("Enter identifier").
		Description(fmt.Sprintf("To proceed, type the exact identifier: %s", strings.Join(accepted, " or "))).
		Value(&typed)

	form := huh.NewForm(huh.NewGroup(field)).WithTheme(tui.Theme())
	if cfg.input != nil {
		form = form.WithAccessible(true).WithInput(cfg.input).WithOutput(cfg.output)
	}
	if err := form.Run(); err != nil {
		return false, err
	}
	return slices.Contains(accepted, strings.TrimSpace(typed)), nil
}
