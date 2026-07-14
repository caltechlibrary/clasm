package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// filterState is the shared "/"-triggered substring-filter behavior for
// Picker- and List-tier screens (DESIGN.md's keybinding conventions
// table: "/  Filter  Menus, pickers, lists, managers"): '/' enters
// filter-typing mode, narrows visible rows by case-insensitive
// substring match against each row's rendered text, Enter commits
// (keeps navigating the narrowed list), Esc clears it. Embedding one of
// these in both PickerModel and ListViewModel keeps their filtering
// behavior identical by construction rather than by convention.
type filterState struct {
	rows []string // reference to the owning model's cfg.Rows; never mutated here

	filtering bool
	filter    string
	visible   []int // indices into rows currently visible (post-filter)
	cursor    int   // index into visible
}

// newFilterState builds a filterState over rows, with every row
// initially visible.
func newFilterState(rows []string) *filterState {
	f := &filterState{rows: rows}
	f.apply()
	return f
}

func (f *filterState) apply() {
	f.visible = f.visible[:0]
	if f.filter == "" {
		for i := range f.rows {
			f.visible = append(f.visible, i)
		}
	} else {
		lf := strings.ToLower(f.filter)
		for i, r := range f.rows {
			if strings.Contains(strings.ToLower(r), lf) {
				f.visible = append(f.visible, i)
			}
		}
	}
	if f.cursor >= len(f.visible) {
		f.cursor = max(len(f.visible)-1, 0)
	}
}

// moveCursor moves the cursor within the currently visible rows by
// delta (-1 up, +1 down), clamped to bounds.
func (f *filterState) moveCursor(delta int) {
	next := f.cursor + delta
	if next < 0 || next >= len(f.visible) {
		return
	}
	f.cursor = next
}

// handleIdleKey processes the keys shared by both models when filtering
// is NOT active: '/' starts it, Esc clears an already-set filter,
// up/down/k/j move the cursor within the visible rows. Callers handle
// every other key themselves (q/ctrl+c to quit, enter to select, ...)
// before falling back to this for anything left over.
func (f *filterState) handleIdleKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "up", "k":
		f.moveCursor(-1)
	case "down", "j":
		f.moveCursor(1)
	case "/":
		f.filtering = true
	case "esc":
		if f.filter != "" {
			f.filter = ""
			f.apply()
		}
	}
}

// handleFilterKey edits the filter buffer while it has focus ('/'),
// applying it on every keystroke, committing (leaving filter-typing
// mode, keeping the filter) on Enter, and clearing it on Esc -- the same
// shape as internal/filemanager's own command-line key handling
// (handleCommandLineKey). Deliberately does not special-case 'q'/ctrl+c
// here: while typing filter text, every key including those is literal
// input, matching that same precedent.
func (f *filterState) handleFilterKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEnter:
		f.filtering = false
	case tea.KeyEsc:
		f.filtering = false
		f.filter = ""
		f.apply()
	case tea.KeyBackspace:
		if len(f.filter) > 0 {
			f.filter = f.filter[:len(f.filter)-1]
			f.apply()
		}
	default:
		if len(msg.Runes) > 0 {
			f.filter += string(msg.Runes)
			f.apply()
		}
	}
}

func (f *filterState) statusLine() string {
	if f.filtering {
		return "/" + f.filter + "█"
	}
	if f.filter != "" {
		return fmt.Sprintf("filter: %s", f.filter)
	}
	return "filter: none"
}

// baseChromeRows counts the box rows that always exist regardless of
// header/filter: top border, the divider directly below the scrollable
// body, the legend row, and the bottom border.
const baseChromeRows = 4

// headerChromeRows/descriptionChromeRows/filterChromeRowCount count the
// extra rows a header line, a description line, or the filter status
// line contribute (the line itself plus the divider immediately below
// it), when present.
const headerChromeRows = 2
const descriptionChromeRows = 2
const filterChromeRowCount = 2

// filterableWindowHeight computes how many body rows fit in the
// scrollable area of a filterable, header-optional List/Picker-tier
// screen, given the real (or default) terminal height. Shared by
// ListViewModel and PickerModel so their sizing stays identical by
// construction.
func filterableWindowHeight(height int, hasHeader, hasDescription bool) int {
	if height <= 0 {
		height = defaultListViewHeight
	}
	chrome := baseChromeRows + filterChromeRowCount
	if hasHeader {
		chrome += headerChromeRows
	}
	if hasDescription {
		chrome += descriptionChromeRows
	}
	return max(height-chrome, minListViewRows)
}
