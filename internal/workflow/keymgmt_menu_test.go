package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rsdoiel/termlib"
)

func testKeyMgmtActions(refreshCalls *int) KeyMgmtActions {
	noop := func(ctx context.Context) error { return nil }
	return KeyMgmtActions{
		CreateKeyPair: noop,
		ImportKeyPair: noop,
		DeleteKeyPair: noop,
		Refresh:       countingAction(refreshCalls),
	}
}

func TestRunKeyMgmtMenu_DispatchesToTheChosenAction(t *testing.T) {
	var createCalls, refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n5\n") // Create Key Pair, then Back to domain picker

	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = countingAction(&createCalls)

	err := RunKeyMgmtMenu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", createCalls)
	}
}

func TestRunKeyMgmtMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n5\n")

	actions := testKeyMgmtActions(&refreshCalls)

	err := RunKeyMgmtMenu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunKeyMgmtMenu_BackToDomainPickerDoesNotRefresh(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "5\n")

	actions := testKeyMgmtActions(&refreshCalls)

	err := RunKeyMgmtMenu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (backing out shouldn't refresh)", refreshCalls)
	}
}

func TestRunKeyMgmtMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls int
	term, le, buf := newPipeEditor(t, "2\n5\n") // Create Key Pair fails, then back to domain picker

	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = failingAction(errors.New("boom"))

	err := RunKeyMgmtMenu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected the loop to survive a single action's error and report ErrBackToDomainPicker, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (a failed action shouldn't refresh)", refreshCalls)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnCancelledPickList(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "0\n") // cancel the menu pick
	actions := testKeyMgmtActions(&refreshCalls)

	if err := RunKeyMgmtMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "") // no input needed -- should exit before prompting
	actions := testKeyMgmtActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RunKeyMgmtMenu(ctx, term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n")
	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = failingAction(termlib.ErrInterrupted)

	if err := RunKeyMgmtMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on ErrInterrupted, got: %v", err)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n")
	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = failingAction(io.EOF)

	if err := RunKeyMgmtMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}
