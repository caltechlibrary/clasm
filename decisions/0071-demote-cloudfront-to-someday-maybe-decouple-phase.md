---
id: "0071"
title: "Demote CloudFront to someday/maybe; decouple Phase 22's real-AWS testing from it"
date: "2026-07-09"
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
uuid: "ed3f7f67-1594-4ee4-8cbf-1cb481d95052"
origin_host: "MACMINI-RD.local"
---

**Context.** The 2026-07-09 "0.0.1 scope" decision (below) postponed
CloudFront (PLAN.md Phase 21, DESIGN.md Features 22-25) "to a later
version" -- phrasing that still implied it was queued up as a
reasonably near-term next step, just after 0.0.1. With the S3 object
management UI/UX pass now actively designed and planned (this file,
above; PLAN.md Phase 20.1), it's clear CloudFront isn't close to being
picked up next -- the user doesn't expect to get to it soon.

**Decision.** Recharacterize CloudFront as someday/maybe: not on the
active roadmap, no committed timeline, weaker than "postponed to a
later version" implied. The design (DESIGN.md Features 22-25, PLAN.md
Phase 21) stays intact as valid reference -- nothing is deleted, only
the status framing changes. Phase 22 ("Real-AWS Testing") is split so
it no longer depends on Phase 21: it now covers only Key Management and
S3, and CloudFront's own real-AWS verification moves into Phase 21's
own scope, to happen whenever that phase is eventually picked up rather
than gating Phase 22's completion on a someday/maybe item.

**Rejected alternatives.**
- *Leave Phase 22 depending on Phase 21* -- rejected; it would mean
  Key Management and S3 (both actively shipped in 0.0.1) can never be
  marked verified-complete until an indefinitely-postponed domain is
  built, which misrepresents how settled those two domains actually
  are.
- *Delete the CloudFront design entirely rather than demote it* --
  rejected; the design and plan are still valid reference and cost
  nothing to keep, per this project's existing practice for postponed
  work (see the original CloudFront postponement decision below).

**Consequences.** PLAN.md Phase 21's status line, its Priority Order
table row, and Phase 22's title/scope/dependency are updated. DESIGN.md's
CloudFront Domain section gets the same someday/maybe note Features 20
and 21 (S3) already carry for their own supersession. TODO.md's
"Postponed to a later version" section is split into a new "Someday/
maybe" section (CloudFront) and the existing postponed section (the
UI/UX overhaul, which is no longer purely "not started" now that Phase
20.1 exists).

---

