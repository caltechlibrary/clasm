---
id: "0105"
title: "Configurable EBS root volume size: scope, flow coverage, and resize automation depth"
date: "2026-07-21"
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
uuid: "4841c335-c8bb-4ec9-bcfc-aececd9b9a0d"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md's "Bug (confirmed in production use, 2026-07-22)"
entry: a launch template built for a 250GB InvenioRDM comparison
instance instead produced an 8GB root volume (the stock Ubuntu 24.04
AMI default), because neither `RunInstances` (`launch_execute.go`) nor
`CreateLaunchTemplate` (`launch_template_create.go`) has ever set
`BlockDeviceMappings` -- the Launch Templates addendum
(DESIGN.md, 2026-07-20) explicitly deferred the entire block-device-
mapping surface as out of scope for the curated field set. This
reopens that specific piece of it. Three scope questions were put to
the operator directly rather than assumed, since each has a real
implementation-cost/flexibility trade-off:

**Decision 1 -- root volume size only, not a general block-device-
mapping editor.** The confirmed real need is "the default is too
small, I sometimes need 250-500GB," not additional data volumes or
per-volume type/IOPS control. Matches the project's existing curated-
field-set restraint (Launch Templates addendum) rather than reopening
the full AWS struct.

**Decision 2 -- every instance-creation flow and template creation, not
just templates.** `collectLaunchInstanceParams` and
`collectLaunchInstanceParamsFromCloudInit` are the two shared
parameter-collection cores behind Feature 2 (AMI), Feature 3
(cloud-init), and Create Launch Template from Cloud-Init YAML (which
already reuses the cloud-init core, per DESIGN.md's Launch Templates
addendum) -- one change to each core covers all three creation paths
in one pass, rather than fixing only the path that happened to trigger
the production incident and leaving the other two with the same latent
gap. Create EC2 Instance from Launch Template stays untouched: the
template already bakes in its own size, and this project already
resolved (DESIGN.md, "Launch Templates," decision A3) that this path is
"just another way to create an instance," not a hybrid template-plus-
override wizard -- reopening that here would undo a settled decision.

**Decision 3 -- automate the OS-side growth via SSM, not just the AWS-
side `ModifyVolume` call.** The operator's own real-world workaround
for the production incident was exactly `aws ec2 modify-volume` +
manual `growpart`/`resize2fs` over SSH -- automating both halves closes
the gap the same way the operator already closed it by hand, rather
than leaving half the workaround still manual. Accepted trade-off: this
is clasm's first workflow that executes shell commands inside a live
instance's OS (every prior use of `SSMAPI.SendCommand` only *checks*
cloud-init status, never changes instance state), so the design commits
to "detect a supported single-partition layout and act, or abort with
the same manual instructions rather than guess" for any layout the
`findmnt`/`lsblk` probe doesn't recognize (e.g. LVM) -- see DESIGN.md,
"Configurable EBS Root Volume Size," Part 2.

---

