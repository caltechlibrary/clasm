---
id: "0139"
title: "Restore OpenSearch: delete conflicting indices before `_restore`, don't close them"
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
uuid: "a594c233-3b33-4c56-b4cd-2c19032bd9d2"
origin_host: "MACMINI-RD.local"
---

**Context.** DESIGN.md's "Restore OpenSearch Snapshot from S3" (PLAN.md
Phase 20.51) left open whether restoring into an already-populated
target should close or delete indices that share a name with the
snapshot being restored -- OpenSearch's restore API can't overwrite an
open index in place either way, so one or the other has to happen first.

**Decision.** Delete the conflicting indices, then restore -- the user's
explicit call. Gated behind the same `ConfirmDestructive` tier already
designed for this step (Feature 9/IAM Delete Role's tier), so the
destructive action is already confirmed before it happens.

**Consequences.** DESIGN.md step 4 and PLAN.md Phase 20.51's work items
now specify an explicit index-delete call (`DELETE <index-name>` via the
OpenSearch REST API, not a raw filesystem operation -- consistent with
this domain's existing "never touch the repo/index files directly,
always go through the OpenSearch API" pattern) immediately before
`POST /_snapshot/<repo>/<name>/_restore`, rather than a close/reopen
sequence. Resolves the last item in DESIGN.md's "Not decided yet" list
that was a genuine design choice rather than an implementation detail.

---

