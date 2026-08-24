---
id: "0116"
title: "Real bug: ListRoles/ListInstanceProfiles/ListPolicies don't return tags inline"
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
uuid: "246f5345-a163-4076-82c4-5aee1911b26a"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via live usage, not a test: the user tagged the real
`air-sampling` role `Origin=DLD` (values actually cased `origin`/`dld`,
matching their own `~/.clasm` `origin_tag` config exactly) from the AWS
Console, restarted clasm, and Show Roles still displayed `(unset)`.
Config loading itself was verified correct in isolation (a throwaway
`go run` confirmed `config.Load` resolved `OriginTag.Key="origin"`,
`OriginTag.DLDValue="dld"` from `~/.clasm` exactly as expected) --
ruling out the config layer entirely. The actual cause: Phase 20.36's
design (DESIGN.md, "IAM Profile & Role Management Domain") assumed
`iam:ListRoles`/`iam:ListInstanceProfiles`/`iam:ListPolicies` return each
resource's `Tags` inline, based on the vendored SDK's `Role`/
`InstanceProfile`/`Policy` response structs all declaring a `Tags []Tag`
field. Confirmed live against the real account that this assumption was
wrong for all three: `aws iam list-roles`, `aws iam
list-instance-profiles`, and `aws iam list-policies --scope Local` all
omit `Tags` entirely from their JSON output, even for resources
confirmed (via `aws iam list-role-tags`) to actually have tags. The SDK's
shared `Tags` field is populated by other operations (`GetRole`,
`CreateRole`, etc.), not these three List calls -- reusing one response
struct across multiple API operations doesn't mean every operation
populates every field.

**This is the same class of mistake this project has hit before**
(DECISIONS.md, "Offer official Ubuntu LTS AMIs..." -- getting AMI name
patterns wrong when not checked against real AWS) and the ARM64 addendum
explicitly checked live *because* of that history. This time the
equivalent live check wasn't done before writing the design -- worth
re-flagging as a standing lesson: an SDK type's field existing is not
evidence a *specific operation* populates it; when a design leans on a
list response including something beyond the obviously-basic fields
(here, tags), verify against a real, already-tagged resource before
writing the design, not just by reading vendored struct definitions.

**Decision.** Add a per-resource tag fetch after each list call: new
`IAMAPI` methods `ListRoleTags`, `ListInstanceProfileTags`,
`ListPolicyTags` (mirrored into `logging_iam.go`), called once per
role/profile/policy returned by `ListRoles`/`ListInstanceProfiles`/
`ListPolicies` respectively, inside `internal/inventory/iam.go`'s three
`ListIAM*Summaries` functions. Every existing "resolves Origin" test in
`internal/inventory/iam_test.go` was rewritten first to supply tags via
the fake's new per-name/per-ARN tag maps (matching the real API shape)
instead of via the list-response structs' `Tags` field, confirmed
failing against the pre-fix code, then the fix made them pass -- per
[[feedback-test-before-fix]].

**Rejected alternative.** *Keep relying on the list response's `Tags`
field* -- not actually an alternative, just the bug; there was never a
real code path where this could have worked, since AWS itself doesn't
return the data.

**Consequences.** Discovery for each of Roles/Instance Profiles/Policies
now costs N+1 IAM calls (one list + one tag-fetch per resource) instead
of 1 -- accepted, since IAM is a low-volume, non-rate-limited-in-practice
control-plane API for an account this size, and there's no way to get
per-resource tags in fewer calls via these APIs. `iamTagsToMap` and
`ResolveOrigin`/`IsDLDOwned` themselves needed no changes -- the bug was
entirely in what was fed to them, not in the tag-matching logic itself.

---

