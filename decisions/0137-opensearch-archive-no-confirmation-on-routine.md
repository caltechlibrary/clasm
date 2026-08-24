---
id: "0137"
title: "OpenSearch archive: no confirmation on routine runs, since the EBS-side delete isn't destructive"
date: "2026-07-28"
status: accepted
kind: decision
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
uuid: "5a5fdaa6-2486-49e9-82fa-8d30220e5f01"
origin_host: "MACMINI-RD.local"
---

**Context.** RDM Backup & Restore domain design (DESIGN.md, "Archive
OpenSearch Snapshot to S3") needed to decide whether every archive run
should require `ConfirmDestructive`, given Feature 11 (Backup Archive &
Trim) already gates its own delete-after-verify step behind one upfront
confirm.

**Decision.** Routine archive runs -- no S3-side cleanup threshold given
-- show no confirmation at all. OpenSearch remains fully available,
serving reads and writes normally, throughout snapshot creation, the S3
sync, and the local EBS-side delete via the OpenSearch API -- none of it
closes or disrupts any index the way Restore's index-close-before-restore
step does. The EBS-side delete specifically is safe because it only ever
removes a copy already independently verified in S3, never the only
copy of anything. The one real cost -- shared I/O and network bandwidth
with the production instance during snapshot creation and sync -- is
resource contention, not a destructive or irreversible action, and
doesn't warrant a type-to-confirm gate.

**Consequences.** `ConfirmDestructive` is reserved for the one genuinely
destructive path in this workflow -- the S3-side cleanup step, and only
when a threshold was given and matched real candidates (see "App-managed
S3-side cleanup," below). Every plain archive run, the common case,
proceeds with no prompts beyond directory/bucket/threshold.

---

