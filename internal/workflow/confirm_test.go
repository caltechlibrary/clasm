package workflow

import (
	"bytes"
	"testing"
)

func TestConfirm_Yes(t *testing.T) {
	var buf bytes.Buffer
	ok, err := Confirm("Launch this instance?", WithConfirmIO(newHuhAccessibleInput("y\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for 'y'")
	}
}

func TestConfirm_No(t *testing.T) {
	var buf bytes.Buffer
	ok, err := Confirm("Launch this instance?", WithConfirmIO(newHuhAccessibleInput("n\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false for 'n'")
	}
}

func TestConfirm_ReprocessesInvalidInput(t *testing.T) {
	var buf bytes.Buffer
	ok, err := Confirm("Launch this instance?", WithConfirmIO(newHuhAccessibleInput("maybe\nyes\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for 'yes'")
	}
}

func TestConfirmDestructive_ExactMatch(t *testing.T) {
	var buf bytes.Buffer
	ok, err := ConfirmDestructive([]string{"i-abc123"}, WithConfirmIO(newHuhAccessibleInput("i-abc123\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true for an exact match")
	}
}

func TestConfirmDestructive_MatchesAnyAcceptedValue(t *testing.T) {
	var buf bytes.Buffer
	ok, err := ConfirmDestructive([]string{"i-abc123", "web-server"}, WithConfirmIO(newHuhAccessibleInput("web-server\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got true, want false when input matches the second accepted value")
	}
}

func TestConfirmDestructive_MismatchCancelsWithoutRetry(t *testing.T) {
	var buf bytes.Buffer
	ok, err := ConfirmDestructive([]string{"i-abc123"}, WithConfirmIO(newHuhAccessibleInput("typo\n"), &buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false for a mismatched input")
	}
}

func TestConfirmDestructive_IgnoresEmptyAcceptedValues(t *testing.T) {
	var buf bytes.Buffer
	ok, err := ConfirmDestructive([]string{"i-abc123", ""}, WithConfirmIO(newHuhAccessibleInput("\n"), &buf)) // blank input
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false -- blank input must not match an untagged (empty) accepted value")
	}
}
