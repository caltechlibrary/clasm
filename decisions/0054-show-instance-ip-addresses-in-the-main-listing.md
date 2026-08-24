---
id: "0054"
title: "Show instance IP addresses in the main listing"
date: "2026-07-02"
status: accepted
kind: refinement
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
uuid: "2b37caa8-a4e5-4dc5-849d-dd2ffa376032"
origin_host: "MACMINI-RD.local"
---

**Context.** While troubleshooting the AWS-CLI-missing backup failure,
the operator installed the AWS CLI on the wrong EC2 instance because
there was no easy way to see which instance had which IP address and
SSH to the right one -- the main "CURRENT EC2 INSTANCES" listing showed
ID/Name/State/AMI/Region/Project/Environment, but no IP at all.
Connection info (`displayConnectionInfo`) already existed, but only as a
one-shot printout right after Create/Start -- not something you could
look up for an already-running instance days later.

**Decision.** `inventory.Instance` gains `PublicIP`/`PrivateIP` fields,
populated from the same `ec2:DescribeInstances` call already made for
every listing (no new API calls). `DisplayInstances` adds "PUBLIC IP"
and "PRIVATE IP" as two new trailing columns, rendering "none" (not
"unknown" -- this isn't missing data, it's a real, common state for a
stopped instance or one without a public IP) when blank.

**Rationale.**
- Adding columns to the always-shown listing, rather than a separate
  "show connection info" action, means the whole fleet's reachability
  is visible on every refresh with no extra menu step -- exactly the
  "which IP do I SSH to" question that triggered this.
- Zero additional AWS calls: `PublicIpAddress`/`PrivateIpAddress` are
  already present on every `DescribeInstances` response object; this
  is populating fields that were already being fetched and discarded.
- "none" instead of "unknown" for blank IPs distinguishes a legitimate
  state (no IP assigned) from `orUnknown`'s existing meaning (missing
  tag data) used for Project/Environment in the same table.

**Consequences.** The table grows two more columns (~26 more characters
per row) -- accepted since the existing table (~115 characters) already
assumes a reasonably wide terminal, and no width budget was documented
as a hard constraint anywhere.

---

