package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
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
		CreateInstanceFromAMI:             noop,
		CreateInstanceFromCloudInit:       noop,
		StartEC2Instance:                  noop,
		StopEC2Instance:                   noop,
		TerminateEC2Instance:              noop,
		ResizeInstanceRootVolume:          noop,
		ManageTags:                        noop,
		CreateAMIFromInstance:             noop,
		RemoveAMI:                         noop,
		ShowCloudInit:                     noop,
		BackupArchiveAndTrim:              noop,
		ShowLaunchTemplate:                noop,
		CreateLaunchTemplateFromCloudInit: noop,
		CreateInstanceFromLaunchTemplate:  noop,
		SyncLaunchTemplate:                noop,
		PromoteLaunchTemplateVersion:      noop,
		DeleteLaunchTemplateVersions:      noop,
		DeleteLaunchTemplate:              noop,
		Refresh:                           countingAction(refreshCalls),
		ShowInstances:                     noop,
		ShowAMIs:                          noop,
		ShowLaunchTemplates:               noop,
	}
}

// cancelingAction (defined in s3_menu_test.go, shared across this
// package's menu tests) increments *calls and cancels ctx, so a test
// can drive one iteration of a menu loop and then have it exit cleanly
// on the *next* iteration's ctx.Err() check -- standing in for choosing
// "Back to domain picker" (removed in this phase: 'q' is now the only
// way, and accessible mode has no way to simulate that abort -- see
// mapMenuPickerErr's doc comment).

func TestRunMainMenu_DispatchesToTheChosenAction(t *testing.T) {
	var startCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testMenuActions(&refreshCalls)
	actions.StartEC2Instance = cancelingAction(&startCalls, cancel)

	err := runMainMenu(ctx, term, actions, newHuhAccessibleInput("7\n"), buf) // Start EC2 instance
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", startCalls)
	}
}

// TestRunMainMenu_ShowInstancesDispatchesToItsOwnAction covers a real
// gap: "Show resource lists" used to dispatch to Refresh directly
// (DESIGN.md's List-tier punch list split it into separate
// ShowInstances/ShowAMIs/ShowLaunchTemplates fields, mirroring
// S3Actions' own Refresh/ShowResourceLists split, then split further
// per resource type -- DECISIONS.md, "Split Show resource lists into
// per-resource-type Compute menu entries"), but no existing test chose
// item 1 to exercise that dispatch at all. The post-action refresh
// still fires afterward (unconditional for every menu item, unchanged)
// -- this test checks both calls happen.
func TestRunMainMenu_ShowInstancesDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, showCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testMenuActions(&refreshCalls)
	actions.ShowInstances = cancelingAction(&showCalls, cancel)

	err := runMainMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
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

func TestRunMainMenu_ResizeInstanceRootVolumeDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, resizeCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testMenuActions(&refreshCalls)
	actions.ResizeInstanceRootVolume = cancelingAction(&resizeCalls, cancel)

	err := runMainMenu(ctx, term, actions, newHuhAccessibleInput("10\n"), buf) // Resize instance's root volume
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if resizeCalls != 1 {
		t.Errorf("resizeCalls = %d, want 1", resizeCalls)
	}
}

func TestRunMainMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var startCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testMenuActions(&refreshCalls)
	actions.StartEC2Instance = cancelingAction(&startCalls, cancel)

	err := runMainMenu(ctx, term, actions, newHuhAccessibleInput("7\n"), buf) // Start EC2 instance
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunMainMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls, createCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testMenuActions(&refreshCalls)
	// Fails the first time (loop must survive and reprompt), succeeds
	// (and cancels ctx to end the test) the second time.
	actions.CreateInstanceFromAMI = func(ctx context.Context) error {
		createCalls++
		if createCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	err := runMainMenu(ctx, term, actions, newHuhAccessibleInput("4\n4\n"), buf) // Create EC2 instance from AMI, twice
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

func TestRunMainMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testMenuActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runMainMenu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunMainMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testMenuActions(&refreshCalls)
	actions.CreateInstanceFromAMI = failingAction(huh.ErrUserAborted)

	if err := runMainMenu(context.Background(), term, actions, newHuhAccessibleInput("4\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunMainMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testMenuActions(&refreshCalls)
	actions.CreateInstanceFromAMI = failingAction(io.EOF)

	if err := runMainMenu(context.Background(), term, actions, newHuhAccessibleInput("4\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestMainMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(mainMenuItems) != 21 {
		t.Fatalf("len(mainMenuItems) = %d, want 21 (no more \"Back to domain picker\" -- 'q' is the only way back now)", len(mainMenuItems))
	}
	for _, item := range mainMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q -- \"Back to domain picker\" should have been removed", item.label)
		}
	}
}
