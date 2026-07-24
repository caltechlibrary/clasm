package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testConfigureActions(dirty *bool, refreshCalls *int) ConfigureActions {
	noop := func(ctx context.Context) error { return nil }
	return ConfigureActions{
		ShowCurrentConfig:        noop,
		EditRegions:              noop,
		EditBackupDirectoryRules: noop,
		EditOriginTag:            noop,
		Save:                     noop,
		Refresh:                  countingAction(refreshCalls),
		Dirty:                    func() bool { return *dirty },
	}
}

func TestRunConfigureMenu_DispatchesToTheChosenAction(t *testing.T) {
	var dirty bool
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testConfigureActions(&dirty, &refreshCalls)
	actions.ShowCurrentConfig = cancelingAction(&showCalls, cancel)

	err := runConfigureMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf) // Show current config
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if showCalls != 1 {
		t.Errorf("showCalls = %d, want 1", showCalls)
	}
}

func TestRunConfigureMenu_SaveDispatchesToItsOwnAction(t *testing.T) {
	var dirty bool
	var refreshCalls, saveCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testConfigureActions(&dirty, &refreshCalls)
	actions.Save = cancelingAction(&saveCalls, cancel)

	err := runConfigureMenu(ctx, term, actions, newHuhAccessibleInput("5\n"), buf) // Save
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if saveCalls != 1 {
		t.Errorf("saveCalls = %d, want 1", saveCalls)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (the unconditional post-action refresh still runs)", refreshCalls)
	}
}

func TestRunConfigureMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var dirty bool
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testConfigureActions(&dirty, &refreshCalls)
	actions.ShowCurrentConfig = cancelingAction(&showCalls, cancel)

	err := runConfigureMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunConfigureMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var dirty bool
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testConfigureActions(&dirty, &refreshCalls)
	actions.ShowCurrentConfig = func(ctx context.Context) error {
		showCalls++
		if showCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	// The blank line between the two picks is the pause-for-acknowledgment
	// prompt consuming its own line of input after the error is printed.
	err := runConfigureMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n1\n"), buf) // Show current config, twice
	if err != nil {
		t.Fatalf("expected the loop to survive a single action's error and exit cleanly once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (only after the second, successful attempt)", refreshCalls)
	}
}

func TestRunConfigureMenu_PausesForAcknowledgmentAfterASuccessfulAction(t *testing.T) {
	var dirty bool
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testConfigureActions(&dirty, &refreshCalls)
	actions.ShowCurrentConfig = func(ctx context.Context) error {
		cancel()
		return nil
	}

	err := runConfigureMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "Press Enter to continue") {
		t.Errorf("expected a pause-for-acknowledgment prompt, got:\n%s", buf.String())
	}
}
