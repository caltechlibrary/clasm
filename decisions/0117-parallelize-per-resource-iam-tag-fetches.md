---
id: "0117"
title: "Parallelize per-resource IAM tag fetches"
date: "2026-07-23"
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
uuid: "2032f1ce-989a-4088-a577-ec6d17f59322"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via live usage while testing Phase 20.37 (Tag
Management's IAM extension): "Manage tags -> IAM Role" took several
seconds just to open the resource picker, in an account with dozens of
roles (several AWS-service-linked, e.g. SageMaker/CloudTrail roles).
Root cause: `ListIAMRoleSummaries`/`ListIAMInstanceProfileSummaries`/
`ListIAMPolicySummaries` (Phase 20.36) fetch each resource's tags one at
a time, sequentially, via `ListRoleTags`/`ListInstanceProfileTags`/
`ListPolicyTags` (required per-resource calls -- see "ListRoles/
ListInstanceProfiles/ListPolicies don't return tags inline," above) --
with N roles/profiles/policies, that's N sequential network round-trips
before any of Show Roles, Show Instance Profiles, Show Policies, Manage
Tags, or Show All Tags can render anything.

**Decision.** Parallelize the per-resource tag fetch with a bounded
worker pool (new `fetchTagsConcurrently` in `internal/inventory/iam.go`,
capped at `iamTagFetchConcurrency = 10` in flight at once), mirroring
`inventory.ListImages`' own concurrent per-region fan-out pattern
(`images.go`) -- generalized here to fan out over resource index rather
than region. All three `ListIAM*Summaries` functions now call this
shared helper instead of looping sequentially.

**Rejected alternative.** *Unbounded concurrency* (fire all N requests
at once, no cap) -- rejected: an account can plausibly have 100+ roles
between service-linked roles and Lambda/SageMaker-created ones (observed
directly in the account this was tested against), and firing that many
concurrent IAM API calls risks throttling. 10 in flight at once is a
conservative, not-tuned starting value -- worth revisiting if it proves
too slow or too aggressive in practice.

**Consequences.** Wall-clock time drops from roughly N x per-call
latency to roughly (N / 10) x per-call latency (plus the fixed cost of
the initial `ListRoles`/`ListInstanceProfiles`/`ListPolicies` call) --
not measured precisely, but expected to turn a multi-second delay into
something close to imperceptible for typical account sizes. Error
handling changes subtly: with sequential fetches, the *first* resource
(in `ListRoles`' own return order) to error would stop the loop
immediately; with concurrent fetches, the first error *observed* (not
necessarily the first resource in list order) is returned, only after
every in-flight fetch has completed -- behaviorally equivalent for
callers (an error is an error), but worth noting if debug-log call
ordering ever looks unexpected. All existing correctness tests
(Origin resolution, sorting, tag-map retention, error propagation) were
kept green through the refactor, and `go test -race` confirms no data
race was introduced.

---

