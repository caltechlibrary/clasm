package tui

import (
	"strings"
	"testing"
)

func TestStyleRow_GatedByColorEnabled(t *testing.T) {
	row := ">* logs/"

	if got := StyleRow(row, true, false, false); got != row {
		t.Errorf("StyleRow with colorEnabled=false = %q, want unchanged %q", got, row)
	}
	if got := StyleRow(row, false, true, false); got != row {
		t.Errorf("StyleRow with colorEnabled=false = %q, want unchanged %q", got, row)
	}

	if got := StyleRow(row, true, false, true); got == row || !strings.Contains(got, ansiReverse) {
		t.Errorf("StyleRow(cursor, colorEnabled=true) = %q, want reverse-video applied", got)
	}
	if got := StyleRow(row, false, true, true); got == row || !strings.Contains(got, ansiBold) {
		t.Errorf("StyleRow(tagged, colorEnabled=true) = %q, want bold applied", got)
	}
}

func TestStyleRow_NeitherCursorNorTaggedIsUnchanged(t *testing.T) {
	row := "  logs/"
	if got := StyleRow(row, false, false, true); got != row {
		t.Errorf("StyleRow(neither, colorEnabled=true) = %q, want unchanged %q", got, row)
	}
}

// A row that already embeds its own ANSI color+reset (e.g.
// internal/ui.instanceRow's colorized STATE column) must not have its
// cursor-row reverse-video cut short by that inner reset -- reverseVideo
// re-asserts itself after every embedded reset it finds.
func TestStyleRow_CursorRowReassertsReverseVideoAfterEmbeddedReset(t *testing.T) {
	row := "i-1  \033[32mrunning\033[0m  web"
	got := StyleRow(row, true, false, true)

	if !strings.HasPrefix(got, ansiReverse) {
		t.Fatalf("got %q, want it to start with reverse-video", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("got %q, want it to end with a reset", got)
	}
	afterEmbeddedReset := strings.SplitN(got, "running"+ansiReset, 2)
	if len(afterEmbeddedReset) != 2 {
		t.Fatalf("got %q, want the embedded %q text followed by its own reset", got, "running")
	}
	if !strings.HasPrefix(afterEmbeddedReset[1], ansiReverse) {
		t.Errorf("got %q, want reverse-video reasserted immediately after the embedded reset so the rest of the row (%q) stays highlighted", got, afterEmbeddedReset[1])
	}
}
