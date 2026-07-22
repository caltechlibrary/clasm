package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func testS3Actions(refreshCalls *int) S3Actions {
	noop := func(ctx context.Context) error { return nil }
	return S3Actions{
		CreateBucket:            noop,
		ConfigureWebsite:        noop,
		BrowseAndManageObjects:  noop,
		ManageLifecyclePolicies: noop,
		DeleteBucket:            noop,
		Refresh:                 countingAction(refreshCalls),
		ShowResourceLists:       noop,
	}
}

// newTermOnly returns a bytes.Buffer usable both as the io.Writer every
// menu loop prints error/refresh output to, and as the buffer tests
// inspect that output through -- both are the same value.
func newTermOnly() (io.Writer, *bytes.Buffer) {
	var buf bytes.Buffer
	return &buf, &buf
}

// cancelingAction increments *calls and cancels ctx via cancel, so a
// test can drive one iteration of runS3Menu's loop and then have it
// exit cleanly on the *next* iteration's ctx.Err() check -- standing in
// for choosing "Back to domain picker" (removed in Phase 20.7: 'q' is
// now the only way back, and accessible mode has no way to simulate
// that abort -- see mapMenuPickerErr's doc comment). The resulting
// exit is nil, not ErrBackToDomainPicker: this is a test-only device to
// observe one dispatch's effects, not a claim about what a real abort
// returns.
func cancelingAction(calls *int, cancel context.CancelFunc) func(context.Context) error {
	return func(ctx context.Context) error {
		*calls++
		cancel()
		return nil
	}
}

func TestRunS3Menu_DispatchesToTheChosenAction(t *testing.T) {
	var createCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = cancelingAction(&createCalls, cancel)

	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Create Bucket
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", createCalls)
	}
}

func TestRunS3Menu_DispatchesEachActionByPosition(t *testing.T) {
	cases := []struct {
		menuInput   string
		actionField string
		assign      func(actions *S3Actions, calls *int, cancel context.CancelFunc)
	}{
		{"3\n", "ConfigureWebsite", func(a *S3Actions, calls *int, cancel context.CancelFunc) {
			a.ConfigureWebsite = cancelingAction(calls, cancel)
		}},
		{"4\n", "BrowseAndManageObjects", func(a *S3Actions, calls *int, cancel context.CancelFunc) {
			a.BrowseAndManageObjects = cancelingAction(calls, cancel)
		}},
		{"5\n", "ManageLifecyclePolicies", func(a *S3Actions, calls *int, cancel context.CancelFunc) {
			a.ManageLifecyclePolicies = cancelingAction(calls, cancel)
		}},
		{"6\n", "DeleteBucket", func(a *S3Actions, calls *int, cancel context.CancelFunc) {
			a.DeleteBucket = cancelingAction(calls, cancel)
		}},
	}

	for _, c := range cases {
		var refreshCalls, calls int
		term, buf := newTermOnly()
		ctx, cancel := context.WithCancel(context.Background())
		actions := testS3Actions(&refreshCalls)
		c.assign(&actions, &calls, cancel)

		if err := runS3Menu(ctx, term, actions, newHuhAccessibleInput(c.menuInput), buf); err != nil {
			t.Fatalf("%s: expected a clean exit (nil error) once ctx is cancelled, got: %v", c.actionField, err)
		}
		if calls != 1 {
			t.Errorf("%s: calls = %d, want 1", c.actionField, calls)
		}
	}
}

// TestRunS3Menu_ShowResourceListsDispatchesToItsOwnAction covers a real
// gap: "Show resource lists" used to dispatch to Refresh directly
// (DESIGN.md, "S3 Resource List Display -- Paged, Accessible-
// Compatible" changed this to a separate ShowResourceLists field), but
// no existing test chose item 1 to exercise that dispatch at all. The
// post-action refresh still fires afterward (unconditional for every
// menu item, unchanged) -- this test checks both calls happen.
func TestRunS3Menu_ShowResourceListsDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	actions.ShowResourceLists = cancelingAction(&showCalls, cancel)

	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
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

func TestRunS3Menu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var refreshCalls, createCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = cancelingAction(&createCalls, cancel)

	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunS3Menu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls, createCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	// Fails the first time (loop must survive and reprompt), succeeds
	// (and cancels ctx to end the test) the second time.
	actions.CreateBucket = func(ctx context.Context) error {
		createCalls++
		if createCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	// The blank line between the two picks is the pause-for-acknowledgment
	// prompt (DECISIONS.md, "Pause for acknowledgment before every
	// menu-loop redraw") consuming its own line of input after the error
	// is printed, before the loop reprompts.
	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("2\n\n2\n"), buf)
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

func TestRunS3Menu_PausesForAcknowledgmentAfterARefreshError(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = cancelingAction(new(int), cancel)
	actions.Refresh = func(ctx context.Context) error {
		refreshCalls++
		return errors.New("refresh boom")
	}

	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("2\n\n"), buf) // Create Bucket
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

// TestRunS3Menu_PausesForAcknowledgmentAfterASuccessfulAction mirrors
// TestRunMainMenu_PausesForAcknowledgmentAfterASuccessfulAction --
// DECISIONS.md, "Widen 'pause for acknowledgment' to every action,
// not just errors."
func TestRunS3Menu_PausesForAcknowledgmentAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = func(ctx context.Context) error {
		fmt.Fprintln(term, "bucket created")
		cancel()
		return nil
	}

	err := runS3Menu(ctx, term, actions, newHuhAccessibleInput("2\n\n"), buf) // Create Bucket
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	out := buf.String()
	statusIdx := strings.Index(out, "bucket created")
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

func TestRunS3Menu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testS3Actions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runS3Menu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunS3Menu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = failingAction(huh.ErrUserAborted)

	if err := runS3Menu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunS3Menu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = failingAction(io.EOF)

	if err := runS3Menu(context.Background(), term, actions, newHuhAccessibleInput("2\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

// TestMapS3MenuPickerErr covers the abort-to-ErrBackToDomainPicker
// mapping as a standalone pure function, since accessible mode (the only
// path the tests above can drive) has no way to produce
// huh.ErrUserAborted itself -- see mapMenuPickerErr's doc comment.
func TestMapS3MenuPickerErr(t *testing.T) {
	if err := mapMenuPickerErr(huh.ErrUserAborted); !errors.Is(err, ErrBackToDomainPicker) {
		t.Errorf("aborting the picker should map to ErrBackToDomainPicker, got: %v", err)
	}

	boom := errors.New("boom")
	if err := mapMenuPickerErr(boom); !errors.Is(err, boom) {
		t.Errorf("a real error should pass through unchanged, got: %v", err)
	}

	if err := mapMenuPickerErr(nil); err != nil {
		t.Errorf("nil should pass through as nil, got: %v", err)
	}
}

func TestS3MenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(s3MenuItems) != 6 {
		t.Fatalf("len(s3MenuItems) = %d, want 6 (no more \"Back to domain picker\" -- 'q' is the only way back now)", len(s3MenuItems))
	}
	for _, item := range s3MenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q -- \"Back to domain picker\" should have been removed", item.label)
		}
	}
}

func TestS3MenuItems_FirstItemIsListS3Buckets(t *testing.T) {
	if got := s3MenuItems[0].label; got != "List S3 Buckets" {
		t.Errorf("s3MenuItems[0].label = %q, want %q", got, "List S3 Buckets")
	}
}
