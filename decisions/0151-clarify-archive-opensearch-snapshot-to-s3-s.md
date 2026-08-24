---
id: "0151"
title: "Clarify Archive OpenSearch Snapshot to S3's cleanup-threshold prompt wording"
date: "2026-07-29"
status: accepted
kind: refinement
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
uuid: "8a117efe-9f6f-41ac-aacc-da2aecda79db"
origin_host: "MACMINI-RD.local"
---

**Context.** Same live-testing session: the user found the cleanup
prompt ("Clean up this instance's OpenSearch backups older than how
many days?") ambiguous -- unclear whether "backups" meant the
instance's own local `/opt/rdm_opensearch_backups` directory or the
already-archived copies in S3.

**Decision.** Reworded to: "Delete this instance's previously-archived
OpenSearch snapshots in S3 older than how many days? (blank to skip --
does not affect anything on the instance itself, or the snapshot this
run is about to create)".

**Rationale.** Spells out exactly what's at stake (S3-side archived
copies only) and exactly what's safe (the local directory, and the
snapshot this run is about to create) in the prompt itself, rather than
requiring the operator to infer it or check documentation mid-run.

**Consequences.** `promptOpenSearchCleanupDays`
(`internal/workflow/opensearch_archive.go`) wording only -- no test
asserted on the old literal text, so no test changes were needed.

---

