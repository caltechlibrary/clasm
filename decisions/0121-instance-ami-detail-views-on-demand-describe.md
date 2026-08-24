---
id: "0121"
title: "Instance/AMI Detail Views: on-demand describe calls, appended menu placement"
date: "2026-07-24"
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
uuid: "32ce97f6-bb28-47b0-a621-3c9a3c88f490"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md has carried an open item since the Launch Templates
work (Phase 20.27/20.28) gave templates a single-resource detail view:
instances and AMIs are still only ever seen as a row in the List-tier
table (Show instances/Show AMIs), never a dedicated detail screen.

**Decision 1: fetch the fuller field set via a new, separate,
single-resource describe call (`inventory.DescribeInstanceDetail`/
`DescribeImageDetail`), not by adding fields to the aggregate
`Instance`/`Image` structs.** Mirrors `DescribeLaunchTemplateVersion`'s
existing shape.

**Rejected alternative.** *Add SecurityGroupIDs/SubnetID/
IAMInstanceProfile/InstanceType (and BlockDeviceMappings/RootDeviceName
for Image) directly to `Instance`/`Image`* — would let the list-fetch
populate everything in one pass, but every other call site
(`ListInstances`/`ListImages`'s many existing callers: pickers, Tag
Management, the list views) would carry fields it never uses, and this
project already hit real breakage once from adding a map field to these
structs (Phase 20.30's `reflect.DeepEqual` fix, when `Tags` was added) —
not worth repeating for fields only the detail view needs.

**Decision 2: the two new menu entries are appended at the end of
`mainMenuItems`, not placed near "Show instances"/"Show AMIs."**
Consistent with this project's established convention (Phase 20.40) of
appending rather than reordering, so existing numeric-index tests for
prior entries stay valid unchanged.

