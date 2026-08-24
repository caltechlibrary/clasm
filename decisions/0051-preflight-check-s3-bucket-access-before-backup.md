---
id: "0051"
title: "Preflight check: S3 bucket access before Backup Archive & Trim's dry-run list"
date: "2026-07-02"
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
uuid: "2b9c9030-0e89-4721-90d0-10820c3102f9"
origin_host: "MACMINI-RD.local"
---

**Context.** Today's `sql-backups.library.caltech.edu` bucket test run
happened to fail for an unrelated reason (the target instance's AWS CLI
wasn't installed), but it highlighted a gap: if the operator's own
credentials can't reach the entered bucket at all (typo'd name, bucket
doesn't exist yet, missing `s3:ListBucket`), the workflow wouldn't find
out until the independent verification step, well after the dry-run
list, the destructive type-to-confirm gate, and a potentially large
upload have already run.

**Decision.** Right after the "S3 bucket" prompt, `BackupArchiveAndTrim`
calls a new `CheckS3BucketAccess`, which does an `s3:HeadBucket` with the
operator's own credentials and aborts immediately with an actionable
error (naming the bucket, and hinting at the likely cause) if it fails
-- before the dry-run list, before type-to-confirm, before any upload.

**Rationale.**
- `s3:HeadBucket` is the standard cheap existence-and-access check: no
  object needs to exist, and both "bucket doesn't exist" and "no
  permission" surface as an error from this one call.
- Checking immediately after the bucket name is entered, rather than
  waiting for the independent verification step later, means an
  operator who mistypes a bucket name finds out in seconds, not after
  a dry-run list and (potentially large) upload have already run.

**Consequences.** `awsclient.S3API` gains `HeadBucket` alongside the
existing `HeadObject`; every implementation (the real SDK client, the
logging decorator, and the test fake) had to add it. No functional
change to the upload/verify/delete pipeline itself.

---

