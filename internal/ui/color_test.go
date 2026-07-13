package ui

import (
	"testing"
)

func TestHighlight_WrapsWhenColorEnabled(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	got := Highlight("Select an instance to start")
	want := ansiBold + "Select an instance to start" + ansiReset
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHighlight_ReturnsPlainWhenColorDisabled(t *testing.T) {
	SetColorEnabled(false)

	got := Highlight("Select an instance to start")
	if got != "Select an instance to start" {
		t.Errorf("got %q, want unchanged input", got)
	}
}
