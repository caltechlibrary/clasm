---
id: "0120"
title: "Real bug: ListRoles/ListInstanceProfiles/ListPolicies silently truncate past 100 items"
date: "2026-07-23"
status: accepted
kind: correction
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
uuid: "ecd9c337-808d-45f3-a69c-b0c5bd64a80b"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via live use immediately after Phase 20.40 shipped:
walking through the Delete Role steps to clean up three real test roles
(`test-rdm-repo-role`, `test-static-site-role`, `test-static-site-role-2`),
the operator's Delete Role picker showed only 2 roles
(`ec2-granian-test-role`, `air-sampling`) instead of 5, even though `aws
iam list-role-tags` confirmed all three missing roles were correctly
tagged `origin=dld`. Root-caused by comparing `aws iam list-roles`
(which auto-paginates by default) against `aws iam list-roles
--no-paginate`: the account has 121 roles total, IAM truncates
`ListRoles` at 100 items per page (`IsTruncated: true`), and the three
missing test roles simply weren't on that first page. `ListIAMRoleSummaries`
(`internal/inventory/iam.go`) made a single, un-paginated `ListRoles`
call and never checked `IsTruncated`/`Marker` at all -- so any account
with more than 100 roles was silently missing some, with no error or
warning. `ListIAMInstanceProfileSummaries`/`ListIAMPolicySummaries` had
the identical gap, just not yet triggered (this account currently has
only 4 instance profiles and 59 local policies, both under IAM's default
page size).

**Decision.** Added `listAllRoles`/`listAllInstanceProfiles`/
`listAllPolicies` (`internal/inventory/iam.go`), each looping on
`Marker` until `IsTruncated` is false, and switched all three
`ListIAM*Summaries` functions to use them instead of a single call.
Fixed all three list calls, not just Roles, even though only Roles is
truncated in this account today -- the same silent-omission bug would
otherwise resurface unannounced the moment instance profiles or local
policies cross IAM's page-size threshold, and the fix is the same
handful of lines in each case. Test-first: `fakeIAMClient` (in
`internal/inventory/iam_test.go`) gained a `*Page2` fixture field per
list kind so a test can simulate a truncated first page + a
`Marker`-keyed second page, and a `TestListIAM*Summaries_
PaginatesAcrossMultiplePages` test per kind confirmed failing (only the
first page's item returned) before the fix, then passing after.

**Follow-on bug, found verifying the fix against real AWS:** once
discovery correctly fetched all 121 of this account's roles instead of
silently dropping to the first 100, the resulting ~121 concurrent
`ListRoleTags` calls (`fetchTagsConcurrently`, Phase 20.37's own
bounded-concurrency fix) reliably triggered `Throttling: Rate exceeded`
past the AWS SDK's default 3 retry attempts -- reproduced twice in a
row via a throwaway verification program. Fixed two ways: lowered
`iamTagFetchConcurrency` from 10 to 4 (a smaller burst against IAM's
not-fully-documented per-account rate quota) and raised
`awsclient.NewIAMClient`'s retry ceiling to 8 attempts via
`config.WithRetryMaxAttempts(8)` -- applied only to the IAM client,
since its N+1-calls-per-list shape (one `List*` plus one `List*Tags` per
resource) makes it uniquely prone to bursty throttling that this
codebase's EC2/S3 clients never hit. Re-verified against real AWS after
both changes: all 121 roles fetched, all 5 DLD-owned roles (including
the three test roles that originally prompted this whole investigation)
correctly recognized, no throttling.

---

