---
id: "0103"
title: "Manage Tags: embed current tags in the action Select's own Description, not just a separate print above it"
date: "2026-07-20"
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
uuid: "22bf6813-879b-4453-b391-3a77705ea788"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via real-terminal testing of the new Tag Management
domain (Phase 20.30): confirmed Add/Update/Remove all worked correctly
for both launch templates and instances, but choosing "Show tags" from
the action menu appeared to do nothing -- the screen looked unchanged.
Confirmed directly with the operator (not assumed): the screen, not the
underlying data, was the problem.

**Decision.** `manageTagsForResource`'s loop (Phase 20.29) already
re-displays current tags via a plain `displayTags(w, ...)` print at the
top of every iteration, immediately followed by the "Choose an action"
huh.Select. Root cause: that Select is a Menu-tier field, pinned to the
full terminal height on every render (DESIGN.md, "Full-height Menu
Tier", Phase 20.26) -- so the instant it renders, it fills the entire
visible terminal and scrolls whatever was printed just before it (here,
displayTags' output) out of view. The data was always current; the
*screen* just never showed it by the time the operator could read it.
Fixed by adding `actionMenuDescription(label, tags)` and passing it as
the Select's own `Description` -- embedding the same tag listing inside
the full-height chrome that's guaranteed to be on screen, instead of
relying on separately-scrolled-away plain output. `displayTags` itself
is kept alongside it, unchanged: huh.Select's accessible-mode
`RunAccessible` only ever prints a field's Title and options, never its
Description (confirmed by reading huh v1.0.0's `field_select.go`), so
every existing accessible-mode test asserting tag content in output
still depends on `displayTags`' plain print and needed no changes.

**Rationale.** This is the same class of bug the Full-height Menu Tier
work (Phase 20.26) already flagged as a risk in principle -- a
full-height render can silently evict whatever was on screen just
before it -- but this specific instance wasn't caught until real
interactive use, since `manage_tags_test.go`'s coverage only asserts
buffer *content*, not what remains visibly on screen after a
full-height render. No other Menu-tier call site in this package prints
data immediately before a full-height Select the way this one does, so
this fix is scoped to `manageTagsForResource` rather than a broader
change to `runMenuField`/`quitKeyGuard`.

**Rejected alternatives.**
- *Shrink the action Select below full terminal height so the printed
  tags stay visible above it* -- rejected: reverses Phase 20.26's own
  full-height decision for one call site, reintroducing the
  inconsistent-compact-submenu problem that phase deliberately fixed.
- *Remove "Show tags" as a menu choice, since the loop already
  redisplays tags on every iteration regardless* -- rejected: the
  redisplay-on-every-iteration behavior is exactly what was invisible;
  removing the choice wouldn't fix the underlying visibility bug for
  the post-Add/Update/Remove redisplay either.

**Consequences.** `actionMenuDescription` (`manage_tags.go`), tested
directly (`TestActionMenuDescription_ListsCurrentTags`/`_NoTags`).
Applies uniformly to both Compute's "Manage tags for an instance or
AMI" (Phase 20.29) and Tag Management's "Manage tags" (Phase 20.30),
since both call the same `manageTagsForResource`.

---

