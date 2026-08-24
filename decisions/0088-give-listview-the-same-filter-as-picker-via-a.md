---
id: "0088"
title: "Give ListView the same filter as Picker, via a shared filterState"
date: "2026-07-10"
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
uuid: "70184fd2-ad10-4d86-81df-5c3d05c0f65a"
origin_host: "MACMINI-RD.local"
---

**Context.** Right after confirming the `tea.ClearScreen` scrolling fix
("Scrolling is much improved"), the user asked whether List-tier
filtering was still planned, recalling an earlier discussion. Checking
DESIGN.md confirmed it: the keybinding conventions table has listed
`/` = Filter for "Menus, pickers, lists, managers" since Phase 20.8,
but `ListViewModel` (`internal/tui/listview.go`) had no filter code at
all -- a real, previously-documented gap, not a misremembering.

**Decision.** Add filtering to `ListViewModel`, matching `PickerModel`
exactly (case-insensitive substring match, `/` to start typing, `Enter`
commits, `Esc` clears, content-height pinned to the unfiltered row
count while typing). Rather than copy `PickerModel`'s filter fields and
methods a second time, extract them into a shared `filterState` type
(`internal/tui/filter.go`) that both models embed: `visible []int`,
`cursor int`, `filtering bool`, `filter string`, plus `apply`,
`moveCursor`, `handleIdleKey`, `handleFilterKey`, `statusLine`. Each
model's `Update` still owns its own quit/select semantics (List just
quits on `q`; Picker also selects on `Enter`) and delegates everything
else to the shared type.

While unifying the two models' box-height math to accommodate the new
filter status line, also folded in `PickerModel`'s existing (optional)
header handling into `ListViewModel` (previously always rendered, even
blank) and replaced both models' separate `windowHeight()` bodies with
one shared `filterableWindowHeight(height, hasHeader bool)` helper --
which also fixed a minor pre-existing off-by-one in `PickerModel`'s own
chrome arithmetic (it subtracted a flat, imprecise `-1` for the filter
line instead of counting the filter line's own divider row).

**Rejected alternative.** Duplicate Picker's filter implementation
directly into `ListViewModel`. Rejected because the user's stated goal
across this whole follow-up ("we want to have the chrome more
consistent") is exactly what a second hand-copy would undermine --
consistency by convention (two implementations that happen to match
today) drifts the moment either one is touched later. A shared type
keeps them identical by construction.

**Consequences.** `ListViewModel` and `PickerModel` are now guaranteed
to filter, scroll, and size identically; any future filter change lands
in one place. `internal/tui/listview_test.go` gained direct mirrors of
`picker_test.go`'s filter tests (minus selection, which List doesn't
have). No behavior change for any existing `ListView`/`Picker` caller:
all currently supply a non-empty `Header`, so the now-conditional
header line renders exactly as before.

