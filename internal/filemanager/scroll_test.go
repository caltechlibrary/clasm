package filemanager

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/tui"
)

// TestModel_LargeListing_ScrollsToReachEntriesBelowTheFold is a
// regression test for the reported bug: with no cap on rows rendered,
// a bucket with more objects than fit on screen pushed the status
// line/command line/hotkey legend off the bottom of the terminal, with
// no way to scroll down and reach (e.g.) an object alphabetically past
// what fit in the first screenful.
func TestModel_LargeListing_ScrollsToReachEntriesBelowTheFold(t *testing.T) {
	var keys []string
	for i := range 40 {
		keys = append(keys, fmt.Sprintf("file-%02d.txt", i))
	}
	keys = append(keys, "opensearch.xml") // sorts after file-*, alphabetically last
	fake := newFakeS3(keys...)

	m := New(context.Background(), fake, "bucket", "us-west-2", "")
	m.width, m.height = 100, 15 // short terminal -- forces a small item window
	drainCmd(m, m.Init())

	itemWindow := m.paneItemWindowHeight()
	if itemWindow >= len(keys) {
		t.Fatalf("test setup invalid: itemWindow=%d must be smaller than %d entries to exercise scrolling", itemWindow, len(keys))
	}

	out := m.View()
	if strings.Contains(out, "opensearch.xml") {
		t.Fatalf("opensearch.xml should not be visible before scrolling down (window height %d, item is last of %d):\n%s", itemWindow, len(keys), out)
	}
	if !strings.Contains(out, "of 41") {
		t.Errorf("expected a scroll indicator mentioning the total item count, got:\n%s", out)
	}

	// Move the cursor all the way to the last entry.
	for range len(keys) {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		drainCmd(m, cmd)
	}

	out = m.View()
	if !strings.Contains(out, "opensearch.xml") {
		t.Fatalf("opensearch.xml should be visible after scrolling to the end:\n%s", out)
	}

	// Every rendered box row must still line up (paging must not break
	// the box's alignment invariant established in box_test.go).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := tui.RuneLen(lines[0])
	for i, l := range lines {
		if got := tui.RuneLen(l); got != want {
			t.Errorf("line %d width = %d, want %d:\n%q", i, got, want, l)
		}
	}
}

func TestModel_LargeListing_BoxHeightStaysBoundedRegardlessOfItemCount(t *testing.T) {
	var keys []string
	for i := range 500 {
		keys = append(keys, fmt.Sprintf("file-%03d.txt", i))
	}
	fake := newFakeS3(keys...)

	m := New(context.Background(), fake, "bucket", "us-west-2", "")
	m.width, m.height = 100, 20
	drainCmd(m, m.Init())

	out := m.View()
	lineCount := strings.Count(out, "\n")
	// Must stay close to the terminal's actual height regardless of a
	// 500-object listing -- this is exactly the bug: before capping
	// rows to the window height, the box grew to match the listing
	// size instead of the terminal size.
	if lineCount > m.height+2 {
		t.Fatalf("rendered %d lines for a %d-line terminal with 500 items -- box height must be bounded by the terminal, not the listing size", lineCount, m.height)
	}
}
