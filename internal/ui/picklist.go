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
// stay global (within the current, possibly filtered, list), so typing
// a number always picks the same item regardless of which page is
// currently shown. Non-numeric, non-empty input that isn't 'n'/'p' is
// treated as filter text: it narrows the list to items whose label
// case-insensitively contains it, so an operator who knows a bucket or
// instance name can type it instead of paging to find it; a blank line
// clears an active filter. Replaces ec2_ami_manager.bash's
// show_pick_list.
func PickList[T any](t *termlib.Terminal, le *termlib.LineEditor, items []T, label func(T) string, prompt string) (T, error) {
	var zero T
	if len(items) == 0 {
		return zero, errors.New("no items to choose from")
	}

	active := items
	filter := ""
	page := 0
	promptText := prompt + ": "

	// Printed once, before the list, so a wrong menu selection is
	// visible immediately instead of only after reading through every
	// item (see internal/ui/color.go's Highlight).
	t.Println(Highlight(prompt))
	t.Refresh()

	for {
		totalPages := (len(active) + pickListPageSize - 1) / pickListPageSize
		start := page * pickListPageSize
		end := min(start+pickListPageSize, len(active))
		for i := start; i < end; i++ {
			t.Printf("%3d) %s\n", i+1, label(active[i]))
		}
		t.Printf("%3d) Cancel\n", 0)
		if totalPages > 1 {
			t.Printf("Page %d/%d -- 'n' next page, 'p' previous page\n", page+1, totalPages)
		}
		if filter != "" {
			t.Printf("Filter %q active (%d of %d shown) -- press Enter with no text to clear\n", filter, len(active), len(items))
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

		if line == "" {
			if filter != "" {
				active = items
				filter = ""
				page = 0
			}
			continue
		}

		n, convErr := strconv.Atoi(line)
		if convErr != nil {
			matches := filterByLabel(items, label, line)
			if len(matches) == 0 {
				t.Printf("no matches for %q\n", line)
				t.Refresh()
				continue
			}
			active = matches
			filter = line
			page = 0
			continue
		}
		if n == 0 {
			return zero, ErrCancelled
		}
		if n < 1 || n > len(active) {
			t.Printf("invalid selection %d: choose 0-%d\n", n, len(active))
			t.Refresh()
			continue
		}
		return active[n-1], nil
	}
}

// filterByLabel returns the items whose rendered label case-insensitively
// contains query, preserving their original relative order.
func filterByLabel[T any](items []T, label func(T) string, query string) []T {
	q := strings.ToLower(query)
	var matches []T
	for _, it := range items {
		if strings.Contains(strings.ToLower(label(it)), q) {
			matches = append(matches, it)
		}
	}
	return matches
}
