---
id: "0154"
title: "Real bug: `listRoles`/`listInstanceProfiles` un-paginated, sibling to a fix already made elsewhere"
date: "2026-08-12"
status: accepted
kind: correction
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: ["0120"]
initiative: ""
session: ""
decisions: []
tags: []
uuid: "64b02056-db1d-4f48-a4a6-7033803ec6eb"
origin_host: "MACMINI-RD.local"
---

**Context.** Live-retrofitting the OpenSearch `path.repo` fix (this time
on CaltechDATA dev, following the safe persistent-volume migration in
`rdm-opensearch-path-repo-retrofit.md`) required a new IAM role
(`rdm-opensearch-backup`, RDM Repository Instance template) with S3
access, then attaching it to the running instance via "Associate/replace
IAM instance profile." The role was confirmed created and SSM-capable
via the IAM domain's "Show Roles" screen, but did not appear at all in
"Select a role to attach" (the picker `createInstanceProfileInteractive`
shows when creating a new instance profile from that same flow) --
only one unrelated, older role showed up. Root cause:
`internal/workflow/resource_lists.go`'s `listRoles`/`listInstanceProfiles`
each make a single, un-paginated `iam.ListRoles`/`iam.ListInstanceProfiles`
call, silently capped at IAM's page size. This is the exact same
truncation `internal/inventory/iam.go`'s `listAllRoles`/
`listAllInstanceProfiles` were already fixed for on 2026-07-23 (see
"ListRoles/ListInstanceProfiles/ListPolicies don't return tags inline,"
same date) -- but that fix only landed in the IAM domain's own discovery
path ("Show Roles"/"Show Instance Profiles"), never in this sibling
helper used by the instance-launch and associate/replace-profile
pickers. IAM doesn't sort `ListRoles` by creation date, so a
newly-created role can land on any page, including one never fetched.

**Decision.** `resource_lists.go`'s `listRoles` and `listInstanceProfiles`
now loop on `IsTruncated`/`Marker`, identical shape to
`inventory.listAllRoles`/`listAllInstanceProfiles`.

**Rationale.**
- Same bug, same fix, just a second call site that was missed the first
  time -- no new design question, just applying the existing pattern
  consistently.
- Reproduced test-first: `fakeIAMClient.ListRoles`/`ListInstanceProfiles`
  gained the same two-page Marker fixture shape as the inventory
  package's fake, and a new test per function confirmed the truncation
  before the fix, then passed after.

**Rejected alternatives.**
- *Fix only the specific call site hit today (`listRoles`)* -- rejected;
  `listInstanceProfiles` has the identical bug shape and would have
  resurfaced the same incident the next time an account accumulates
  enough instance profiles.

**Consequences.**
- `internal/workflow/resource_lists.go`: both functions now paginate.
- `internal/workflow/create_instance_profile_test.go`: `fakeIAMClient`
  gained `rolesPage2`/`instanceProfilesPage2` fixture fields.
- `internal/workflow/resource_lists_test.go`: two new regression tests,
  `TestListRoles_PaginatesAcrossMultiplePages`/
  `TestListInstanceProfiles_PaginatesAcrossMultiplePages`.
- `go build`/`vet`/`test -race`/`gofmt` clean throughout (pre-existing
  `gofmt -l` flag on `version.go` unrelated, predates this change).
- No `listPolicies` equivalent exists in `resource_lists.go` today (no
  picker currently offers a "pick any policy in the account" flow from
  this file) -- if one is added later, apply the same pagination from
  the start rather than repeating this gap a third time.

---

