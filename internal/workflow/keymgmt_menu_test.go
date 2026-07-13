package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func testKeyMgmtActions(refreshCalls *int) KeyMgmtActions {
	noop := func(ctx context.Context) error { return nil }
	return KeyMgmtActions{
		CreateKeyPair:     noop,
		ImportKeyPair:     noop,
		DeleteKeyPair:     noop,
		Refresh:           countingAction(refreshCalls),
		ShowResourceLists: noop,
	}
}

func TestRunKeyMgmtMenu_DispatchesToTheChosenAction(t *testing.T) {
	var createCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = cancelingAction(&createCalls, cancel)

	err := runKeyMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Create Key Pair
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", createCalls)
	}
}

// TestRunKeyMgmtMenu_ShowResourceListsDispatchesToItsOwnAction covers a
// real gap: "Show resource lists" used to dispatch to Refresh directly
// (DESIGN.md's List-tier punch list split it into a separate
// ShowResourceLists field, mirroring S3Actions'/MenuActions' own
// splits), but no existing test chose item 1 to exercise that dispatch
// at all. The post-action refresh still fires afterward (unconditional
// for every menu item, unchanged) -- this test checks both calls happen.
func TestRunKeyMgmtMenu_ShowResourceListsDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testKeyMgmtActions(&refreshCalls)
	actions.ShowResourceLists = cancelingAction(&showCalls, cancel)

	err := runKeyMgmtMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if showCalls != 1 {
		t.Errorf("showCalls = %d, want 1", showCalls)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (the unconditional post-action refresh still runs)", refreshCalls)
	}
}

func TestRunKeyMgmtMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var createCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = cancelingAction(&createCalls, cancel)

	err := runKeyMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunKeyMgmtMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls, createCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = func(ctx context.Context) error {
		createCalls++
		if createCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	err := runKeyMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n2\n"), buf) // Create Key Pair, twice
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

func TestRunKeyMgmtMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testKeyMgmtActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runKeyMgmtMenu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = failingAction(huh.ErrUserAborted)

	if err := runKeyMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunKeyMgmtMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testKeyMgmtActions(&refreshCalls)
	actions.CreateKeyPair = failingAction(io.EOF)

	if err := runKeyMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestKeyMgmtMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(keyMgmtMenuItems) != 4 {
		t.Fatalf("len(keyMgmtMenuItems) = %d, want 4 (no more \"Back to domain picker\" -- 'q' is the only way back now)", len(keyMgmtMenuItems))
	}
	for _, item := range keyMgmtMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q -- \"Back to domain picker\" should have been removed", item.label)
		}
	}
}
