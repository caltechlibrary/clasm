---
id: "0138"
title: "OpenSearch backup bucket confirmed: `opensearch-backups.library.caltech.edu` already exists"
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
uuid: "eec1f1eb-27d8-4b5e-843e-5d09fb904488"
origin_host: "MACMINI-RD.local"
---

**Context.** DESIGN.md's "Archive OpenSearch Snapshot to S3" section
named `opensearch-backups.library.caltech.edu` as the target bucket,
but this decision was never recorded here, and a knowledge-base
observation from the same 2026-07-28 session separately listed the
bucket name as still undecided -- a real discrepancy surfaced while
reviewing the domain's planning before starting implementation.

**Decision.** The named bucket is correct and already exists in AWS
(confirmed directly by the user 2026-07-29) -- the DESIGN.md text was
right; the knowledge-base note was stale. No new bucket-naming work is
needed before Phase 20.49.

**Consequences.** No separate provisioning step is needed before Phase
20.49 -- the bucket already exists. Correcting an overstatement from an
earlier draft of this entry: Feature 11 doesn't actually recall a last-
used bucket at all (`BackupHistory` only recalls the last instance and
directory per instance; `promptBackupBucket` lists every bucket in the
account fresh, with no pre-selection, every run). Phase 20.49 reuses
that same picker mechanism unchanged (`promptBackupBucketFunc`) -- the
operator picks `opensearch-backups.library.caltech.edu` from the live
list each run (or "Other" to type it), exactly like every other bucket
choice in this app. Nothing hardcodes or bypasses the picker; this entry
exists so the bucket's name/identity has the same dated record every
other choice in this domain already has, rather than living only in
DESIGN.md's workflow prose.

---

