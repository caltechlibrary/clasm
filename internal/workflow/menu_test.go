package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rsdoiel/termlib"
)

func countingAction(calls *int) func(context.Context) error {
	return func(ctx context.Context) error {
		*calls++
		return nil
	}
}

func failingAction(err error) func(context.Context) error {
	return func(ctx context.Context) error {
		return err
	}
}

func testMenuActions(refreshCalls *int) MenuActions {
	noop := func(ctx context.Context) error { return nil }
	return MenuActions{
		CreateInstanceFromAMI:       noop,
		CreateInstanceFromCloudInit: noop,
		StartEC2Instance:            noop,
		StopEC2Instance:             noop,
		TerminateEC2Instance:        noop,
		ManageTags:                  noop,
		CreateAMIFromInstance:       noop,
		RemoveAMI:                   noop,
		ShowCloudInit:               noop,
		BackupArchiveAndTrim:        noop,
		Refresh:                     countingAction(refreshCalls),
	}
}

func TestRunMainMenu_DispatchesToTheChosenAction(t *testing.T) {
	var startCalls, refreshCalls int
	term, le, _ := newPipeEditor(t, "3\n12\n") // Start EC2 instance, then Exit

	actions := testMenuActions(&refreshCalls)
	actions.StartEC2Instance = countingAction(&startCalls)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", startCalls)
	}
}

func TestRunMainMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "3\n12\n")

	actions := testMenuActions(&refreshCalls)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunMainMenu_ExitDoesNotRefresh(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "12\n")

	actions := testMenuActions(&refreshCalls)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (Exit shouldn't refresh)", refreshCalls)
	}
}

func TestRunMainMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls int
	term, le, buf := newPipeEditor(t, "1\n12\n") // action 1 fails, then Exit

	actions := testMenuActions(&refreshCalls)
	actions.CreateInstanceFromAMI = failingAction(errors.New("boom"))

	err := RunMainMenu(context.Background(), term, le, actions)
	if err != nil {
		t.Fatalf("expected the loop to survive a single action's error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (a failed action shouldn't refresh)", refreshCalls)
	}
}

func TestRunMainMenu_CleanExitOnCancelledPickList(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "0\n") // cancel the top-level menu pick
	actions := testMenuActions(&refreshCalls)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
}

func TestRunMainMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "") // no input needed -- should exit before prompting
	actions := testMenuActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RunMainMenu(ctx, term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunMainMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "1\n") // action 1 returns ErrInterrupted, as if Ctrl+C fired mid-workflow
	actions := testMenuActions(&refreshCalls)
	actions.CreateInstanceFromAMI = failingAction(termlib.ErrInterrupted)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on ErrInterrupted, got: %v", err)
	}
}

func TestRunMainMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "1\n")
	actions := testMenuActions(&refreshCalls)
	actions.CreateInstanceFromAMI = failingAction(io.EOF)

	if err := RunMainMenu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}
