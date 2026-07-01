package ui

import (
	"os"

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
