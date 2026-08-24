---
id: "0150"
title: "Archive OpenSearch Snapshot to S3 (Phase 20.49): a separate BackupHistory instantiation, not a shared one"
date: "2026-07-29"
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
uuid: "50bb90c4-e27d-4d26-8319-a993d59fac77"
origin_host: "MACMINI-RD.local"
---

**Context.** PLAN.md Phase 20.49's own work-item text specifies
`ArchiveOpenSearchSnapshot(..., hist BackupHistory)`, reusing Run SQL
Backup/Archive SQL Backup's existing `BackupHistory` *type* to recall the
last-picked instance and pre-fill the backup-directory prompt's default.
It doesn't say whether the actual data behind that struct should be
shared with SQL's own recall state or kept separate -- a real gap in the
committed design, surfaced only once implementation had to decide what
`main.go` actually passes in.

**Decision.** **Reuse the `BackupHistory` struct type unchanged, but back
it with a brand-new, independent `appState.OpenSearchArchive` field**
(`internal/state.State`, same shape as the existing `BackupArchive`
field), not the SQL workflows' own `appState.BackupArchive`.

**Rationale.**
- An instance's SQL backup directory (`/opt/rdm_sql_backups`) and its
  OpenSearch backup directory (`/opt/rdm_opensearch_backups`) are
  unrelated paths. `LastDirectoryByInstance` is keyed only by instance
  ID, so sharing one map would mean running Archive OpenSearch Snapshot
  right after Run SQL Backup (or vice versa) on the same instance would
  silently overwrite the other workflow's recalled default the next time
  either runs.
- The struct *type* staying shared (rather than inventing a second,
  parallel type) keeps `BackupHistory`'s existing shape/semantics --
  `LastInstanceID`, `LastDirectoryByInstance`, `Save` -- as the one
  established "recall a workflow's instance/directory choices" pattern
  in this codebase, matching how `RDMPostgresRule` got its own config
  slice separate from `BackupDirectoryRule` despite a similar shape.

**Rejected alternatives.**
- *Share `appState.BackupArchive` directly* -- the literal reading of
  Phase 20.49's Work Items text, but would introduce a real, silent
  cross-workflow data-clobbering bug for any instance run through both
  workflows.
- *A wholly new type for OpenSearch's own recall* -- unnecessary
  duplication; `BackupHistory`'s existing shape already fits exactly.

**Consequences.**
- `internal/state.State` gains `OpenSearchArchive BackupArchiveState`
  (yaml `opensearch_archive`), independent of the existing
  `BackupArchive` field.
- `main.go` gains a second `saveOpenSearchArchiveHistory` closure and
  `openSearchArchiveHistory` `BackupHistory` instantiation, parallel to
  `saveBackupHistory`/`backupHistory`.

---

