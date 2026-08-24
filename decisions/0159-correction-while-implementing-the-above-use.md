---
id: "0159"
title: "Correction while implementing the above: \"use existing\" must return `created=true`, not `created=false`"
date: "2026-08-18"
status: accepted
kind: correction
trigger: ""
project: clasm
phase: ""
supersedes: ["0160"]
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "104446e5-777a-49dd-85d5-f27c45f31073"
origin_host: "MACMINI-RD.local"
---

**Context.** Implementing Decision 1 above literally as written
(`created=false` on "use existing") and tracing its caller,
`promptIAMInstanceProfileOrCreate`, surfaced that this would have broken
the very feature it was meant to add: that function's own loop only ever
returns a resolved name to *its* caller when `created` is `true`
(`if created { return name, nil }`); `false` unconditionally redisplays
the instance-profile picker instead, on the theory that nothing usable
was resolved (correct for the pre-existing "no SSM-capable roles" case,
which really does have nothing to return). Returning `false` for "use
existing" would have silently discarded the just-confirmed, perfectly
usable existing profile name and looped back to the picker -- the
operator's "yes, use it" would have appeared to do nothing.

**Decision.** "Use existing" returns `(profileName, true, nil)`.
`created` here is better read as "a usable profile name was resolved,"
not literally "a new profile object was created" -- the only place that
distinction would matter (whether to log/print something as
newly-created) already happens locally, before either return.

**Rationale.** Caught by re-reading the calling code before implementing
the literal plan text, not by a failing test -- worth noting as a
general reminder that a design decision's *return-value contract* needs
tracing through its actual caller, not just its own local logic, before
committing to an implementation.

**Consequences.** `TestCreateInstanceProfileForRole_CollisionOffersUseExisting`
asserts `created` is `true` on this path. No other change to Decision 1
above.

---

