package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func testTagMgmtActions(refreshCalls *int) TagMgmtActions {
	noop := func(ctx context.Context) error { return nil }
	return TagMgmtActions{
		ManageTags:  noop,
		ShowAllTags: noop,
		Refresh:     countingAction(refreshCalls),
	}
}

func TestRunTagMgmtMenu_DispatchesToTheChosenAction(t *testing.T) {
	var manageCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = cancelingAction(&manageCalls, cancel)

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf) // Manage tags
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if manageCalls != 1 {
		t.Errorf("manageCalls = %d, want 1", manageCalls)
	}
}

func TestRunTagMgmtMenu_ShowAllTagsDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ShowAllTags = cancelingAction(&showCalls, cancel)

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Show all tags
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

func TestRunTagMgmtMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var manageCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = cancelingAction(&manageCalls, cancel)

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunTagMgmtMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls, manageCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = func(ctx context.Context) error {
		manageCalls++
		if manageCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("1\n1\n"), buf) // Manage tags, twice
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

func TestRunTagMgmtMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testTagMgmtActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunTagMgmtMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = failingAction(huh.ErrUserAborted)

	if err := runTagMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunTagMgmtMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = failingAction(io.EOF)

	if err := runTagMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestTagMgmtMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(tagMgmtMenuItems) != 2 {
		t.Fatalf("len(tagMgmtMenuItems) = %d, want 2 (no \"Back to domain picker\" -- 'q' is the only way back)", len(tagMgmtMenuItems))
	}
	for _, item := range tagMgmtMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q", item.label)
		}
	}
}
