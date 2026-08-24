---
id: "0063"
title: "Clear a bucket's lifecycle configuration via DeleteBucketLifecycle, not an empty PutBucketLifecycleConfiguration"
date: "2026-07-08"
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
uuid: "28f724ae-e4ed-485f-b3db-6fd41b4e0cc7"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS manual verification of Phase 20's Manage Bucket
Lifecycle Policies feature (immediately after implementation) surfaced
two real bugs the unit tests' fakes couldn't catch, since fakes don't
enforce AWS's own validation rules:

1. Setting a guided backup policy with expiration (30 days) shorter than
   its transition (90 days) fails with a real AWS error:
   `'Days' in the Expiration action ... must be greater than 'Days' in
   the Transition action`. This is a genuine AWS API constraint (an
   object must transition to cheaper storage before it can expire, not
   after) that neither the guided flow's two independent prompts nor
   this decision log's earlier design round anticipated.
2. Removing a bucket's *last* remaining lifecycle rule failed outright:
   `PutBucketLifecycleConfiguration` requires a non-empty `Rules` field
   (enforced client-side by the AWS SDK before any network call), so
   `bucket_lifecycle.go`'s fetch-modify-PutBack pattern (see the 21.1
   entry below) breaks down exactly when the "modify" step empties the
   rule set.

**Decision.** Fixed #2 (the one blocking Remove entirely, not just a
specific input combination): added `DeleteBucketLifecycle` to `S3API`
and its logging wrapper, and `ManageBucketLifecyclePolicies` now branches
on the modified rule set's length -- empty calls `DeleteBucketLifecycle`
instead of `PutBucketLifecycleConfiguration`. Per this project's
test-before-fix practice, a failing test
(`TestManageBucketLifecyclePolicies_RemovingLastRuleCallsDeleteNotPutWithEmptyRules`)
reproduced the bug against the pre-fix code before the fix was written,
then re-verified against real AWS (create a rule, remove it, confirm
`GetBucketLifecycleConfiguration` reports `NoSuchLifecycleConfiguration`
afterward).

#1 (the expiration/transition ordering constraint) was **not** given
local validation in this pass -- worked around during verification by
choosing valid test values (transition < expiration), and left as a
known gap: the guided flow currently surfaces AWS's own error message
verbatim rather than catching the mismatch before calling AWS. A future
pass should add a local check (transition days, when both are set, must
be less than expiration days) mirroring `validateBucketName`'s
locally-caught-before-AWS pattern from Create Bucket.

**Consequences.** `S3API` gains a thirteenth method,
`DeleteBucketLifecycle`, alongside the twelve added for Phase 20.
`internal/workflow/bucket_fakes_test.go`'s shared fake gained matching
fields/methods. The Add/Edit guided-flow's real-world expiration-vs-
transition ordering constraint remains an open, documented gap (see
TODO.md).

---

