---
id: "0038"
title: "Filter the subnet picker by instance-type Availability Zone support"
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
uuid: "8506c228-6f4e-4273-82e3-a3dab6266d4c"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing surfaced repeated back-and-forth: pick an
instance type, pick a subnet, get told after the fact ("Instance type
... is not offered in ... this subnet's Availability Zone") that the two
don't work together, then recover via a pick list. The user's framing:
"the choices are out of context" -- since instance type is already
chosen by the time Subnet ID is prompted, the tool already has enough
information to never offer an incompatible subnet in the first place,
rather than offering it and then walking the operator back out.

**Decision.** `promptSubnetID` now takes the already-chosen
`instanceType` and narrows its subnet listing to those whose
Availability Zone actually offers it
(`filterSubnetsByInstanceTypeAZ`, reusing `instanceTypeOfferedAZs`) --
instance type stays the first choice (unchanged position in the flow;
it's the workload-driven decision -- cost, performance, ENA-compatibility
with the AMI), and the network choice narrows around it, not the other
way around. Filtering is best-effort and never a dead end: if the
AZ-offerings lookup itself errors, or if filtering would leave zero
subnets to pick from, `promptSubnetID` falls back to showing the full,
unfiltered list. `ensureInstanceTypeSupportedInSubnet`'s reactive
recovery pick list (2026-07-02, above) is unchanged and stays in place as
the safety net for exactly those two fallback cases (and the free-text-
fallback path, where the AZ isn't known at all) -- in the common case
where filtering succeeds, that reactive check now simply finds the
already-filtered subnet compatible on the first try and returns
immediately, invisible to the operator.

**Rationale.** Matches the "different routes through the same choices
should all reach a running system" framing directly: narrowing options
before they're offered, instead of discovering and recovering from an
incompatible combination after the fact, removes a whole category of
back-and-forth without removing any actual capability -- every subnet
that could have worked is still offered; only the ones that couldn't
are pre-filtered out.

**Rejected alternatives.**
- *Reorder to prompt for subnet before instance type* -- rejected (see
  the exploratory discussion this decision follows from): instance type
  is the more workload-driven decision and should stay a free first
  choice; subnet is more of an implementation detail that can be
  narrowed once the type is known. Reordering would also do nothing for
  the unrelated instance-type-vs-AMI-ENA-support check, which depends on
  the AMI (chosen long before either instance type or subnet), not the
  network.
- *Remove `ensureInstanceTypeSupportedInSubnet` now that filtering
  exists* -- rejected: it's still the only thing that catches an
  incompatibility when filtering itself couldn't run (lookup error) or
  couldn't narrow anything (all known subnets incompatible) or wasn't
  attempted at all (free-text fallback, unknown AZ). Removing it would
  turn those cases back into dead ends.

**Consequences.**
- `promptSubnetID`'s signature gained an `instanceType string` parameter;
  its three call sites (`launch_instance.go`, `launch_from_cloud_init.go`,
  and `ensureInstanceTypeSupportedInSubnet`'s "Pick a different subnet"
  branch) already had the instance type in scope, so no new plumbing was
  needed beyond passing it through.
- No new AWS permissions or calls in the common case -- the same
  `ec2:DescribeInstanceTypeOfferings` call `ensureInstanceTypeSupportedInSubnet`
  already made reactively now happens once, proactively, per launch.

---

