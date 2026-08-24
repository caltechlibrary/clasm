---
id: "0035"
title: "Pre-flight check: instance type vs. AMI ENA support"
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
uuid: "65aea77c-ecb1-49af-9970-ba679da55d3d"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing hit `AWS error [InvalidParameterCombination]:
Enhanced networking with the Elastic Network Adapter (ENA) is required
for the 't3.small' instance type. Ensure that you are using an AMI that
is enabled for ENA.` -- this is the ENA pre-flight check idea already
queued in TODO.md since an earlier session (two real launch failures
that day: AMI `ami-0da49db6a772dda02` isn't ENA-enabled, both `t3.micro`
and now `t3.small` require it). With the AZ pre-flight check (above)
just implemented as a template, this was the natural next failure class
to close.

**Decision.**
- `inventory.Image` gained an `EnaSupport bool` field, populated for
  free from the same `ec2:DescribeImages` call `ListImages` already
  makes (the SDK's `Image.EnaSupport` field) -- no extra AWS call for
  the AMI side.
- After Instance type is picked (Feature 2/3), check whether it
  requires ENA (`ec2:DescribeInstanceTypes`,
  `NetworkInfo.EnaSupport == Required`) and, if so, whether the
  already-picked AMI supports it. If not, print the incompatibility and
  show a pick list: **Change instance type** or **Abort this launch** --
  no "pick a different AMI" option, unlike the AZ check's "pick a
  different subnet": swapping the AMI this late would mean redoing
  earlier choices that depend on it (e.g. the Project tag default), so
  aborting and restarting covers that case instead, same as any other
  declined confirmation.
- "Abort" reuses `ui.ErrCancelled`, same as the AZ check.
- "Change instance type" reuses `promptInstanceType` (the curated pick
  list, below), not a bespoke free-text prompt -- for consistency, both
  pre-flight checks' recovery flows now go through the same instance-
  type entry point.

**Rationale.**
- Closes the exact TODO.md item this team already flagged, using the
  same pick-list-recovery pattern just established for the AZ check
  rather than inventing a third UX shape.
- Getting the AMI's `EnaSupport` for free from data already fetched
  avoids adding a new per-check AWS call on the AMI side.

**Rejected alternatives.**
- *Also offer "pick a different AMI"* -- rejected because the AMI's
  already-collected downstream effects (Project tag default, region-
  scoped clients) would need to be redone; abort-and-restart is simpler
  and matches this tool's existing cancellation semantics.
- *A shared multi-check framework covering both AZ and ENA together* --
  still rejected for now, per the AZ check's own decision above: two
  checks doesn't justify an abstraction yet.

**Consequences.**
- New EC2 permission required: `ec2:DescribeInstanceTypes` (see
  `DESIGN.md`, "Assumptions").
- `internal/workflow/instance_type_ena_check.go` (new):
  `instanceTypeRequiresENA`, `ensureInstanceTypeENACompatible`,
  `enaIncompatibilityChoices` (reuses `incompatibilityChoice`/
  `incompatibilityChoiceLabel` from the AZ check).

---

