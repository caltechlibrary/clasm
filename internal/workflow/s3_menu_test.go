package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rsdoiel/termlib"
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
	}
}

func TestRunS3Menu_DispatchesToTheChosenAction(t *testing.T) {
	var createCalls, refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n7\n") // Create Bucket, then Back to domain picker

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = countingAction(&createCalls)

	err := RunS3Menu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", createCalls)
	}
}

func TestRunS3Menu_DispatchesEachActionByPosition(t *testing.T) {
	var configureCalls, browseCalls, lifecycleCalls, deleteBucketCalls int

	actionsFor := func() S3Actions {
		var refreshCalls int
		a := testS3Actions(&refreshCalls)
		a.ConfigureWebsite = countingAction(&configureCalls)
		a.BrowseAndManageObjects = countingAction(&browseCalls)
		a.ManageLifecyclePolicies = countingAction(&lifecycleCalls)
		a.DeleteBucket = countingAction(&deleteBucketCalls)
		return a
	}

	term, le, _ := newPipeEditor(t, "3\n7\n") // Configure Static Website Hosting
	if err := RunS3Menu(context.Background(), term, le, actionsFor()); !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if configureCalls != 1 {
		t.Errorf("configureCalls = %d, want 1", configureCalls)
	}

	term, le, _ = newPipeEditor(t, "4\n7\n") // Browse & Manage Objects
	if err := RunS3Menu(context.Background(), term, le, actionsFor()); !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if browseCalls != 1 {
		t.Errorf("browseCalls = %d, want 1", browseCalls)
	}

	term, le, _ = newPipeEditor(t, "5\n7\n") // Manage Bucket Lifecycle Policies
	if err := RunS3Menu(context.Background(), term, le, actionsFor()); !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if lifecycleCalls != 1 {
		t.Errorf("lifecycleCalls = %d, want 1", lifecycleCalls)
	}

	term, le, _ = newPipeEditor(t, "6\n7\n") // Delete Bucket
	if err := RunS3Menu(context.Background(), term, le, actionsFor()); !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if deleteBucketCalls != 1 {
		t.Errorf("deleteBucketCalls = %d, want 1", deleteBucketCalls)
	}
}

func TestRunS3Menu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n7\n")

	actions := testS3Actions(&refreshCalls)

	err := RunS3Menu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunS3Menu_BackToDomainPickerDoesNotRefresh(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "7\n")

	actions := testS3Actions(&refreshCalls)

	err := RunS3Menu(context.Background(), term, le, actions)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (backing out shouldn't refresh)", refreshCalls)
	}
}

func TestRunS3Menu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls int
	term, le, buf := newPipeEditor(t, "2\n7\n")

	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = failingAction(errors.New("boom"))

	err := RunS3Menu(context.Background(), term, le, actions)
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

func TestRunS3Menu_CleanExitOnCancelledPickList(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "0\n")
	actions := testS3Actions(&refreshCalls)

	if err := RunS3Menu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
}

func TestRunS3Menu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "")
	actions := testS3Actions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RunS3Menu(ctx, term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunS3Menu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n")
	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = failingAction(termlib.ErrInterrupted)

	if err := RunS3Menu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on ErrInterrupted, got: %v", err)
	}
}

func TestRunS3Menu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, le, _ := newPipeEditor(t, "2\n")
	actions := testS3Actions(&refreshCalls)
	actions.CreateBucket = failingAction(io.EOF)

	if err := RunS3Menu(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}
