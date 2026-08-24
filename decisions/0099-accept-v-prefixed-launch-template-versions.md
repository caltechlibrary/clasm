---
id: "0099"
title: "Accept \"v\"-prefixed launch template versions"
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
uuid: "e4a9becd-22ee-4139-b3ed-c2bbde6b6af0"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via the debug log from the operator's first
real-AWS pass over Phase 20.27
(`clasm-debug-20260720-132204.jsonl`): typing `v1` at a version prompt
-- a natural thing to type, since `launchTemplateLabel`'s own display
format is "default v2" -- caused a hard AWS rejection at two call
sites: `DescribeLaunchTemplateVersions` ("Invalid launch template
version: either '$Default', '$Latest', or a numeric version are
allowed") and `ModifyLaunchTemplate` ("A launch template version must
be specified..."). The latter is why Promote appeared to silently do
nothing -- it had actually failed outright, not succeeded-without-
refreshing as first suspected.

**Decision.** New `normalizeVersionSelector(s string) string` strips a
leading `v`/`V` from a plain version number (`"v1"` -> `"1"`) before it
reaches any AWS call. `"$Default"`/`"$Latest"` and anything not of the
exact form `v<digits>` pass through unchanged. Applied at all four
places a version selector is entered: the shared
`promptLaunchTemplateVersion`/`promptLaunchTemplateVersionLabeled`
(Show, Create-from-template, Sync's compare-against version, Show's
two version-diff prompts), Promote's version prompt, and Delete
Version(s)'s comma-separated list.

**Rationale.**
- The mismatch is self-inflicted: this project's own display convention
  ("v2", "v3" in `launchTemplateLabel`) primes the operator to type
  what they see elsewhere in the same tool, and AWS's API has no
  tolerance for that format at all -- normalizing at the boundary is
  more correct than asking every operator to remember an
  AWS-vs-clasm formatting distinction.
- A single shared normalization function, applied everywhere a version
  selector is entered, avoids fixing the same bug three more times as
  new version-entry points get added later.

**Rejected alternatives.**
- *Change `launchTemplateLabel`'s display format instead* (drop the
  "v" prefix) -- fixes the display/input mismatch from the other
  direction, but "v2"/"v3" is a reasonable, common way to label
  versions for a human reader; normalizing the input is less invasive
  than changing an already-shipped display convention.
- *Validate and reject `"v1"` with a clear error message, re-prompt* --
  considered, but silently accepting the obviously-intended value is
  friendlier than making the operator retype it correctly, and there's
  no ambiguity in what `v<digits>` means.

**Consequences.** `internal/workflow/show_launch_template.go` gains
`normalizeVersionSelector`; `launch_template_manage.go`'s Promote and
Delete Version(s) prompts both call it. Test-first: reproduced the
exact `"v1"` -> AWS-rejects-it failure before fixing it. See `PLAN.md`
Phase 20.28.

---

