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

// TestRDMMenuItems_CLISlugs pins PLAN.md Phase 20.64: only the two
// archive leaves get a cliSlug in this phase (mechanical rule:
// lowercase, hyphenate, drop parentheticals). Generate SQL Backup and
// both Restore leaves stay "" -- unreachable from the CLI path entirely,
// per the design brief's decision 3.
func TestRDMMenuItems_CLISlugs(t *testing.T) {
	want := map[string]string{
		"Generate SQL Backup":                               "",
		"Archive SQL Backups to S3 (and trim local copies)": "archive-sql-backups-to-s3",
		"Archive OpenSearch Snapshot to S3":                 "archive-opensearch-snapshot-to-s3",
		"Restore SQL Backup from S3":                        "",
		"Restore OpenSearch Snapshot from S3":               "",
	}
	for _, item := range rdmMenuItems {
		wantSlug, ok := want[item.label]
		if !ok {
			t.Fatalf("unexpected rdm menu label %q -- update this test's want map", item.label)
		}
		if item.cliSlug != wantSlug {
			t.Errorf("rdmMenuItems[%q].cliSlug = %q, want %q", item.label, item.cliSlug, wantSlug)
		}
	}
}

// TestRunRDMBackupRestoreMenuFromSlug_UnknownSlugIsNotFound pins that an
// unregistered slug does nothing at all, same contract as
// domainItemBySlug/RunDomainPickerFromSlug one level up.
func TestRunRDMBackupRestoreMenuFromSlug_UnknownSlugIsNotFound(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	actions := testRDMBackupRestoreActions(&refreshCalls)

	ok, err := runRDMBackupRestoreMenuFromSlug(context.Background(), term, actions, "no-such-leaf", nil, nil)
	if ok {
		t.Error("expected ok = false for an unregistered slug")
	}
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for an unregistered slug, got:\n%s", buf.String())
	}
}

// TestRunRDMBackupRestoreMenuFromSlug_RunsThatLeafThenContinuesTheNormalMenu
// pins PLAN.md Phase 20.64's leaf-level deep-link: `clasm
// rdm-backup-and-restore archive-sql-backups-to-s3` (zero further args)
// runs that one leaf directly -- consuming no menu input to get there --
// then behaves exactly as if the operator were now sitting at the normal
// RDM menu (able to pick a different entry next), not a dead end and not
// an immediate exit.
func TestRunRDMBackupRestoreMenuFromSlug_RunsThatLeafThenContinuesTheNormalMenu(t *testing.T) {
	var refreshCalls, archiveSQLCalls, archiveOSCalls int
	ctx, cancel := context.WithCancel(context.Background())
	term, buf := newTermOnly()

	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.ArchiveSQL = countingAction(&archiveSQLCalls)                // the deep-linked leaf -- must consume no menu input
	actions.ArchiveOpenSearch = cancelingAction(&archiveOSCalls, cancel) // chosen from the fallback menu

	// Three lines: pauseForAcknowledgment after the deep-linked leaf
	// consumes one (its content doesn't matter -- huh's accessible Input
	// has no validator here); "3" is the actual menu pick (Archive
	// OpenSearch Snapshot to S3, not "1", so a wrongly-consumed line
	// would produce a visibly wrong result, not an accidental pass); the
	// third is runRDMBackupRestoreMenu's own post-dispatch pause.
	menuInput := newHuhAccessibleInput("\n3\n\n")

	ok, err := runRDMBackupRestoreMenuFromSlug(ctx, term, actions, "archive-sql-backups-to-s3", menuInput, buf)
	if !ok {
		t.Fatal("expected ok = true for a registered leaf slug")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if archiveSQLCalls != 1 {
		t.Errorf("archiveSQLCalls = %d, want 1 (the deep-linked leaf, run once)", archiveSQLCalls)
	}
	if archiveOSCalls != 1 {
		t.Errorf("archiveOSCalls = %d, want 1 (chosen from the menu after falling back to it)", archiveOSCalls)
	}
	if refreshCalls != 2 {
		t.Errorf("refreshCalls = %d, want 2 (once after the deep-linked leaf, once after the menu-chosen one)", refreshCalls)
	}
}

// TestRunRDMBackupRestoreMenuFromSlug_ExitSignalReturnsNilWithoutContinuing
// pins that an exit signal from the deep-linked leaf itself ends the
// whole run cleanly, without falling into the menu at all.
func TestRunRDMBackupRestoreMenuFromSlug_ExitSignalReturnsNilWithoutContinuing(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	actions := testRDMBackupRestoreActions(&refreshCalls)
	actions.ArchiveSQL = func(ctx context.Context) error { return huh.ErrUserAborted }

	ok, err := runRDMBackupRestoreMenuFromSlug(context.Background(), term, actions, "archive-sql-backups-to-s3", nil, nil)
	if !ok || err != nil {
		t.Fatalf("got ok=%t err=%v, want ok=true err=nil", ok, err)
	}
	if refreshCalls != 0 {
		t.Errorf("refreshCalls = %d, want 0 (an exit signal must not refresh or continue into the menu)", refreshCalls)
	}
	if !strings.Contains(buf.String(), "Exiting") {
		t.Errorf("expected an Exiting message, got:\n%s", buf.String())
	}
}
