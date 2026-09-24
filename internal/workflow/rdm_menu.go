package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// RDMBackupRestoreActions bundles the RDM Backup & Restore domain's menu
// entry points, mirroring TagMgmtActions/KeyMgmtActions' shape for the
// other domains (DESIGN.md, "RDM Backup & Restore Domain"). ArchiveSQL is
// Feature 11 (Backup Archive & Trim), relocated here unchanged from
// Compute (PLAN.md Phase 20.48); RunSQLBackup is new (PLAN.md Phase
// 20.52) -- an on-demand pg_dump trigger, generating a local dump and
// returning (no auto-chain into ArchiveSQL -- removed 2026-08-18, PLAN.md
// Phase 20.54, since it read as confusing in practice; see the "Generate
// SQL Backup" menu label below), so a full backup no longer depends on
// invenio-sql-backup.bash already being on the box; ArchiveOpenSearch/
// RestoreSQL/RestoreOpenSearch land in Phases 20.49-20.51.
type RDMBackupRestoreActions struct {
	RunSQLBackup      func(ctx context.Context) error
	ArchiveSQL        func(ctx context.Context) error
	ArchiveOpenSearch func(ctx context.Context) error
	RestoreSQL        func(ctx context.Context) error
	RestoreOpenSearch func(ctx context.Context) error
	// Refresh re-fetches the instance listing this domain's workflows
	// pick from, silently -- no display. Called once after every
	// successful dispatched action (DECISIONS.md, "Refresh data after
	// each operation") and once on entering this domain. Meaningful
	// here, unlike Configuration's own no-op Refresh, since Archive
	// OpenSearch/Restore actions make real AWS calls.
	Refresh func(ctx context.Context) error
}

// rdmItem pairs an RDM Backup & Restore menu label with the
// RDMBackupRestoreActions field it dispatches to.
type rdmItem struct {
	label string
	// cliSlug is this leaf's stable CLI path segment (PLAN.md Phase
	// 20.64, design brief "cli_forms_for_tui_leaves.md") -- same
	// frozen-once, hand-committed convention as domainItem.cliSlug.
	// Empty means this leaf has no CLI form and is unreachable from the
	// CLI path entirely.
	cliSlug string
	action  func(RDMBackupRestoreActions, context.Context) error
}

// rdmMenuItems is DESIGN.md's RDM Backup & Restore menu, in order:
// Generate SQL Backup leads (the natural first step for an instance with
// no existing dump yet), then archive before restore, SQL before
// OpenSearch within each pair (DESIGN.md, "RDM Backup & Restore Domain").
// No "Back to domain picker" entry -- DECISIONS.md, "TUI keybinding
// conventions": 'q' is the universal back key everywhere. "Generate SQL
// Backup" (was "Run SQL Backup" until 2026-08-18, PLAN.md Phase 20.54 --
// DECISIONS.md, "Run SQL Backup: drop the Archive-SQL auto-chain, rename
// to 'Generate SQL Backup'") names what the action actually does: it
// only ever generates the local dump file, never archives it.
// "Archive SQL Backups to S3 (and trim local copies)" (was "Archive SQL
// Backup to S3" until 2026-08-27, PLAN.md Phase 20.62 -- DR-0170,
// decision 8) is the same rename lesson a second time: the copy is now
// unconditional and covers every backup in the directory, while the
// trim is a separate, optional phase, and the old label described
// neither.
// ArchiveSQLBackupsCLISlug/ArchiveOpenSearchSnapshotCLISlug are the two
// archive leaves' cliSlug values, exported (PLAN.md Phase 20.64) so
// every other place that needs to name a leaf -- ParseBackupArchiveArgs/
// ParseOpenSearchArchiveArgs's own usage messages (cli_args.go),
// cmd/clasm's non-interactive dispatch switch, its pastable-command
// builder -- shares this one literal instead of each re-typing it.
const (
	ArchiveSQLBackupsCLISlug         = "archive-sql-backups-to-s3"
	ArchiveOpenSearchSnapshotCLISlug = "archive-opensearch-snapshot-to-s3"
)

var rdmMenuItems = []rdmItem{
	{label: "Generate SQL Backup", action: func(a RDMBackupRestoreActions, ctx context.Context) error { return a.RunSQLBackup(ctx) }},
	{label: "Archive SQL Backups to S3 (and trim local copies)", cliSlug: ArchiveSQLBackupsCLISlug, action: func(a RDMBackupRestoreActions, ctx context.Context) error { return a.ArchiveSQL(ctx) }},
	{label: "Archive OpenSearch Snapshot to S3", cliSlug: ArchiveOpenSearchSnapshotCLISlug, action: func(a RDMBackupRestoreActions, ctx context.Context) error { return a.ArchiveOpenSearch(ctx) }},
	{label: "Restore SQL Backup from S3", action: func(a RDMBackupRestoreActions, ctx context.Context) error { return a.RestoreSQL(ctx) }},
	{label: "Restore OpenSearch Snapshot from S3", action: func(a RDMBackupRestoreActions, ctx context.Context) error { return a.RestoreOpenSearch(ctx) }},
}

