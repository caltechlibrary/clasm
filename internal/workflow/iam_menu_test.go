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

func testIAMActions() IAMActions {
	noop := func(ctx context.Context) error { return nil }
	return IAMActions{
		ShowRoles:            noop,
		ShowInstanceProfiles: noop,
		ShowPolicies:         noop,
	}
}

func TestRunIAMMenu_DispatchesToShowRoles(t *testing.T) {
	var calls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testIAMActions()
	actions.ShowRoles = cancelingAction(&calls, cancel)

	err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf) // Show Roles
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRunIAMMenu_DispatchesToShowInstanceProfiles(t *testing.T) {
	var calls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testIAMActions()
	actions.ShowInstanceProfiles = cancelingAction(&calls, cancel)

	err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Show Instance Profiles
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRunIAMMenu_DispatchesToShowPolicies(t *testing.T) {
	var calls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testIAMActions()
	actions.ShowPolicies = cancelingAction(&calls, cancel)

	err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput("3\n"), buf) // Show Policies
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRunIAMMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testIAMActions()
	actions.ShowRoles = func(ctx context.Context) error {
		showCalls++
		if showCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	// The blank line between the two picks is the pause-for-acknowledgment
	// prompt (DECISIONS.md, "Pause for acknowledgment before every
	// menu-loop redraw") consuming its own line of input after the error
	// is printed, before the loop reprompts.
	err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n1\n"), buf) // Show Roles, twice
	if err != nil {
		t.Fatalf("expected the loop to survive a single action's error and exit cleanly once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Press Enter to continue") {
		t.Errorf("expected a pause-for-acknowledgment prompt after the error, got:\n%s", buf.String())
	}
}

func TestRunIAMMenu_PausesForAcknowledgmentAfterASuccessfulAction(t *testing.T) {
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testIAMActions()
	actions.ShowRoles = func(ctx context.Context) error {
		fmt.Fprintln(term, "roles listed")
		cancel()
		return nil
	}

	err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n"), buf) // Show Roles
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	out := buf.String()
	statusIdx := strings.Index(out, "roles listed")
	pauseIdx := strings.Index(out, "Press Enter to continue")
	if statusIdx == -1 {
		t.Errorf("expected the successful action's own output to be shown, got:\n%s", out)
	}
	if pauseIdx == -1 || pauseIdx < statusIdx {
		t.Errorf("expected a pause-for-acknowledgment prompt after the action's own output, got:\n%s", out)
	}
}

func TestRunIAMMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	term, buf := newTermOnly()
	actions := testIAMActions()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runIAMMenu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunIAMMenu_CleanExitOnInterrupt(t *testing.T) {
	term, buf := newTermOnly()
	actions := testIAMActions()
	actions.ShowRoles = failingAction(huh.ErrUserAborted)

	if err := runIAMMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunIAMMenu_CleanExitOnEOF(t *testing.T) {
	term, buf := newTermOnly()
	actions := testIAMActions()
	actions.ShowRoles = failingAction(io.EOF)

	if err := runIAMMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestIAMMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(iamMenuItems) != 3 {
		t.Fatalf("len(iamMenuItems) = %d, want 3 (no \"Back to domain picker\" -- 'q' is the only way back)", len(iamMenuItems))
	}
	for _, item := range iamMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q", item.label)
		}
	}
}
