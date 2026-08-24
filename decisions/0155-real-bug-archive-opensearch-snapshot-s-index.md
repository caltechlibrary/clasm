---
id: "0155"
title: "Real bug: Archive OpenSearch Snapshot's index-match patterns used the Name tag, not the Project tag"
date: "2026-08-17"
status: accepted
kind: correction
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "0c430935-0f97-49e0-85f9-6ec9b633a37d"
origin_host: "MACMINI-RD.local"
---

**Context.** Ran Archive OpenSearch Snapshot to S3 (Phase 20.49) against
CaltechAUTHORS production (`i-0c4c81336aea33d27`) for the first time,
after its `path.repo`/persistent-volume retrofit was completed and
verified the same day. The snapshot reported `state: SUCCESS` but
`shards: {"total":0,"failed":0,"successful":0}` and `duration_in_millis:
0` -- an entirely empty snapshot, confirmed via the debug log's captured
`CreateSnapshot` command: its `indices` filter was built from
`newauthors-rdmrecords-*`, `newauthors-stats-record-view-*`, etc. --
this instance's Name tag (`newauthors`, a legacy label) -- while its
real indices are all prefixed `caltechauthors-` (confirmed live the same
day via `_cat/indices?v`: `caltechauthors-rdmrecords-records-record-v7.0.0`
at 147,438 docs). `ignore_unavailable: true` (deliberate, so a wrong
pattern fails quietly rather than aborting the whole run) meant
OpenSearch never raised an error about the mismatch -- it just silently
matched nothing. The instance's Project tag (`caltechauthors`) matches
the real index prefix exactly.

This is the identical shape of mistake Phase 20.52 already found and
fixed once, for Postgres `db_name`/`db_user` defaulting (see 2026-07-29
below, "Default db_name/db_user to the instance's Project tag, not its
Name tag") -- a `Name` tag that's a legacy/display label diverging from
the `Project` tag that actually corresponds to how the instance's own
resources (there, the Postgres DB name; here, the OpenSearch index
prefix) are really named.

**Decision.** `opensearch_archive.go`'s single `prefix` variable is
split into two: the existing `prefix` (`inst.Name`, unchanged) continues
to drive the S3 destination key -- purely cosmetic, already decided
(2026-08-12 hand-off) to leave as-is, since renaming the tag risks
breaking unknown external automation that may depend on its current
value. A new `indexPrefix := cmp.Or(inst.Project, prefix)` is used
*only* for `rdmOpenSearchSnapshotIndexPatterns` -- preferring the
Project tag, falling back to the same S3-side prefix (Name, then
InstanceID) only when Project is blank.

**Rationale.**
- Matches an already-established, tested pattern (`cmp.Or(inst.Project,
  inst.Name)`, `run_sql_backup.go`) rather than inventing a new
  resolution rule -- same fix shape, different call site.
- Deliberately does *not* touch the EC2 Name tag itself -- the user
  explicitly chose to fix clasm instead, precisely because it's unknown
  what other automation might depend on the tag's current value
  (`newauthors`). A code-side fix carries no such external-dependency
  risk.
- Keeps the S3 key-naming and OpenSearch index-matching concerns
  independently correct rather than coupling them through one variable
  that can't simultaneously be right for both purposes on an instance
  where Name and Project diverge.
- Reproduced test-first:
  `TestArchiveOpenSearchSnapshot_IndexPatternsUseProjectTagNotNameTag`
  (`inst.Project = "caltechauthors"`, `inst.Name = "newauthors"`)
  confirmed failing against the pre-fix code (asserted `indices` command
  contained `caltechauthors-rdmrecords-` and did not contain
  `newauthors-rdmrecords-`, while the sync command still used
  `newauthors/opensearch-snapshots`) before the fix was applied. All
  pre-existing Archive OpenSearch Snapshot tests (none of which set
  `Project`) pass unchanged, since `cmp.Or` falls back to the prior
  behavior when Project is blank. `go build`/`vet`/`test ./... -race`/
  `gofmt -l` all clean.

**Rejected alternatives.**
- *Rename the EC2 Name tag to `caltechauthors`* -- would fix both this
  and the cosmetic S3-prefix mismatch in one move, no code change
  needed. Rejected by the user for the same reason a Name-tag rename was
  rejected once before (2026-08-12): unknown what other scripts/tooling
  might key off the tag's current value. The stakes are higher now that
  the tag also silently determines what a production backup actually
  captures, but the underlying unknown-dependency risk is unchanged, and
  the user's call was to eliminate that risk entirely by fixing clasm
  instead.
- *A new config override table* (mirroring `rdm_postgres_config`'s
  pattern-matching rules) -- rejected as unnecessary complexity for a
  problem the existing `Project`-tag convention already solves cleanly;
  worth revisiting only if a future instance needs an index prefix that
  matches neither its Name nor its Project tag.

**Consequences.**
- CaltechAUTHORS's next Archive OpenSearch Snapshot run will correctly
  match its real indices; the S3 destination path is unaffected
  (`s3://.../newauthors/opensearch-snapshots/...`, unchanged).
- Any other instance whose Name and Project tags diverge is now
  protected by the same fallback, not just CaltechAUTHORS.
- The empty snapshot (`rdm-20260817-232634`) created during the
  live-testing session that surfaced this bug was never synced to S3
  (that step failed separately, on IAM grounds, before this bug's
  consequence would have mattered) -- no cleanup required.

---

