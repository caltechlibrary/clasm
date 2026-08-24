---
id: "0093"
title: "Contextual description text on Menu/Picker-tier screens"
date: "2026-07-13"
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
uuid: "4da0ffc2-4b05-44d3-b1dc-5794c9232914"
origin_host: "MACMINI-RD.local"
---

**Context.** The domain picker (and every other Menu-tier `huh.Select`
and Picker-tier `tui.RunPicker` screen) showed only a bare title -- no
explanation of what the choice means or what happens next. Raised
alongside the border-matching decision above, as part of the same
"make the chrome and the screens themselves more consistent and
informative" pass.

**Decision.** Every Menu-tier `huh.Select` gains a `.Description(...)`
call (huh's own built-in field, previously unused everywhere in this
codebase) with one or two sentences of context. `tui.PickerConfig`
gains a new `Description string` field, rendered as its own line
(matching `Header`'s existing shape: the line itself plus a `Divider`)
directly below the top border, above any `Header`/rows;
`filterableWindowHeight` accounts for its two extra chrome rows the
same way it already does for `Header`. `pickString`/`pickComparable`
(the shared Menu-tier helpers) and `pickImage`/`pickBucket`/
`pickInstance`/`pickInstanceDefaulted` (Picker-tier functions called
from more than one call site with meaningfully different context) all
gained a `description string` parameter threaded from their own
callers; Picker-tier functions with exactly one caller
(`pickInstanceProfileChoice`, `pickRole`, `pickSubnet`,
`pickKeyPairChoice`, `pickKeyPairForDeletion`, `pickLifecycleRule`) got
a single description written directly into the function instead, since
there was no real per-call-site variation to preserve. List-tier
screens (`ListViewConfig`/`ListViewModel` -- the tabular "Show resource
lists" displays) deliberately did NOT get a `Description` field: they
aren't "just a pick list," and their tabular column headers already
carry the relevant context.

**Rationale.** huh's `.Description()` is a stable, well-established
part of its own API -- adding text there is essentially free and
carries no risk of a rendering bug on our part. The Picker-tier
`Description` field mirrors `Header`'s existing chrome shape exactly
(same two-row cost, same position, same `Divider` beneath it), so it
reuses an already-proven layout instead of inventing a new one.
Threading a parameter only where callers actually differ (rather than
everywhere) avoids padding every call site with an unused, always-""
argument.

**Consequences.** `pickString`, `pickComparable`, `pickImage`,
`pickBucket`, `pickInstance`, `pickInstanceDefaulted` all gained a
`description string` parameter -- every call site across
`internal/workflow` was updated. `internal/tui/filter.go`'s
`filterableWindowHeight` gained a `hasDescription bool` parameter
(`ListViewModel`'s call site passes `false` explicitly, since List-tier
doesn't use this). New `internal/tui/filter_test.go` and additions to
`picker_test.go` cover the height math and rendering position.

---

