package tui

import "strings"

const (
	ansiReverse = "\033[7m"
	ansiBold    = "\033[1m"
	ansiReset   = "\033[0m"
)

// reverseVideo wraps s in reverse-video, re-asserting it after any reset
// s already carries (e.g. a per-cell color embedded by a caller like
// instanceListViewConfig's STATE column) so the cursor-row highlight
// doesn't get cut short by that inner reset.
func reverseVideo(s string) string {
	return ansiReverse + strings.ReplaceAll(s, ansiReset, ansiReset+ansiReverse) + ansiReset
}
func bold(s string) string { return ansiBold + s + ansiReset }

// StyleRow applies reverse-video to a cursor row and bold to a tagged
// row -- the unambiguous "this is the selected row" signal every file
// manager (mc, ranger, WinSCP) uses -- gated by colorEnabled (this
// project's NO_COLOR/non-TTY convention, internal/ui.ColorEnabled)
// rather than emitted unconditionally, even though reverse/bold are
// text decorations rather than colors.
func StyleRow(row string, isCursor, isTagged, colorEnabled bool) string {
	if !colorEnabled {
		return row
	}
	switch {
	case isCursor:
		return reverseVideo(row)
	case isTagged:
		return bold(row)
	default:
		return row
	}
}
