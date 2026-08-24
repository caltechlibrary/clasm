---
id: "0036"
title: "Add non-ENA-required options to the curated instance type list"
date: "2026-07-02"
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
uuid: "d1259114-e789-4b23-a307-29a671b90610"
origin_host: "MACMINI-RD.local"
---

**Context.** Trying to launch from a real, legacy AMI (`etd-workflow-v0.0.1`)
that isn't ENA-enabled, every entry in the newly-added curated instance
type list (t3/m5/c5/r5) failed `ensureInstanceTypeENACompatible` --
all nine are Nitro-based and require ENA unconditionally. The "Change
instance type" recovery pick list technically worked, but every
alternative it could offer was equally incompatible: the operator was
launch-blocked with no way to get unstuck without already knowing (from
outside awsops) that e.g. `t2.micro` would work, and typing it via
"Other". This is a real gap in the curated list, not a one-off: any
sufficiently old AMI (common for long-lived, hand-maintained gold
images) hits the same dead end.

**Decision.** Add `t2.micro` and `t2.medium` to `curatedInstanceTypes`
as the list's only non-Nitro, no-ENA-required entries, each labeled
"no ENA required, works with older/legacy AMIs" so they're
self-explanatory in the pick list, not just a name an operator has to
already recognize. Every ENA-requiring entry's label now also says
"(requires ENA)" for the same reason -- so the *first* pick, not just
the recovery pick, can be an informed choice. `ensureInstanceTypeENACompatible`'s
incompatibility message now explicitly suggests these two by name, plus
a one-line pointer to the actual (out-of-scope-for-awsops) permanent
fix: enabling ENA on the source instance and re-creating the AMI.

**Rationale.** The recovery pick list (DECISIONS.md, "Pre-flight check:
instance type vs. AMI ENA support") is only actually useful if it can
offer *some* type that works; a list where every option shares the same
failure mode isn't a recovery path, it's a longer way to the same dead
end. Two low-cost, universally-available legacy types cover the
common case without expanding the list's scope back toward "the full
AWS catalog," which the curated-list decision (above) already rejected.

**Rejected alternatives.**
- *Make promptInstanceType AMI-aware (filter or reorder based on the
  picked AMI's EnaSupport)* -- rejected as unnecessary complexity for
  now: the static list already contains a working answer once it
  includes non-ENA options; the pre-flight check's message pointing at
  them by name accomplishes the same practical outcome without the
  static-list design changing shape or `promptInstanceType` needing new
  parameters/context it didn't have before.
- *Only fix it via the incompatibility message, without adding to the
  curated list* -- rejected because the *first* instance-type pick
  (before any AMI-compatibility check has even run) should also be able
  to make an informed choice for a known-legacy AMI, not just recover
  from a bad one after the fact.

**Consequences.**
- `curatedInstanceTypes` grew from 9 to 11 entries; "Other" shifted from
  pick-list position 10 to 12.
- No new AWS permissions or calls -- purely a static list change plus a
  message update.

---

