package workflow

import (
	"bytes"
	"os"
	"testing"

	"github.com/rsdoiel/termlib"
)

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

func TestConfirm_Yes(t *testing.T) {
	term, le, _ := newPipeEditor(t, "y\n")
	ok, err := Confirm(term, le, "Launch this instance?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for 'y'")
	}
}

func TestConfirm_No(t *testing.T) {
	term, le, _ := newPipeEditor(t, "n\n")
	ok, err := Confirm(term, le, "Launch this instance?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false for 'n'")
	}
}

func TestConfirm_ReprocessesInvalidInput(t *testing.T) {
	term, le, buf := newPipeEditor(t, "maybe\nyes\n")
	ok, err := Confirm(term, le, "Launch this instance?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for 'yes'")
	}
	if !bytes.Contains(buf.Bytes(), []byte("enter")) {
		t.Errorf("expected a re-prompt hint in output, got:\n%s", buf.String())
	}
}

func TestConfirmDestructive_ExactMatch(t *testing.T) {
	term, le, _ := newPipeEditor(t, "i-abc123\n")
	ok, err := ConfirmDestructive(term, le, "i-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for an exact match")
	}
}

func TestConfirmDestructive_MatchesAnyAcceptedValue(t *testing.T) {
	term, le, _ := newPipeEditor(t, "web-server\n")
	ok, err := ConfirmDestructive(term, le, "i-abc123", "web-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true when input matches the second accepted value")
	}
}

func TestConfirmDestructive_MismatchCancelsWithoutRetry(t *testing.T) {
	term, le, _ := newPipeEditor(t, "typo\n")
	ok, err := ConfirmDestructive(term, le, "i-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false for a mismatched input")
	}
}

func TestConfirmDestructive_IgnoresEmptyAcceptedValues(t *testing.T) {
	term, le, _ := newPipeEditor(t, "\n") // blank input
	ok, err := ConfirmDestructive(term, le, "i-abc123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false -- blank input must not match an untagged (empty) accepted value")
	}
}
