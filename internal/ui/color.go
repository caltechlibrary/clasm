package ui

import (
	"os"

	"github.com/rsdoiel/termlib"
	"golang.org/x/term"
)

// ColorEnabled reports whether DisplayInstances should colorize its
// STATE column: respects the NO_COLOR convention (https://no-color.org)
// and falls back to plain output when stdout isn't a terminal (e.g.
// piped/redirected) -- see PLAN.md, Phase 15, "NO_COLOR/non-TTY
// fallback". Call once at startup; DisplayInstances itself takes the
// result as a plain bool so it stays environment-independent and
// testable.
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// colorEnabled backs Highlight. PickList and other deeply-nested prompts
// (unlike DisplayInstances/DisplayImages) have no direct path back to
// main's computed ColorEnabled() result -- threading a bool parameter
// through the dozens of workflow functions between main() and every
// PickList call site would be a large refactor for a purely cosmetic
// feature, so this package-level flag fills that gap instead. Set once
// at startup via SetColorEnabled.
var colorEnabled = false

// SetColorEnabled sets the flag Highlight consults. Call once at
// startup with the same value passed to DisplayInstances/DisplayImages.
func SetColorEnabled(v bool) {
	colorEnabled = v
}

// Highlight wraps s in a bold ANSI escape when color output is enabled,
// so a prompt that follows a menu selection (e.g. "Select an instance
// to start") stands out immediately -- letting an operator who typed
// the wrong menu digit notice before reading through the list that
// follows. Returns s unchanged when color is disabled.
func Highlight(s string) string {
	if !colorEnabled {
		return s
	}
	return termlib.Bold + s + termlib.Reset
}
