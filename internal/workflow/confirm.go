package workflow

import (
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