// pickRDMBackupRestoreItem runs the RDM Backup & Restore menu's huh.Select
// and returns the chosen rdmItem. Selects by index into rdmMenuItems, not
// by rdmItem itself -- huh.Select's T must be comparable, and
// rdmItem.action (a func) isn't. input/output are nil in production
// (interactive, real terminal) and supplied by tests for the
// accessible-mode pipe path.
func pickRDMBackupRestoreItem(w io.Writer, input io.Reader, output io.Writer) (rdmItem, error) {
	opts := make([]huh.Option[int], len(rdmMenuItems))
	for i, item := range rdmMenuItems {
		opts[i] = huh.NewOption(item.label, i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("Archive or restore SQL and OpenSearch backups to/from S3. Restore is destructive -- it overwrites data on the target instance.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintGoBack, field, input, output); err != nil {
		return rdmItem{}, err
	}
	return rdmMenuItems[idx], nil
}

// RunRDMBackupRestoreMenu runs the RDM Backup & Restore domain's
// interactive menu loop, the same shape as RunTagMgmtMenu: show the menu,
// dispatch the chosen action, refresh after a successful dispatch, and
// repeat -- until the picker is aborted ('q'/ctrl+c, reported as
// ErrBackToDomainPicker) or an exit signal is hit (reported as nil, which
// RunDomainPicker treats as "exit the whole program"). A single action's
// error is shown and the loop continues.
func RunRDMBackupRestoreMenu(ctx context.Context, w io.Writer, actions RDMBackupRestoreActions) error {
	return runRDMBackupRestoreMenu(ctx, w, actions, nil, nil)
}

// rdmItemBySlug finds the rdmItem whose cliSlug matches slug (PLAN.md
// Phase 20.64) -- a leaf with no CLI form yet has cliSlug == "", so it
// never matches here regardless of what slug is asked for (design
// brief, decision 3: unreachable from the CLI path entirely, not just
// missing a full-args form).
func rdmItemBySlug(slug string) (rdmItem, bool) {
	for _, item := range rdmMenuItems {
		if item.cliSlug != "" && item.cliSlug == slug {
			return item, true
		}
	}
	return rdmItem{}, false
}

// RDMLeafCLISlugExists reports whether slug is a registered RDM leaf CLI
// slug -- same reasoning as DomainCLISlugExists, one level deeper.
func RDMLeafCLISlugExists(slug string) bool {
	_, found := rdmItemBySlug(slug)
	return found
}

// RunRDMBackupRestoreMenuFromSlug runs slug's own leaf action once,
// exactly as one iteration of RunRDMBackupRestoreMenu's own loop would
// (print/pause on error, pause+refresh on success), then falls into a
// fresh RunRDMBackupRestoreMenu for continuation -- so
// `clasm rdm-backup-and-restore <leaf-slug>` behaves exactly as if the
// operator had navigated to this domain and chosen that one menu entry
// themselves (PLAN.md Phase 20.64, design brief decision 1, applied one
// level deeper than RunDomainPickerFromSlug). ok is false when slug
// isn't a registered leaf CLI slug; nothing is run in that case.
func RunRDMBackupRestoreMenuFromSlug(ctx context.Context, w io.Writer, actions RDMBackupRestoreActions, slug string) (ok bool, err error) {
	return runRDMBackupRestoreMenuFromSlug(ctx, w, actions, slug, nil, nil)
}

// runRDMBackupRestoreMenuFromSlug is RunRDMBackupRestoreMenuFromSlug's
// testable core.
func runRDMBackupRestoreMenuFromSlug(ctx context.Context, w io.Writer, actions RDMBackupRestoreActions, slug string, menuInput io.Reader, menuOutput io.Writer) (ok bool, err error) {
	item, found := rdmItemBySlug(slug)
	if !found {
		return false, nil
	}

	if err := item.action(actions, ctx); err != nil {
		if isExitSignal(err) {
			printExiting(w)
			return true, nil
		}
		fmt.Fprintf(w, "Error: %s\n", formatError(err))
		pauseForAcknowledgment(menuInput, menuOutput)
		return true, runRDMBackupRestoreMenu(ctx, w, actions, menuInput, menuOutput)
	}

	pauseForAcknowledgment(menuInput, menuOutput)
	if refreshErr := actions.Refresh(ctx); refreshErr != nil {
		fmt.Fprintf(w, "Error refreshing: %s\n", formatError(refreshErr))
		pauseForAcknowledgment(menuInput, menuOutput)
	}
	return true, runRDMBackupRestoreMenu(ctx, w, actions, menuInput, menuOutput)
}

// runRDMBackupRestoreMenu is RunRDMBackupRestoreMenu's testable core:
// menuInput/menuOutput are nil in production and supplied by tests to
// drive the same huh.Select through its accessible-mode pipe path
// instead.
func runRDMBackupRestoreMenu(ctx context.Context, w io.Writer, actions RDMBackupRestoreActions, menuInput io.Reader, menuOutput io.Writer) error {
	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		choice, err := pickRDMBackupRestoreItem(w, menuInput, menuOutput)
		if err != nil {
			return mapMenuPickerErr(err)
		}

		if err := choice.action(actions, ctx); err != nil {
			if isExitSignal(err) {
				printExiting(w)
				return nil
			}
			fmt.Fprintf(w, "Error: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
			continue
		}

		// The dispatched action succeeded and may have printed its own
		// status output (DECISIONS.md, "Widen 'pause for acknowledgment'
		// to every action, not just errors") -- pause before Refresh's
		// own (silent, no-display) work and the next redraw.
		pauseForAcknowledgment(menuInput, menuOutput)

		if err := actions.Refresh(ctx); err != nil {
			fmt.Fprintf(w, "Error refreshing: %s\n", formatError(err))
			pauseForAcknowledgment(menuInput, menuOutput)
		}
	}
}
