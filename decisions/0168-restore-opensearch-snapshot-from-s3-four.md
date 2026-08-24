---
id: "0168"
title: "Restore OpenSearch Snapshot from S3: four implementation-time corrections to PLAN.md Phase 20.51's original sketch"
date: "2026-08-19"
status: accepted
kind: correction
trigger: live-test
project: clasm
phase: "20.51"
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "9bb92ffd-af0e-4eda-90e0-997a5822bdfa"
origin_host: "MACMINI-RD.local"
---

**Context.** Implementing Phase 20.51 test-first (design/decision already
settled 2026-07-28/29 -- DESIGN.md, "Restore OpenSearch Snapshot from
S3"; DECISIONS.md, "Restore OpenSearch: delete conflicting indices
before `_restore`, don't close them"), four points where the original
work-item sketch needed a real correction before it could be built
soundly, each checked against real behavior rather than guessed:

1. **Listing archived snapshot sub-prefixes reuses
   `ListArchivedSnapshotPrefixes` (opensearch_cleanup.go), not a new
   `ListSnapshotPrefixes`.** The original sketch called for a new,
   `[]string`-returning function. `ListArchivedSnapshotPrefixes` already
   does exactly this listing (`CommonPrefixes` under
   `<prefix>/opensearch-snapshots/`), already parses each name's
   timestamp (free `CreatedAt` for the picker's display label, matching
   `pickS3Object`'s own timestamp-in-label precedent) and already skips
   a malformed name rather than failing the whole listing. Writing a
   second, near-duplicate prefix-listing function would only reproduce
   logic already written and tested for Archive OpenSearch's own cleanup
   path.
2. **Conflicting-index detection is scoped to two broad wildcards
   (`<prefix>-*`, `.ds-<prefix>-*`), not the full 18-pattern curated
   list verbatim.** Checked live 2026-08-19 against CaltechAUTHORS
   production (`_cat/indices?h=index`, `_cat/indices/<pattern>?h=...`)
   to confirm real response shape before writing any parsing code, per
   this project's established practice. One curated pattern
   (`<prefix>-stats-bookmarks`) is a bare exact name, not a wildcard --
   comma-joining it with the other 17 wildcard patterns in one
   `_cat/indices` call risks a 404 if that one exact index happens not
   to exist (the same category of surprise Archive OpenSearch's own
   `ignore_unavailable:true` was added to avoid on the snapshot-creation
   side). Querying two always-wildcard patterns instead is
   unconditionally safe (OpenSearch's `_cat` endpoints degrade to an
   empty, not an error, result for a non-matching wildcard) and still
   covers every curated pattern, since each one is itself a match of the
   broader `<prefix>-*`/`.ds-<prefix>-*` shapes; the *precise* curated-
   pattern match happens client-side afterward (`matchesAnyPattern`),
   so nothing unintended slips through.
3. **Post-restore verification reports `_cat/indices`' own observed
   health/status/docs.count per index, not a comparison against the
   snapshot's own internal `_status` metadata**, as the original sketch
   proposed. Checking a real `_snapshot/<repo>/<name>/_status` response
   requires the snapshot to still exist in the *local* repo -- but
   Archive OpenSearch Snapshot always deletes the EBS-side snapshot
   immediately after syncing to S3 (its own established, unconditional
   behavior), confirmed live 2026-08-19 by querying a real, already-
   archived CaltechAUTHORS snapshot's `_status` and getting
   `snapshot_missing_exception` back -- the data genuinely isn't there
   to compare against until Restore's own sync-down step puts it back,
   and even then the exact per-index doc-count field path in that
   verbose endpoint's response has never been confirmed against a real
   OpenSearch reply. `_cat/indices`' plain-text shape *was* confirmed
   live (point 2, same session) -- reporting what it actually shows
   (each restored index's real health/status/doc count, flagging red
   health explicitly) is simpler, avoids guessing at an unverified
   nested JSON path, and still catches the failure modes that matter:
   an index missing from the response entirely, or one that came back
   broken.
4. **Conflicting-index detection and deletion run immediately after the
   AWS-CLI preflight, before any bucket/source-name/snapshot-pick
   prompt or the (potentially multi-gigabyte) sync-down** -- applying
   Restore SQL Backup's own step-order lesson (PLAN.md Phase 20.50,
   DECISIONS.md, "Restore SQL Backup: resolve the Postgres target
   before any S3 prompt, not after") proactively from the start, rather
   than needing a second live-testing round to rediscover the same
   "why did we transfer gigabytes before finding out about a blocking
   problem" pain point. This works because which indices might conflict
   depends only on the *target* instance's own index prefix (Project/
   Name tag) -- entirely independent of which archived snapshot
   eventually gets restored -- so there's no reason to defer the check
   until after picking one.

**Decision.** All four adopted as described above.

**Rationale.** Each is grounded in either reusing already-tested code
(point 1), a live-checked real API response shape (points 2 and 3), or
a lesson this same project already paid to learn once, this session,
on the SQL-restore side (point 4) -- consistent with this project's
recurring practice of checking real AWS/API facts before writing code
rather than guessing, and of applying a hard-won lesson proactively
once learned rather than waiting to relearn it.

**Consequences.** PLAN.md Phase 20.51 implemented and unit-tested,
test-first throughout, 2026-08-19 (`internal/workflow/
restore_opensearch.go`/`restore_opensearch_test.go`, new files); wired
into `cmd/clasm/main.go`'s `RestoreOpenSearch` action, replacing its
`NotYetImplemented` stub. `go build`/`vet`/`test -race`/`gofmt` all
clean. **Real-AWS-verified end to end, same day**, after correction 5
below landed -- restoring CaltechDATA production's real snapshot onto
`caltechdata-restore-test` succeeded on the first attempt, including
the `_cat/recovery`-based `PollRestoreUntilComplete` poller (all 43
snapshot-type shard rows reached `done`), confirming its API shape was
correctly understood without having been live-tested beforehand. See
this file's own "...a fifth correction..." entry and PLAN.md Phase
20.51 (updated) for the full verification detail.

---

