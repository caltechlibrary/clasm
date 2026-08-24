---
id: "0048"
title: "Highlight PickList's prompt header when color is enabled"
date: "2026-07-02"
status: accepted
kind: refinement
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
uuid: "4fce6926-a061-4a79-b60a-8563a20495ad"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing surfaced a UX gap: after picking a main
menu action by number (e.g. typing 4 for Start instead of 5 for Stop),
the resulting pick list looks the same regardless of which action was
chosen -- the operator has to read through the whole list of instances
to notice the mismatch, since the only place the action name ("Select an
instance to start") appeared was buried at the bottom as the trailing
input prompt.

**Decision.** `ui.PickList` now prints its `prompt` argument as its own
line *before* the numbered list, wrapped in a bold ANSI escape when
color output is enabled (`ui.Highlight`), so the action name is the
first thing on screen. Color enablement for this is a new
package-level flag (`ui.SetColorEnabled`, read by `Highlight`), set once
in `main.go` from the same `ui.ColorEnabled()` result already computed
for `DisplayInstances`'s STATE column.

**Rationale.**
- The header must appear *before* the list, not just as the trailing
  input query, to actually save the reread the operator asked for --
  otherwise the highlighted text is the last thing read, not the first.
- A package-level flag (rather than threading a `colorEnabled bool`
  parameter through `PickList` and the ~26 call sites across 16
  workflow files that reach it) keeps this a small, low-risk change for
  a cosmetic feature. `DisplayInstances`/`DisplayImages` keep their
  existing explicit-parameter style since they're called directly from
  `main.go` with no intervening call chain.
- Bold (not a hue like green/red/yellow) avoids implying success,
  failure, or a warning -- meanings already claimed by
  `stateColor`'s use of color for instance state.

**Rejected alternatives.**
- *Thread `colorEnabled` through every workflow function down to
  `PickList`.* Consistent with `DisplayInstances`'s style, but a large
  diff (16 files, dozens of function signatures) for a purely cosmetic
  feature; revisit if a second cross-cutting per-call setting emerges
  that would justify threading a shared options struct instead.
- *Color the prompt only in its existing position (after the list).*
  Doesn't address the actual complaint -- the operator has already read
  the list by the time they reach it.

**Consequences.** `internal/ui/color.go` gains one small piece of
mutable package state (`colorEnabled`, defaulting to `false`, set once
at startup and never changed thereafter) -- acceptable since it's
write-once and read-only from that point on, and every unit test that
doesn't call `SetColorEnabled` observes the same disabled default as
before this change.

---

