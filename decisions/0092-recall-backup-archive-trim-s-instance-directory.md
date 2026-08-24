---
id: "0092"
title: "Recall Backup Archive & Trim's instance/directory choices per-instance"
date: "2026-07-13"
status: accepted
kind: decision
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "586f35e2-5780-4ad3-aba2-18725928255a"
origin_host: "MACMINI-RD.local"
---

**Context.** Backup Archive & Trim always started from a blank slate:
pick an instance from the full list every time, and (absent a
`backupDirRules` Name-pattern match) type the backup directory from
scratch every time -- even for an operator who runs this same
instance/directory combination repeatedly. Requested directly: recall
what was used last time as the default for next time.

**Decision.** A new `internal/state` package persists a small
`~/.clasm_state` YAML file -- deliberately NOT folded into `~/.clasm`
(config.Config), which is exclusively user-hand-edited today (`Load()`
only, no `Save()` exists): auto-writing into a file the operator
maintains by hand risked silently reformatting or stripping their own
edits. `~/.clasm_state` is exclusively app-managed, safe to delete, and
never meant for hand-editing. History is keyed per-instance
(`map[instanceID]directory`), not a single global "last used" value,
since different instances plausibly back up different directories.
`internal/workflow.BackupHistory` is the narrow interface `internal/
workflow` sees (`LastInstanceID`, `LastDirectoryByInstance`, and a
`Save(instanceID, directory string) error` callback) -- this package
never imports `internal/state` or knows about YAML/file paths;
`cmd/clasm/main.go` owns the actual on-disk format and wires the
callback. The recalled directory takes priority over `backupDirRules`'
Name-pattern-based default (it reflects what was actually typed for
this exact instance most recently, not a generic pattern match). The
instance picker gained a new `tui.PickerConfig.InitialCursor int` field
(pre-positions the cursor on a specific row instead of always starting
at 0) so the recalled instance is pre-selected, not just pre-filled as
text -- `pickInstance`/`pickInstanceDefaulted` (`power_state.go`) split
so every other caller of `pickInstance` (start/stop/terminate/create-
AMI/etc.) is unaffected, while Backup Archive & Trim's own call site
passes `hist.LastInstanceID`.

**Rationale.** Separating "settings" (hand-edited, read-only to the
app) from "history" (app-managed, disposable) avoids ever surprising an
operator by rewriting a file they maintain themselves. Per-instance
keying costs one extra map versus a single string field, for
meaningfully better behavior once more than one instance is in
rotation. `Save`'s error is reported to `w` as a warning, not returned
as fatal -- history is a convenience, not core to the backup itself; a
disk-write failure here shouldn't abort an otherwise-successful backup
run.

**Consequences.** `BackupArchiveAndTrim`'s signature gained a
`hist BackupHistory` parameter (every call site, including all
existing tests, updated -- the zero value disables all of this,
matching pre-existing behavior exactly). `tui.PickerConfig` gained
`InitialCursor`; out-of-range values (including the zero value, when
there's no prior choice) fall back to row 0, so every other
`PickerConfig` caller is unaffected. New `internal/state` package (+
tests) and new tests in `internal/workflow/backup_archive_test.go`
(history takes priority over the Name-pattern rule; `Save` is called
with the right instance/directory; a `Save` error is a warning, not
fatal) and `internal/tui/picker_test.go` (`InitialCursor` positions the
cursor; out-of-range falls back to 0).

---

