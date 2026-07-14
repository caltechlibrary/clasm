package ui

import (
	"io"

	"github.com/charmbracelet/x/ansi"
)

// ClearScreen erases the terminal and homes the cursor, using the same
// two escape sequences bubbletea's own tea.ClearScreen sends -- so
// clasm's startup looks like every other screen in the app clearing
// itself, not a one-off ANSI sequence of its own (DECISIONS.md, "Clear
// the screen at startup"). Called once, before clasm's first line of
// output, so old terminal scrollback never lingers behind the app.
func ClearScreen(w io.Writer) {
	io.WriteString(w, ansi.EraseEntireScreen+ansi.CursorHomePosition)
}
