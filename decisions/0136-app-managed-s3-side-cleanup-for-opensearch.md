---
id: "0136"
title: "App-managed S3-side cleanup for OpenSearch backups, not a bucket lifecycle policy"
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
uuid: "75edc6c4-a960-4ae7-b22e-dd1141cd6ee2"
origin_host: "MACMINI-RD.local"
---

**Context.** OpenSearch backups can't use SQL backups' "keep several
days locally, then trim" model (see "Single OpenSearch snapshot on EBS
at a time," below) -- only one snapshot is ever kept on EBS, and each
archive run's segment files land in their own new S3 sub-prefix, so
S3-side storage grows without bound unless something expires old
prefixes. clasm already has S3 bucket lifecycle management (Feature
21.1), the obvious first candidate, and was the first approach proposed
here.

**Decision.** Rejected a native S3 lifecycle policy in favor of
app-managed cleanup driven from inside the Archive OpenSearch Snapshot
workflow itself. A lifecycle rule runs on its own schedule independent
of whether fresh archives are actually happening -- if archiving ever
stalls (a lapsed cron, a decommissioned habit, someone simply
forgetting), a lifecycle rule will still happily expire an instance's
last remaining backup, leaving zero backups with nobody the wiser until
one is needed. Coupling cleanup to a successful archive run instead
means cleanup only ever executes as a side effect of a fresh snapshot
having just landed safely in S3 -- there is no path to zero backups that
doesn't also involve a fresh one replacing them first.

**Consequences.** clasm implements its own list/parse/dry-run/confirm/
batch-delete logic (new `opensearch_cleanup.go`, PLAN.md Phase 20.49)
rather than a one-time AWS-side policy configuration. S3 has no atomic
"delete a whole prefix" call, so deleting one old snapshot means listing
then batch-deleting up to 1,000 keys at a time (`DeleteObjects`).
`sql-backups.library.caltech.edu`'s own lifecycle status is a separate,
pre-existing question this decision doesn't resolve -- flagged for a
separate audit (DESIGN.md, "Not decided yet").

---

