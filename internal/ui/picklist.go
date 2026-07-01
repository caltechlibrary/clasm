package ui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/rsdoiel/termlib"
)

// ErrCancelled is returned by PickList when the user enters 0 to cancel.
var ErrCancelled = errors.New("selection cancelled")

// PickList prints a numbered list of items to t, then reads a selection
// via le, re-prompting on non-numeric or out-of-range input. Entering 0
// cancels, returning ErrCancelled -- replaces ec2_ami_manager.bash's
// show_pick_list.
func PickList[T any](t *termlib.Terminal, le *termlib.LineEditor, items []T, label func(T) string, prompt string) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, errors.New("no items to choose from")
	}

	for i, item := range items {
		t.Printf("%3d) %s\n", i+1, label(item))
	}
	t.Printf("%3d) Cancel\n", 0)
	t.Refresh()

	promptText := prompt + ": "
	for {
		line, err := le.Prompt(promptText)
		if err != nil {
			return zero, err
		}
		line = strings.TrimSpace(line)

		n, convErr := strconv.Atoi(line)
		if convErr != nil {
			t.Printf("invalid selection %q: enter a number\n", line)
			t.Refresh()
			continue
		}
		if n == 0 {
			return zero, ErrCancelled
		}
		if n < 1 || n > len(items) {
			t.Printf("invalid selection %d: choose 0-%d\n", n, len(items))
			t.Refresh()
			continue
		}
		return items[n-1], nil
	}
}
