package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rsdoiel/termlib"
)

// newPipeEditor returns a LineEditor whose input is a pipe pre-loaded with
// input, and a Terminal sharing the same output buffer. A pipe is not a
// TTY, so termlib.LineEditor.Prompt falls back to plain line reading --
// exactly the behavior its own doc comment recommends for piped test input.
func newPipeEditor(t *testing.T, input string) (*termlib.Terminal, *termlib.LineEditor, *bytes.Buffer) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	go func() {
		w.WriteString(input)
		w.Close()
	}()

	var buf bytes.Buffer
	term := termlib.New(&buf)
	le := termlib.NewLineEditor(r, &buf)
	return term, le, &buf
}

func TestPickList_ValidSelection(t *testing.T) {
	term, le, _ := newPipeEditor(t, "2\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "beta" {
		t.Errorf("got %q, want %q", got, "beta")
	}
}

func TestPickList_ReprocessesInvalidInput(t *testing.T) {
	term, le, buf := newPipeEditor(t, "abc\n99\n1\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "alpha" {
		t.Errorf("got %q, want %q", got, "alpha")
	}
	if !contains(buf.String(), "invalid selection") {
		t.Errorf("expected an invalid-selection message in output, got:\n%s", buf.String())
	}
}

func TestPickList_Cancel(t *testing.T) {
	term, le, _ := newPipeEditor(t, "0\n")
	items := []string{"alpha", "beta"}

	_, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != ErrCancelled {
		t.Fatalf("got error %v, want ErrCancelled", err)
	}
}

func TestPickList_NoItems(t *testing.T) {
	term, le, _ := newPipeEditor(t, "")
	_, err := PickList(term, le, []string{}, func(s string) string { return s }, "Select an item")
	if err == nil {
		t.Fatal("expected an error for an empty item list")
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

func manyItems(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = "item-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	return items
}

func TestPickList_PaginatesLargeLists(t *testing.T) {
	items := manyItems(75) // > the 50-item page size
	term, le, buf := newPipeEditor(t, "n\n60\n")

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != items[59] {
		t.Errorf("got %q, want %q", got, items[59])
	}
	if !contains(buf.String(), "Page 1/2") {
		t.Errorf("expected a page indicator in output, got:\n%s", buf.String())
	}
}

func TestPickList_PageBackNavigation(t *testing.T) {
	items := manyItems(75)
	term, le, _ := newPipeEditor(t, "n\np\n1\n") // page 2, back to page 1, pick item 1

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != items[0] {
		t.Errorf("got %q, want %q", got, items[0])
	}
}

func TestPickList_PrintsHighlightedPromptHeaderBeforeList(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	term, le, buf := newPipeEditor(t, "1\n")
	items := []string{"alpha", "beta"}

	if _, err := PickList(term, le, items, func(s string) string { return s }, "Select an instance to start"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	headerIdx := strings.Index(out, termlib.Bold+"Select an instance to start"+termlib.Reset)
	itemIdx := strings.Index(out, "1) alpha")
	if headerIdx < 0 {
		t.Errorf("expected a highlighted header before the list, got:\n%s", out)
	}
	if itemIdx < 0 || headerIdx > itemIdx {
		t.Errorf("expected the header to precede the list items, got:\n%s", out)
	}
}

func TestPickList_NoHighlightWhenColorDisabled(t *testing.T) {
	SetColorEnabled(false)

	term, le, buf := newPipeEditor(t, "1\n")
	items := []string{"alpha", "beta"}

	if _, err := PickList(term, le, items, func(s string) string { return s }, "Select an instance to start"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contains(buf.String(), termlib.Bold) {
		t.Errorf("did not expect ANSI bold codes with color disabled, got:\n%s", buf.String())
	}
}

func TestPickList_NoPageIndicatorForSmallLists(t *testing.T) {
	term, le, buf := newPipeEditor(t, "1\n")
	items := []string{"alpha", "beta"}

	if _, err := PickList(term, le, items, func(s string) string { return s }, "Select an item"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contains(buf.String(), "Page ") {
		t.Errorf("did not expect a page indicator for a list under the page size, got:\n%s", buf.String())
	}
}

func TestPickList_SelectionAcrossPagesUsesGlobalNumbering(t *testing.T) {
	items := manyItems(75)
	term, le, _ := newPipeEditor(t, "1\n") // page 1, item 1 -- no "n" needed

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != items[0] {
		t.Errorf("got %q, want %q", got, items[0])
	}
}

func TestPickList_FilterNarrowsListByLabelSubstring(t *testing.T) {
	term, le, buf := newPipeEditor(t, "ta\n1\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "ta" only matches "beta"; "1" then picks it -- not "alpha", which
	// would be item 1 of the original, unfiltered list.
	if got != "beta" {
		t.Errorf("got %q, want %q", got, "beta")
	}
	// The re-rendered list after the filter is applied should show only
	// the match, not the other two original items.
	out := buf.String()
	afterFilter := out[strings.LastIndex(out, "Filter"):]
	if contains(afterFilter, "alpha") || contains(afterFilter, "gamma") {
		t.Errorf("expected non-matching items to be hidden once filtered, got:\n%s", afterFilter)
	}
}

func TestPickList_FilterIsCaseInsensitive(t *testing.T) {
	term, le, _ := newPipeEditor(t, "GAM\n1\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "gamma" {
		t.Errorf("got %q, want %q", got, "gamma")
	}
}

func TestPickList_FilterWithNoMatchesKeepsPriorList(t *testing.T) {
	term, le, buf := newPipeEditor(t, "zzz\n1\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "alpha" {
		t.Errorf("got %q, want %q", got, "alpha")
	}
	if !contains(buf.String(), "no matches") {
		t.Errorf("expected a no-matches message, got:\n%s", buf.String())
	}
}

func TestPickList_EmptyInputClearsFilter(t *testing.T) {
	term, le, buf := newPipeEditor(t, "ta\n\n1\n")
	items := []string{"alpha", "beta", "gamma"}

	got, err := PickList(term, le, items, func(s string) string { return s }, "Select an item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After clearing the filter with a blank line, "1" picks the first
	// item of the full, unfiltered list again.
	if got != "alpha" {
		t.Errorf("got %q, want %q", got, "alpha")
	}
	if !contains(buf.String(), "alpha") {
		t.Errorf("expected the full list to be shown again after clearing the filter, got:\n%s", buf.String())
	}
}
