package ui

import (
	"errors"
	"testing"
)

func TestPrompt_ReturnsTypedValue(t *testing.T) {
	term, le, _ := newPipeEditor(t, "us-east-1\n")

	got, err := Prompt(term, le, "Region")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-east-1" {
		t.Errorf("got %q, want %q", got, "us-east-1")
	}
}

func TestPrompt_EmptyInputUsesDefault(t *testing.T) {
	term, le, _ := newPipeEditor(t, "\n")

	got, err := Prompt(term, le, "Region", WithDefault("us-east-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-east-1" {
		t.Errorf("got %q, want default %q", got, "us-east-1")
	}
}

func TestPrompt_NonEmptyInputOverridesDefault(t *testing.T) {
	term, le, _ := newPipeEditor(t, "us-west-2\n")

	got, err := Prompt(term, le, "Region", WithDefault("us-east-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-west-2" {
		t.Errorf("got %q, want %q", got, "us-west-2")
	}
}

func TestPrompt_ValidatorRejectsThenAccepts(t *testing.T) {
	term, le, buf := newPipeEditor(t, "prod\nproduction\n")

	validate := func(s string) error {
		if s != "production" && s != "development" && s != "test" {
			return errors.New("must be one of production, development, test")
		}
		return nil
	}

	got, err := Prompt(term, le, "Environment", WithValidator(validate))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "production" {
		t.Errorf("got %q, want %q", got, "production")
	}
	if !contains(buf.String(), "invalid input") {
		t.Errorf("expected an invalid-input message in output, got:\n%s", buf.String())
	}
}
