---
id: "0104"
title: "Generalize applyOneTagChange for S3's read-modify-write tag semantics"
date: "2026-07-20"
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
uuid: "aa6f31a6-e685-451f-beab-2615db303f87"
origin_host: "MACMINI-RD.local"
---

**Context.** With the four EC2-backed kinds of Tag Management done and
confirmed against real AWS, S3 Bucket was the one remaining kind (see
DECISIONS.md, "Tag Management: a fourth domain...", which anticipated
this exact generalization but deliberately deferred it until a real
second apply-shape existed -- PLAN.md Phase 20.30's "Design note").
`s3:PutBucketTagging` replaces a bucket's *entire* tag set and has no
fine-grained add/remove-one-tag call the way EC2's
`CreateTags`/`DeleteTags` do, so S3 needed a genuinely different apply
path, not just new fetch/wiring like Launch Template/Key Pair did.

**Decision.** `applyOneTagChange`/`manageTagsForResource`
(`manage_tags.go`) now take a `tagApplyFunc` (`func(ctx
context.Context, params TagChangeParams) error`) instead of a
hardcoded `awsclient.EC2API` client. Every existing call site
(Compute's `manageTags`, and `manageResourceTags`'s four EC2-backed
cases) builds `func(ctx, params) error { return ApplyTagChange(ctx,
client, params) }` once, at the point `client` is already resolved --
mechanically identical behavior, just the client wrapped in a closure
instead of passed raw. New `internal/workflow/bucket_tags.go` adds the
S3 side: `fetchBucketTags` (a full-tag-set `GetBucketTagging`, treating
`NoSuchTagSet` as empty, same convention as `bucketPurpose`) and
`applyBucketTagChange` (the S3 `tagApplyFunc`) -- fetch current tags,
apply the one collected Add/Update/Remove locally, then write the
whole set back. If that leaves zero tags (removing the bucket's last
one), it calls the newly-added `s3:DeleteBucketTagging` instead of
`PutBucketTagging` with an empty `TagSet` -- proactively matching
`ManageBucketLifecyclePolicies`' own `DeleteBucketLifecycle` precedent
for the same "replace the whole set" operation shape (real-AWS
verification there found `PutBucketLifecycleConfiguration` rejects an
empty `Rules` list client-side). Checked (not assumed) whether the same
applies to `PutBucketTaggingInput`: the SDK's generated
`validateOpPutBucketTaggingInput`/`validateTagging` only require
`TagSet` to be non-nil, not non-empty, so an empty-but-non-nil
`PutBucketTagging` call might in fact succeed client-side (and
possibly server-side too) -- `DeleteBucketTagging` was still chosen
out of caution, matching the established precedent, since this hasn't
been confirmed against real AWS either way yet.

**Rationale.** Generalizing only when a real second apply-shape
appeared (S3) rather than up front (when the EC2-backed slice was
built) avoided speculative complexity in Phase 20.30's first slice --
every EC2-backed call site needed no behavior change at all, just a
closure wrapper, confirming the deferral was the right call. Rejecting
`PutBucketTagging` with an empty set (in favor of
`DeleteBucketTagging`) even though the client-side validator would
accept it errs toward the same caution the lifecycle-rules case
already established, rather than assuming AWS's server-side behavior
matches what the client-side SDK validator merely permits.

**Rejected alternatives.**
- *A second, parallel tag-editing workflow just for S3* -- rejected:
  would duplicate `manageTagsForResource`'s entire loop/action-picker/
  confirm/Show-tags shape for no reason, the exact outcome the
  pluggable-apply-closure generalization was designed to avoid.
- *Skip `DeleteBucketTagging` and always call `PutBucketTagging`,
  since the client-side validator accepts an empty `TagSet`* --
  rejected: client-side acceptance doesn't confirm server-side
  acceptance, and the lifecycle-rules case already showed AWS can
  reject an empty "whole set" write that the SDK itself doesn't block
  locally; safer to match that precedent than assume this operation
  differs, pending real-AWS confirmation.

**Consequences.** `manage_tags_test.go` gained a small `ec2Apply(client)`
test helper so existing direct calls to
`applyOneTagChange`/`manageTagsForResource` keep working unchanged.
New `statefulTagsFakeS3Client` (`bucket_tags_test.go`, mirroring
`statefulTagsFakeEC2Client`) proves the S3 read-modify-write round
trip and the `DeleteBucketTagging`-on-empty branch specifically.
`awsclient.S3API` gained `DeleteBucketTagging` (+ logging wrapper +
shared `fakeS3Client` method). See PLAN.md Phase 20.30, "Work Items
(S3 Bucket)".

---

