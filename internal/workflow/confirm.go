package workflow

import (
	"slices"
	"strings"

	"github.com/rsdoiel/termlib"
)

// Confirm asks a simple yes/no question, re-prompting on unrecognized
// input. This is the lightweight confirmation gate for reversible
// actions (Create Instance, Create AMI, Start/Stop, Manage Tags); a
// heavier dry-run + type-to-confirm gate is added when Terminate/Remove
// AMI/Backup Delete need it. Kept as its own reusable step -- not
// inlined into each workflow -- so a future replay engine can route
// through the identical gate rather than a second implementation (see
// DECISIONS.md, "Structure workflows for future record/replay").
func Confirm(t *termlib.Terminal, le *termlib.LineEditor, question string) (bool, error) {
	promptText := question + " [y/N]: "
	for {
		line, err := le.Prompt(promptText)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no", "":
			return false, nil
		default:
			t.Println("please enter y or n")
			t.Refresh()
		}
	}
}

// ConfirmDestructive is the heavier confirmation tier for genuinely
// destructive operations (Terminate, Remove AMI, Backup Delete): the
// user must type one of mustMatch's non-empty values exactly, in a
// single attempt. A mismatch cancels rather than re-prompting --
// unlimited retries would undermine the point of requiring a
// deliberate, correct action (matches ec2_ami_manager.bash's
// type_to_confirm). Empty accepted values (e.g. an untagged instance's
// blank Name) never match, even against blank input.
func ConfirmDestructive(t *termlib.Terminal, le *termlib.LineEditor, mustMatch ...string) (bool, error) {
	var accepted []string
	for _, m := range mustMatch {
		if m != "" {
			accepted = append(accepted, m)
		}
	}

	t.Printf("\nTo proceed, type the exact identifier: %s\n", strings.Join(accepted, " or "))
	t.Refresh()

	input, err := le.Prompt("Enter identifier: ")
	if err != nil {
		return false, err
	}
	input = strings.TrimSpace(input)

	return slices.Contains(accepted, input), nil
}
