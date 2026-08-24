---
id: "0135"
title: "OpenSearch cleanup: optional day threshold, one upfront confirm against a fixed candidate list, delete only after the new snapshot is safely synced"
date: "2026-07-28"
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
uuid: "62e271ca-e6c0-4105-9f6e-76a808e0d973"
origin_host: "MACMINI-RD.local"
---

**Context.** Once app-managed cleanup was chosen (above), needed to
decide where in the Archive OpenSearch Snapshot workflow the threshold
is asked, how confirmation is gated, and when deletion actually executes
relative to the new snapshot's own creation.

**Decision.** Prompt for the day threshold immediately after the bucket
is picked, accepting blank to skip cleanup entirely -- no default,
unlike `promptAgeDays`'s required positive integer. If a threshold is
given, list the instance's existing snapshot sub-prefixes older than it
*before* anything else happens, show a Feature-11-style dry-run, and
gate on one `ConfirmDestructive` -- once, upfront, covering the whole
run (matching the user's own preferred sequence: bucket, then threshold,
then confirm, then the actual archive work). That confirmed candidate
list is captured and reused unchanged when cleanup actually executes,
never re-derived right before deleting -- the same time-of-check/
time-of-use avoidance Feature 11's own delete phase already established.

**Consequences.** Execution order still runs archive-first (create,
poll, sync, verify, EBS-delete) and cleanup last, even though both were
confirmed together upfront -- a fresh snapshot is always safely in S3
before anything old is removed. If the threshold matches zero existing
candidates, no confirmation is shown at all, matching Feature 11's own
"nothing to do, skip the confirm" precedent.

---

