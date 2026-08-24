---
id: "0033"
title: "Pre-flight check: instance type vs. subnet Availability Zone"
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
uuid: "6b8802f3-516b-4a7c-9806-405c0ee22a27"
origin_host: "MACMINI-RD.local"
---

**Context.** The `-debug` JSONL log from the same real-AWS testing
session that surfaced the key-filename bug (above) showed a second,
unrelated real failure in the middle of that sequence: once the key
pair name was correct, `RunInstances` failed with `Unsupported: Your
requested instance type (t2.micro) is not supported in your requested
Availability Zone (us-west-2d). Please retry your request by not
specifying an Availability Zone or choosing us-west-2a, us-west-2b,
us-west-2c.` -- the picked subnet (`subnet-5870b473`) sits in
`us-west-2d`, which doesn't offer `t2.micro`. This is the same general
class of problem as the already-deferred ENA pre-flight check idea
(TODO.md) -- an instance-type/launch-parameter incompatibility AWS only
reports after the fact -- but a different specific incompatibility (AZ
offering, not ENA support).

**Decision.**
- After the Subnet ID prompt (Feature 2/3), check whether the
  already-chosen instance type is actually offered in the picked
  subnet's Availability Zone (`ec2:DescribeInstanceTypeOfferings`,
  `LocationType=availability-zone`).
- If it isn't, print the incompatibility and (best-effort) the AZs the
  instance type *is* offered in, then show a pick list: **Change
  instance type**, **Pick a different subnet**, or **Abort this
  launch** -- rather than a dead-end error message or silently sending
  a `RunInstances` call already known to fail.
- "Abort this launch" returns `ui.ErrCancelled`, reusing the exact same
  cancellation path every other declined/cancelled confirmation in this
  tool already uses (`CreateInstanceFromAMI`/`CreateInstanceFromCloudInit`
  catch it and print "Cancelled.", returning to the domain menu) --
  no new cancellation mechanism needed.
- The check is skipped entirely (not just tolerantly failed) when the
  subnet's Availability Zone is unknown -- i.e. `promptSubnetID` fell
  back to its free-text prompt -- or when the check call itself errors,
  matching this tool's existing "best-effort diagnostic, never blocks
  the whole flow" pattern (e.g. SSM-unavailable fallbacks).
- Scoped to this one incompatibility class for now, not a general
  multi-check framework -- the ENA-support variant (TODO.md) remains a
  separate, not-yet-implemented item; if a third class of
  incompatibility turns up, that's the point to reconsider a shared
  abstraction, not before.

**Rationale.**
- Fixes a real failure found in the same debug-log session as the key-
  filename bug, using the tool that made it discoverable in the first
  place (the `-debug` log) rather than guessing.
- A pick list of concrete remediation options (change type / change
  subnet / abort) is more actionable than a printed error the operator
  has to interpret and act on by restarting the flow -- and matches an
  explicit request that error recovery should offer a pick list, not
  just a message.
- Reusing `ui.ErrCancelled` for "abort" avoids inventing a second
  cancellation contract alongside the one this tool already has.

**Rejected alternatives.**
- *Generalize into a multi-check pre-flight framework covering ENA and
  AZ together* -- rejected for now as premature: only one check is
  actually implemented; building shared abstraction for a framework of
  one (plus one still-deferred idea) isn't justified yet.
- *Just show a better error message, no recovery pick list* -- rejected
  per explicit direction that a pick list to correct or abort is the
  right shape once a chosen setting turns out to be invalid.

**Consequences.**
- `promptSubnetID`'s return type changed from `(string, error)` to
  `(SubnetInfo, error)`, so its caller has the picked subnet's
  Availability Zone available without a redundant lookup -- `SubnetInfo`
  already carried this field for the pick-list label. The free-text
  fallback path returns `SubnetInfo{SubnetID: ...}` with an empty
  `AvailabilityZone`, which is exactly the "unknown, skip the check"
  signal `ensureInstanceTypeSupportedInSubnet` looks for.
- New EC2 permission required: `ec2:DescribeInstanceTypeOfferings` (see
  `DESIGN.md`, "Assumptions").
- No new AWS SDK dependency -- `DescribeInstanceTypeOfferings` is
  already part of the `ec2` package this tool depends on.

---

