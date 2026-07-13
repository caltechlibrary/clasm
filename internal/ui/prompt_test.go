package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPrompt_ReturnsTypedValue(t *testing.T) {
	var buf bytes.Buffer
	got, err := Prompt("Region", WithIO(newHuhAccessibleInput("us-east-1\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-east-1" {
		t.Errorf("got %q, want %q", got, "us-east-1")
	}
}

func TestPrompt_EmptyInputUsesDefault(t *testing.T) {
	var buf bytes.Buffer
	got, err := Prompt("Region", WithDefault("us-east-1"), WithIO(newHuhAccessibleInput("\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-east-1" {
		t.Errorf("got %q, want default %q", got, "us-east-1")
	}
}

func TestPrompt_NonEmptyInputOverridesDefault(t *testing.T) {
	var buf bytes.Buffer
	got, err := Prompt("Region", WithDefault("us-east-1"), WithIO(newHuhAccessibleInput("us-west-2\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-west-2" {
		t.Errorf("got %q, want %q", got, "us-west-2")
	}
}

func TestPrompt_ValidatorRejectsThenAccepts(t *testing.T) {
	validate := func(s string) error {
		if s != "production" && s != "development" && s != "test" {
			return errors.New("must be one of production, development, test")
		}
		return nil
	}

	var buf bytes.Buffer
	got, err := Prompt("Environment", WithValidator(validate), WithIO(newHuhAccessibleInput("prod\nproduction\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "production" {
		t.Errorf("got %q, want %q", got, "production")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected an invalid-input message in output, got:\n%s", buf.String())
	}
}

// newHuhAccessibleInput returns an io.Reader that yields at most one
// newline-terminated line per Read call -- huh's accessible-mode fields
// build a bufio.Scanner per call and discard whatever it buffered past
// the first newline once that call returns, so a reader that eagerly
// returns everything in one Read (e.g. strings.NewReader) starves any
// field after the first. Mirrors internal/workflow's helper of the same
// name (DECISIONS.md, "huh fields are pipe-testable via
// WithAccessible(true).WithInput/WithOutput").
func newHuhAccessibleInput(s string) io.Reader {
	return &lineAtATimeReader{remaining: []byte(s)}
}

type lineAtATimeReader struct {
	remaining []byte
}

func (r *lineAtATimeReader) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, io.EOF
	}
	idx := bytes.IndexByte(r.remaining, '\n')
	var line []byte
	if idx == -1 {
		line = r.remaining
		r.remaining = nil
	} else {
		line = r.remaining[:idx+1]
		r.remaining = r.remaining[idx+1:]
	}
	return copy(p, line), nil
}
