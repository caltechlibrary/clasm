---
id: "0162"
title: "Restore SQL Backup: resolve the Postgres target before any S3 prompt, not after"
date: "2026-08-18"
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
uuid: "a9f54f29-4050-4002-b3d4-5295f76b3cfb"
origin_host: "MACMINI-RD.local"
---

**Context.** PLAN.md Phase 20.50's originally-sketched work-item order
(2026-07-28) put `resolveRDMPostgresConfig` (Postgres-container
discovery) *after* the bucket/source-name/object-pick prompts and the S3
listing -- but that same document's own Tests section, for the identical
phase, describes a test as "zero or multiple `docker ps` results for the
target aborts before any S3 activity." Implementing both literally would
have been self-contradictory: discovery happening after S3 listing means
a broken Postgres target can never be caught "before any S3 activity,"
since the S3 activity already happened by the time discovery runs.

**Decision.** Moved Postgres-target discovery to run immediately after
the AWS-CLI preflight check, before the bucket prompt, the source-
instance-name prompt, the S3 object listing, and the object pick.

**Rationale.**
- A broken target (no running Postgres container, or more than one) is
  fatal to the whole restore regardless of which S3 object ends up
  picked -- there's no scenario where continuing through the bucket/
  object-pick prompts first is useful. Failing fast avoids making the
  operator pick a bucket and a potentially large object only to hit the
  identical failure right before the load step.
- Matches the Tests section's own evident intent, confirmed by a new
  regression test, `TestRestoreSQLBackup_DiscoveryFailureAbortsBeforeAnyS3Activity`,
  which asserts zero S3 calls of any kind on a discovery failure.

**Consequences.** No other step's relative order changed. See PLAN.md
Phase 20.50 (work items renumbered to match the implemented order).

---

