---
id: "0037"
title: "Tolerate DescribeInstances' post-RunInstances eventual-consistency window"
date: "2026-07-02"
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
uuid: "de5961ed-65c4-48c5-b843-6f4d2d778990"
origin_host: "MACMINI-RD.local"
---

**Context.** A real launch (subnet/instance-type mismatch resolved,
confirmed, `RunInstances` succeeded) immediately failed anyway: `Launched
i-088ab06fb0c16eb0b, waiting for it to reach running... Error: AWS error
[InvalidInstanceID.NotFound]: The instance ID 'i-088ab06fb0c16eb0b' does
not exist`. This is documented AWS behavior, not a real failure: a newly
launched instance ID can be briefly invisible to `ec2:DescribeInstances`
for a few seconds after `ec2:RunInstances` returns it, before the
instance is fully registered. `waitUntilState` (backing `WaitUntilRunning`,
used by every launch and by Start Instance) treated *any*
`DescribeInstances` error as fatal, so this blocked every single launch
that happened to hit the window -- not an edge case, a near-certain
race every time.

**Decision.** `waitUntilState` now tolerates AWS's own
`InvalidInstanceID.NotFound` the same way it already tolerates "not in
the wanted state yet" -- keep polling instead of returning the error.
Any other `DescribeInstances` error still fails immediately, unchanged.
Found and fixed the identical exposure on the AMI side while here:
`WaitForAMIAvailable` could hit the equivalent `InvalidAMIID.NotFound`
right after `ec2:CreateImage` returns, before `ec2:DescribeImages`
recognizes the new image -- same tolerance added there
(`isImageNotYetVisible`), even though it hadn't been reported yet, since
it's the exact same class of bug on the exact same code path shape.

**Rationale.** This is a well-known, documented AWS eventual-consistency
behavior (not specific to this account or these instances) -- the fix is
to expect it, not work around it operationally (e.g. "just retry the
whole launch"). Fixing the AMI-side analog preemptively, rather than
waiting for a second bug report, matches this session's pattern of
fixing the failure *class*, not just the exact reported instance of it.

**Rejected alternatives.**
- *Retry the whole launch flow on this error* -- rejected: the instance
  already launched successfully; retrying `RunInstances` would create a
  second, redundant instance instead of just waiting a few more seconds
  for the first one to become visible.
- *A fixed short sleep before the first `DescribeInstances` call* --
  rejected in favor of tolerating the specific error code during normal
  polling: simpler, no new timing constant to tune, and self-correcting
  regardless of how long the window actually is.

**Consequences.**
- `internal/workflow/launch_execute.go`: `isInstanceNotYetVisible`.
- `internal/workflow/create_ami_execute.go`: `isImageNotYetVisible`.
- No new AWS permissions -- purely client-side error handling around
  calls this tool already makes.

---

