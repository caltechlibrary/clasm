---
id: "0126"
title: "Modify Launch Template Size: one combined action, unfiltered instance-type picker, shrink to the AMI's snapshot floor"
date: "2026-07-28"
status: accepted
kind: decision
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "20fbddae-5173-4414-b102-f1f93a01ec2d"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md requested feature: modify a launch template's
instance type and EBS root volume size from clasm, not just sync
cloud-init. See DESIGN.md, "Modify Launch Template Size", PLAN.md
Phase 20.46.

**Decision 1 -- one combined action, not two.** User's explicit call:
a single "Modify launch template's instance type / EBS root volume
size" entry prompts for both and creates one new launch template
version with both overrides, mirroring how creation already collects
both together and how Sync creates exactly one new version per run.

**Decision 2 -- instance-type picker stays unfiltered; architecture
mismatches are handled by offering to swap the AMI, not by filtering
the picker.** Initially designed as simply unfiltered (Phase 20.35's
launch-time picker filters by the *picked* AMI's architecture, which
doesn't apply here since there's no "picked AMI" yet, only the
template's *existing* one). While confirming scope, the user gave a
concrete real-world case that changed this: `granian-rdm-v14-test`
currently launches a Graviton (arm64) instance, and they want to
switch it to an x86_64 type. Filtering the instance-type picker by the
template's *current* AMI architecture would have made that impossible
-- switching architecture families is the actual use case, not an
error to prevent. Resolved instead with a post-hoc check: after the
(still unfiltered) instance-type pick, a new `instanceTypeArchitecture`
helper (`ec2:DescribeInstanceTypes`, mirroring
`instanceTypeRequiresENA`'s exact shape) reports the picked type's
required architecture; on a mismatch against the current AMI's
architecture (`describeImageRootVolume`, widened to also return it --
same `DescribeImages` response already being read, no extra call), the
operator is prompted to pick a new base AMI, filtered to the target
architecture and the template's own region. This is *why* AWS's
architecture constraint isn't caught at `CreateLaunchTemplateVersion`
time at all -- only `RunInstances` validates instance-type/AMI
architecture compatibility, so without this check the narrower
original design would have silently produced a new version that fails
only when someone actually launches from it. The new version's
`RequestLaunchTemplateData` now always explicitly sets `ImageId`
(previously going to be UserData/InstanceType/BlockDeviceMappings
only, inheriting `ImageId` via `SourceVersion`) -- harmless when
unchanged, required when the AMI swap fires. The conditional AMI
re-pick (`pickImage`, a real bubbletea program, not pipe-testable) is
invoked mid-sequence rather than hoisted to the untestable entry point
the way every other Picker-tier call in this package is, so it needs
its own substitutable seam -- `pickNewLaunchTemplateAMIFunc`, the same
shape `backup_archive.go`'s `promptBackupBucketFunc` already
established for exactly this "conditional, hard-to-drive step embedded
inside an otherwise pipe-testable sequence" case.

**Decision 3 -- shrinking the EBS root volume is allowed down to the
AMI's own snapshot size, not floored at the template's current
setting.** User's explicit call. AWS's only real constraint on a
launch template's `BlockDeviceMappings.Ebs.VolumeSize` is never
smaller than the source snapshot -- the same floor
`promptRootVolumeSizeGB` already enforces at creation time. Unlike
`ResizeInstanceRootVolume` (which really can only grow, an AWS
API-level restriction on `ec2:ModifyVolume` for an already-attached
volume), a launch template's stored size has no such limit.
`promptRootVolumeSizeGB` widens from one `defaultGB` parameter (used
for both the pre-filled display value and the validation floor --
identical at creation time) to two: `displayDefaultGB` (the version's
current override, or the AMI default if never set) and `floorGB`
(always the AMI's own snapshot size). The two existing creation-time
call sites pass the same value for both, unchanged behavior.

**Rejected alternative.** *Floor shrinking at the template's current
setting* (simpler "can only grow" mental model, matching
`ResizeInstanceRootVolume`'s own restriction) -- rejected because that
restriction exists there for a different, AWS-API-level reason (a live
attached volume genuinely cannot shrink) that doesn't apply to a launch
template's stored, not-yet-attached size; enforcing it here anyway
would block a legitimate operation (shrinking an over-provisioned
template back down) for no real reason.

**Not decided, raised but out of scope.** Whether to add `m7i`/other
non-curated instance-type families to `curatedInstanceTypes` -- "Other"
already covers any exact value; a separate, smaller follow-up if
wanted.

---

