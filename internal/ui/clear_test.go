package ui

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestClearScreen_WritesEraseAndHomeSequences(t *testing.T) {
	var buf bytes.Buffer
	ClearScreen(&buf)

	want := ansi.EraseEntireScreen + ansi.CursorHomePosition
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
