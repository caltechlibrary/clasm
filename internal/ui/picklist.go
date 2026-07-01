package ui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/rsdoiel/termlib"
)

// ErrCancelled is returned by PickList when the user enters 0 to cancel.
var ErrCancelled = errors.New("selection cancelled")

// pickListPageSize bounds how many items PickList shows at once (PLAN.md,
// Phase 15, "Pagination for large lists (>50 items)"). Selection numbers
// are global across the whole list, not per-page, so a page boundary
// never changes what number picks a given item.
const pickListPageSize = 50

// PickList prints a numbered list of items to t, then reads a selection
// via le, re-prompting on non-numeric or out-of-range input. Entering 0
// cancels, returning ErrCancelled. Lists longer than pickListPageSize
// are paginated: 'n'/'p' move to the next/previous page; item numbers
// stay global, so typing a number always picks the same item regardless
// of which page is currently shown. Replaces ec2_ami_manager.bash's
// show_pick_list.
func PickList[T any](t *termlib.Terminal, le *termlib.LineEditor, items []T, label func(T) string, prompt string) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, errors.New("no items to choose from")
	}

	totalPages := (len(items) + pickListPageSize - 1) / pickListPageSize
	page := 0
	promptText := prompt + ": "

	for {
		start := page * pickListPageSize
		end := min(start+pickListPageSize, len(items))
		for i := start; i < end; i++ {
			t.Printf("%3d) %s\n", i+1, label(items[i]))
		}
		t.Printf("%3d) Cancel\n", 0)
		if totalPages > 1 {
			t.Printf("Page %d/%d -- 'n' next page, 'p' previous page\n", page+1, totalPages)
		}
		t.Refresh()

		line, err := le.Prompt(promptText)
		if err != nil {
			return zero, err
		}
		line = strings.TrimSpace(line)

		if totalPages > 1 {
			switch strings.ToLower(line) {
			case "n":
				if page < totalPages-1 {
					page++
				}
				continue
			case "p":
				if page > 0 {
					page--
				}
				continue
			}
		}

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
