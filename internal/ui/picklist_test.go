package ui

import (
	"bytes"
	"os"
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
