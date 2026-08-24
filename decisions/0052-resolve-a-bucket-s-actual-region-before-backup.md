---
id: "0052"
title: "Resolve a bucket's actual region before Backup Archive & Trim's access check"
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
uuid: "d82dfa96-5bc6-4c0f-89c7-d74c4f5c8e38"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing against `sql-backups.library.caltech.edu`
failed with `AWS error [MovedPermanently]: Moved Permanently` on the
brand-new `s3:HeadBucket` preflight check (above). The debug log showed
the call went out scoped to `us-west-1` (`cfg.Regions[0]`, the region
`awsops`' single global S3 client has always used) -- but the bucket
isn't in that region. `HeadBucket`/`HeadObject` return a bare 301 with
no useful detail when the calling client's region doesn't match the
bucket's; DESIGN.md's "a bucket's home region is unrelated to the
instance's" was correct, but the code never actually resolved *which*
region a given bucket is in before talking to it.

**Decision.** `BackupArchiveAndTrim` now takes both the original
`s3Client` (used only to call the new `BucketRegion`, which resolves a
bucket's true region via `s3:GetBucketLocation` -- a control-plane call
that, unlike `HeadBucket`, works from a client scoped to any region) and
a `newS3Client func(ctx, region) (awsclient.S3API, error)` factory. Once
the bucket's region is known, `newS3Client` builds a client actually
scoped to that region, used for the `CheckS3BucketAccess` preflight and
every later `s3:HeadObject` verification call in the run.

**Rationale.**
- `s3:GetBucketLocation` exists specifically to answer "what region is
  this bucket in" without already knowing the answer -- it's the
  standard mechanism for this, not a workaround.
- A factory function (rather than eagerly building N per-region S3
  clients at startup, the way EC2/SSM clients are pre-built for every
  configured region) fits S3 better: buckets can be in any AWS region,
  not just the ones in `~/.awsops`' `regions` list, so there's no fixed
  set to pre-build against.
- Considered and rejected using
  `github.com/aws/aws-sdk-go-v2/feature/s3/manager`'s `GetBucketRegion`
  helper (which does something similar by inspecting a `HeadBucket`
  redirect's `x-amz-bucket-region` header) -- `go get` reported that
  module deprecated in favor of `feature/s3/transfermanager`, and
  `GetBucketLocation` alone, already available via the `service/s3`
  package already in use, needs no new dependency at all.

**Consequences.** `awsclient.S3API` gains `GetBucketLocation` alongside
`HeadObject`/`HeadBucket` (real client, logging decorator, and test fake
all updated). `BackupArchiveAndTrim`'s signature grows a
`newS3Client` parameter; `main.go` supplies both the initial
`cfg.Regions[0]`-scoped probe client and the factory closure.

---

