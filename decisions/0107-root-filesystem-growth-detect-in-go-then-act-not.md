---
id: "0107"
title: "Root filesystem growth: detect-in-Go then act, not a self-detecting bash script"
date: "2026-07-21"
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
uuid: "fb0566e1-d6eb-4d1c-a46f-4c49ba530952"
origin_host: "MACMINI-RD.local"
---

**Context.** Implementing Part 2 of "Configurable EBS root volume
size" (PLAN.md Phase 20.31): once `ec2:ModifyVolume` grows the EBS
volume itself, the OS-level partition and filesystem still need to
grow to use the extra space (`growpart` + `resize2fs`/`xfs_growfs`) --
the exact manual step the operator had to do by hand for the
production incident this phase closes. The open design question was
*where* the "is this a layout we can safely automate, or should we
back off" decision gets made: entirely inside one bash script sent via
SSM, or split into a detect step (bash) plus a decide step (Go).

**Decision.** Two separate SSM round-trips, both through the existing
`WaitForSSMOnline`/`RunShellCommand` primitives (`ssm.go`, already used
by `checkCloudInitCompletion` for the cloud-init-status check -- no new
SSM plumbing needed). First, `findmnt -no SOURCE,FSTYPE /` reports the
root partition's device path and filesystem type; its output is parsed
in Go (`splitDiskAndPartition`, `parseFindmntOutput`, `ssm_grow.go`),
not in bash. Only if that parse succeeds -- a single partition directly
on a whole disk, NVMe- or Xen/legacy-named, ext2/3/4 or xfs -- does a
second command actually run `growpart`/`resize2fs`/`xfs_growfs`.
Anything else (an LVM logical volume such as
`/dev/mapper/ubuntu--vg-ubuntu--lv`, a device-mapper node, an
unsupported filesystem) falls back to printing the same manual
commands the operator already ran by hand, rather than growing
anything. Rationale: PLAN.md's own work-item language for this phase
called for "fixture-driven unit tests for the `findmnt`-output-parsing
logic, independent of any live SSM round-trip" -- only achievable if
that parsing is a plain Go function, not logic buried inside a bash
string this project's tests can't introspect. It also keeps the
one genuinely destructive step (the actual `growpart` call) gated
behind Go-side validation of the parsed device, rather than trusting a
single monolithic script to both detect its own layout and correctly
bail out if it doesn't recognize it -- "fail loud, don't guess" applies
to the detection logic itself, not just its outcome.

**Bug caught by this phase's own tests.** `growRootFilesystem` and
`resizeInstanceRootVolume` initially hardcoded the package's production
`Default*` SSM timeouts (2 minutes online, 10 minutes per command)
directly, rather than taking them as parameters the way
`checkCloudInitCompletion` already does. The first test run of
`TestGrowRootFilesystem_SSMNotOnline_PrintsManualInstructions` actually
took 120 real seconds to pass -- it was genuinely waiting out the
production timeout, not simulating it. Fixed by threading
`onlineTimeout`/`commandTimeout`/`pollInterval` through
`growRootFilesystem` explicitly (production call site in
`resize_volume.go` passes the real `Default*` constants; tests pass
millisecond-scale ones), and by configuring
`resizeInstanceRootVolume`'s own end-to-end test's fake SSM client to
resolve immediately rather than shrinking the timeout further --
matching `checkCloudInitCompletion`'s existing shape, which this phase
should have followed from the start.

---

