package ui

import (
	"fmt"
	"strings"

	"github.com/rsdoiel/termlib"
)

type promptConfig struct {
	def      string
	validate func(string) error
}

// PromptOption configures Prompt's optional default value and validator.
type PromptOption func(*promptConfig)

// WithDefault sets the value Prompt returns when the user enters an
// empty line.
func WithDefault(def string) PromptOption {
	return func(c *promptConfig) { c.def = def }
}

// WithValidator sets a function Prompt calls on the (post-default) input;
// a non-nil error re-prompts instead of returning.
func WithValidator(fn func(string) error) PromptOption {
	return func(c *promptConfig) { c.validate = fn }
}

// Prompt reads a single free-text line via le, applying an optional
// default value and validator, re-prompting on validation failure --
// replaces ec2_ami_manager.bash's repeated "echo -n ...; read -r ..."
// pattern.
func Prompt(t *termlib.Terminal, le *termlib.LineEditor, label string, opts ...PromptOption) (string, error) {
	cfg := &promptConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	promptText := label + ": "
	if cfg.def != "" {
		promptText = fmt.Sprintf("%s [%s]: ", label, cfg.def)
	}

	for {
		line, err := le.Prompt(promptText)
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" && cfg.def != "" {
			line = cfg.def
		}

		if cfg.validate != nil {
			if verr := cfg.validate(line); verr != nil {
				t.Printf("invalid input: %v\n", verr)
				t.Refresh()
				continue
			}
		}
		return line, nil
	}
}
