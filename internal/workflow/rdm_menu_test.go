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

func testRDMBackupRestoreActions(refreshCalls *int) RDMBackupRestoreActions {
	noop := func(ctx context.Context) error { return nil }
	return RDMBackupRestoreActions{
		RunSQLBackup:      noop,
		ArchiveSQL:        noop,
		ArchiveOpenSearch: noop,
		RestoreSQL:        noop,
		RestoreOpenSearch: noop,
		Refresh:           countingAction(refreshCalls),
	}
}

func TestRunRDMBackupRestoreMenu_DispatchesToTheChosenAction(t *testing.T) {
	var runSQLBackupCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = cancelingAction(&runSQLBackupCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf) // Generate SQL Backup
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if runSQLBackupCalls != 1 {
		t.Errorf("runSQLBackupCalls = %d, want 1", runSQLBackupCalls)
	}
}

func TestRunRDMBackupRestoreMenu_ArchiveSQLDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, archiveSQLCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.ArchiveSQL = cancelingAction(&archiveSQLCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("2\n"), buf) // Archive SQL Backup to S3
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if archiveSQLCalls != 1 {
		t.Errorf("archiveSQLCalls = %d, want 1", archiveSQLCalls)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (the unconditional post-action refresh still runs)", refreshCalls)
	}
}

func TestRunRDMBackupRestoreMenu_ArchiveOpenSearchDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, archiveOSCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.ArchiveOpenSearch = cancelingAction(&archiveOSCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("3\n"), buf) // Archive OpenSearch Snapshot to S3
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if archiveOSCalls != 1 {
		t.Errorf("archiveOSCalls = %d, want 1", archiveOSCalls)
	}
}

func TestRunRDMBackupRestoreMenu_RestoreSQLDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, restoreSQLCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RestoreSQL = cancelingAction(&restoreSQLCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("4\n"), buf) // Restore SQL Backup from S3
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if restoreSQLCalls != 1 {
		t.Errorf("restoreSQLCalls = %d, want 1", restoreSQLCalls)
	}
}

func TestRunRDMBackupRestoreMenu_RestoreOpenSearchDispatchesToItsOwnAction(t *testing.T) {
	var refreshCalls, restoreOSCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RestoreOpenSearch = cancelingAction(&restoreOSCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("5\n"), buf) // Restore OpenSearch Snapshot from S3
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if restoreOSCalls != 1 {
		t.Errorf("restoreOSCalls = %d, want 1", restoreOSCalls)
	}
}

func TestRunRDMBackupRestoreMenu_RefreshesAfterASuccessfulAction(t *testing.T) {
	var runSQLBackupCalls, refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = cancelingAction(&runSQLBackupCalls, cancel)

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1 (once, after the dispatched action)", refreshCalls)
	}
}

func TestRunRDMBackupRestoreMenu_ActionErrorDoesNotCrashLoop(t *testing.T) {
	var refreshCalls, runSQLBackupCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = func(ctx context.Context) error {
		runSQLBackupCalls++
		if runSQLBackupCalls == 1 {
			return errors.New("boom")
		}
		cancel()
		return nil
	}

	// The blank line between the two picks is the pause-for-acknowledgment
	// prompt (DECISIONS.md, "Pause for acknowledgment before every
	// menu-loop redraw") consuming its own line of input after the error
	// is printed, before the loop reprompts.
	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n1\n"), buf) // Generate SQL Backup, twice
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

func TestRunRDMBackupRestoreMenu_PausesForAcknowledgmentAfterARefreshError(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = cancelingAction(new(int), cancel)
	actions.Refresh = func(ctx context.Context) error {
		refreshCalls++
		return errors.New("refresh boom")
	}

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n"), buf) // Generate SQL Backup
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

// TestRunRDMBackupRestoreMenu_PausesForAcknowledgmentAfterASuccessfulAction
// mirrors TestRunTagMgmtMenu_PausesForAcknowledgmentAfterASuccessfulAction
// -- DECISIONS.md, "Widen 'pause for acknowledgment' to every action, not
// just errors."
func TestRunRDMBackupRestoreMenu_PausesForAcknowledgmentAfterASuccessfulAction(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = func(ctx context.Context) error {
		fmt.Fprintln(term, "backup created")
		cancel()
		return nil
	}

	err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput("1\n\n"), buf) // Generate SQL Backup
	if err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	out := buf.String()
	statusIdx := strings.Index(out, "backup created")
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

func TestRunRDMBackupRestoreMenu_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testRDMBackupRestoreActions(&refreshCalls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runRDMBackupRestoreMenu(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

func TestRunRDMBackupRestoreMenu_CleanExitOnInterrupt(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = failingAction(huh.ErrUserAborted)

	if err := runRDMBackupRestoreMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on huh.ErrUserAborted, got: %v", err)
	}
}

func TestRunRDMBackupRestoreMenu_CleanExitOnEOF(t *testing.T) {
	var refreshCalls int
	term, buf := newTermOnly()
	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.RunSQLBackup = failingAction(io.EOF)

	if err := runRDMBackupRestoreMenu(context.Background(), term, actions, newHuhAccessibleInput("1\n"), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on io.EOF, got: %v", err)
	}
}

func TestRDMMenuItems_NoBackToDomainPickerEntry(t *testing.T) {
	if len(rdmMenuItems) != 5 {
		t.Fatalf("len(rdmMenuItems) = %d, want 5 (no \"Back to domain picker\" -- 'q' is the only way back)", len(rdmMenuItems))
	}
	for _, item := range rdmMenuItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q", item.label)
		}
	}
}

// TestRDMMenuItems_Order pins the DESIGN.md-specified order: Generate
// SQL Backup leads, then archive before restore, SQL before OpenSearch
// within each pair.
func TestRDMMenuItems_Order(t *testing.T) {
	want := []string{
		"Generate SQL Backup",
		"Archive SQL Backups to S3 (and trim local copies)",
		"Archive OpenSearch Snapshot to S3",
		"Restore SQL Backup from S3",
		"Restore OpenSearch Snapshot from S3",
	}
	for i, label := range want {
		if rdmMenuItems[i].label != label {
			t.Errorf("rdmMenuItems[%d].label = %q, want %q", i, rdmMenuItems[i].label, label)
		}
	}
}
