---
id: "0050"
title: "Per-file upload progress for Backup Archive & Trim"
date: "2026-07-02"
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
uuid: "bc771a2e-87e9-4ff0-b517-b1af610daef1"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing on `newauthors` (85 files, ~85 GB total)
surfaced that the upload phase's only feedback was a generic "...
uploading backup files to S3 (elapsed Xs)" heartbeat every 30 seconds --
no file count, no bytes, no way to tell whether it was actually making
progress or stuck, for a phase that can legitimately run for a long time
on a large backup set.

**Decision.** `UploadBackupFiles` now runs one `ssm:SendCommand` per
file instead of a single script covering the whole batch, and takes an
`onProgress func(UploadProgress)` callback invoked after each file
completes with a running `Done`/`Total` file count and `BytesDone`/
`BytesTotal`. `BackupArchiveAndTrim` uses this to print one line per
file: `... uploading 12/84 (1.2 GiB of 85.5 GiB) - OK <key>`.

**Rationale.**
- Real per-file progress requires the client to observe each file's
  outcome individually, which is only possible if each file is its own
  SSM command -- `ssm:GetCommandInvocation` doesn't expose partial
  stdout mid-script for a single long-running batched command.
- A callback (not a hardcoded print inside `UploadBackupFiles`) keeps
  the SSM-calling code testable without capturing terminal output, and
  leaves `nil` as a valid "don't care" value for callers that don't need
  a live display (as the existing unit tests exercise).

**Rejected alternatives.**
- *Keep one batched script, just upgrade the heartbeat to a spinner.*
  Cheaper, but still can't report which file or how far through the
  batch the operation actually is -- doesn't address the real ask.

**Consequences.** One `ssm:SendCommand`/`ssm:GetCommandInvocation`
round-trip pair per file instead of one for the whole batch -- more AWS
API calls and a bit more wall-clock overhead per file (each carries its
own poll-until-terminal wait), accepted in exchange for real progress
visibility on backup sets that can take a long time regardless.
`DefaultBackupUploadTimeout` (30 minutes) is now a per-file timeout
rather than a whole-batch timeout, which is more generous in aggregate
than before -- appropriate since a single ~1 GB file uploading within 30
minutes is already a very loose bound.

---

