package ui

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
)

// ErrCancelled is returned by callers (e.g. the AZ/ENA-incompatibility
// remediation menus) when the operator chooses a "Cancel" option from a
// fixed huh.Select -- independent of tui.ErrCancelled (returned by the
// Picker tier) and huh.ErrUserAborted (q/ctrl+c), which callers map
// alongside this one at their own call sites.
var ErrCancelled = errors.New("selection cancelled")

type promptConfig struct {
	def      string
	validate func(string) error
	input    io.Reader
	output   io.Writer
}

// PromptOption configures Prompt's optional default value, validator,
// and (for tests) accessible-mode I/O.
type PromptOption func(*promptConfig)

// WithDefault sets the value Prompt returns when the user submits an
// empty line -- pre-filled into the field itself, so the operator sees
// and can edit the default rather than it being an invisible fallback.
func WithDefault(def string) PromptOption {
	return func(c *promptConfig) { c.def = def }
}

// WithValidator sets a function Prompt calls on the input; a non-nil
// error re-prompts instead of returning (huh.Input's own behavior,
// replacing the manual re-prompt loop the termlib-based version needed).
func WithValidator(fn func(string) error) PromptOption {
	return func(c *promptConfig) { c.validate = fn }
}

// WithIO drives Prompt's field through huh's accessible-mode pipe path
// (WithAccessible(true).WithInput/WithOutput) instead of a real
// terminal. nil in production; callers one or more levels up the call
// chain from a workflow function's own menuInput/menuOutput pass them
// through here so tests can drive a free-text prompt the same way they
// already drive a Menu-tier huh.Select (DECISIONS.md, "huh fields are
// pipe-testable via WithAccessible(true).WithInput/WithOutput").
func WithIO(input io.Reader, output io.Writer) PromptOption {
	return func(c *promptConfig) { c.input, c.output = input, output }
}

// Prompt reads a single free-text line via a huh.Input field, applying
// an optional default value and validator -- replaces
// ec2_ami_manager.bash's repeated "echo -n ...; read -r ..." pattern.
func Prompt(label string, opts ...PromptOption) (string, error) {
	cfg := &promptConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	value := cfg.def
	field := huh.NewInput().Title(label).Value(&value)
	if cfg.validate != nil {
		// A blank submission always means "use the default" -- skip
		// validation rather than running it against "", which would
		// reject a validator that (correctly) never accepts blank input
		// itself (e.g. validateAMIName). Prefixed to match the message
		// this project's callers/tests have always expected from the
		// termlib-based Prompt.
		field.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" && cfg.def != "" {
				return nil
			}
			if err := cfg.validate(s); err != nil {
				return fmt.Errorf("invalid input: %w", err)
			}
			return nil
		})
	}

	form := huh.NewForm(huh.NewGroup(field))
	if cfg.input != nil {
		form = form.WithAccessible(true).WithInput(cfg.input).WithOutput(cfg.output)
	}
	if err := form.Run(); err != nil {
		return "", err
	}
	// The accessible-mode path already substitutes the default on a
	// blank line (accessibility.PromptString's own cmp.Or); the
	// interactive path doesn't, since Value(&value) pre-fills the
	// default as editable text and a normal Enter submits it unchanged
	// -- this only matters if the operator explicitly clears a pre-filled
	// default and submits empty.
	if strings.TrimSpace(value) == "" && cfg.def != "" {
		value = cfg.def
	}
	return value, nil
}
