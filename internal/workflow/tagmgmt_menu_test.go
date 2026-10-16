package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func testTagMgmtActions(refreshCalls *int) TagMgmtActions {
	noop := func(ctx context.Context) error { return nil }
	return TagMgmtActions{
		ManageTags:   noop,
		ShowAllTags:  noop,
		ShowUntagged: noop,
		Refresh:      countingAction(refreshCalls),
	}
}

func TestRunTagMgmtMenu_DispatchesToTheChosenAction(t *testing.T) {
	var manageCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = cancelingAction(&manageCalls, cancel)

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Manage tags
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

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf) // Show all tags
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

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf)
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

	// The blank line between the two picks is the pause-for-acknowledgment
	// prompt (DECISIONS.md, "Pause for acknowledgment before every
	// menu-loop redraw") consuming its own line of input after the error
	// is printed, before the loop reprompts.
	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n\n2\n"), buf) // Manage tags, twice
	if err != nil {
		t.Fatalf("expected the loop to survive a single action's error and exit cleanly once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Press Enter to continue") {
		t.Errorf("expected a pause-for-acknowledgment prompt after the error, got:\n%s", buf.String())
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (only after the second, successful attempt)", refreshCalls)
	}
}

func TestRunTagMgmtMenu_PausesForAcknowledgmentAfterARefreshError(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = cancelingAction(new(int), cancel)
	actions.Refresh = func(ctx context.Context) error {
		refreshCalls++
		return errors.New("refresh boom")
	}

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n\n"), buf) // Manage tags
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "refresh boom") {
		t.Errorf("expected the refresh error to be shown, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Press Enter to continue") {
		t.Errorf("expected a pause-for-acknowledgment prompt after the refresh error, got:\n%s", buf.String())
	}
}

// TestRunTagMgmtMenu_PausesForAcknowledgmentAfterASuccessfulAction
// mirrors TestRunMainMenu_PausesForAcknowledgmentAfterASuccessfulAction
// -- DECISIONS.md, "Widen 'pause for acknowledgment' to every action,
// not just errors."
func TestRunTagMgmtMenu_PausesForAcknowledgmentAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = func(ctx context.Context) error {
		fmt.Fprintln(term, "tags updated")
		cancel()
		return nil
	}

	err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("2\n\n"), buf) // Manage tags
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	out := buf.String()
	statusIdx := strings.Index(out, "tags updated")
	pauseIdx := strings.Index(out, "Press Enter to continue")
	if statusIdx == -1 {
		t.Errorf("expected the successful action's own output to be shown, got:\n%s", out)
	}
	if pauseIdx == -1 || pauseIdx < statusIdx {
		t.Errorf("expected a pause-for-acknowledgment prompt after the action's own output, got:\n%s", out)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (the pause happens before Refresh, which still runs)", refreshCalls)
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

	if err := runTagMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunTagMgmtMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testTagMgmtActions(&refreshCalls)
	actions.ManageTags = failingAction(io.EOF)

	if err := runTagMgmtMenu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestTagMgmtMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(tagMgmtMenuItems) != 3 {
		t.Fatalf("len(tagMgmtMenuItems) = %d, want 3 (no \"Back to domain picker\" -- 'q' is the only way back)", len(tagMgmtMenuItems))
	}
	for _, item := range tagMgmtMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q", item.label)
		}
	}
}

// Show untagged resources (DR-0192) is appended, so "Show all tags" and "Manage tags" keep
// their numbers.
func TestRunTagMgmtMenu_DispatchesToShowUntagged(t *testing.T) {
	var calls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())
	actions := testTagMgmtActions(&refreshCalls)
	actions.ShowUntagged = cancelingAction(&calls, cancel)
	if err := runTagMgmtMenu(ctx, term, actions, newHuhAccessibleInput("3\n"), buf); err != nil {
		t.Fatalf("expected a clean exit once ctx is cancelled, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if tagMgmtMenuItems[2].cliSlug != "show-untagged" || tagMgmtMenuItems[0].label != "Show all tags" || tagMgmtMenuItems[1].label != "Manage tags" {
		t.Errorf("the first two items must not move and the new one carries the slug: %+v", tagMgmtMenuItems)
	}
}

func TestUntaggedRowsForDisplay_OneRowPerResourceWithItsReason(t *testing.T) {
	rows := untaggedRowsForDisplay(UntaggedReport{
		Missing:    []UntaggedItem{{Kind: "instance", ID: "i-1", Name: "new-data", Region: "us-west-2", Created: "2026-05-21", Tags: map[string]string{"Name": "new-data"}}},
		OtherValue: []UntaggedItem{{Kind: "ebs-volume", ID: "vol-1", OriginValue: "imss"}},
	})
	if len(rows) != 2 || rows[0].ID != "i-1" || rows[1].ID != "vol-1" {
		t.Fatalf("rows = %+v", rows)
	}
	for _, want := range []string{"instance", "new-data", "us-west-2", "2026-05-21", "missing"} {
		if !strings.Contains(rows[0].Label, want) {
			t.Errorf("label %q should mention %q", rows[0].Label, want)
		}
	}
	if !strings.Contains(rows[1].Label, "other: imss") || rows[0].Tags["Name"] != "new-data" {
		t.Errorf("rows = %+v", rows)
	}
}
