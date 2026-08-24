---
id: "0094"
title: "huh fields get a full box border to match tui's chrome"
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
uuid: "891f7b65-71ad-410f-b6d5-c664b0ac7a09"
origin_host: "MACMINI-RD.local"
---

**Context.** Phase 20.17 gave `huh` fields and `internal/tui`'s boxes the
same indigo accent color, but not the same *shape*: `huh.ThemeBase()`'s
`Focused.Base` draws only a thick bar down the left side of a field
(`lipgloss.ThickBorder().BorderLeft(true)`), while `tui/box.go` draws a
full `┌─┐│ │└─┘` rectangle. Matching color without matching shape left
a Menu-tier `huh.Select` and a Picker/List/Manager screen still reading
as two different visual languages -- raised when reviewing chrome
consistency alongside adding contextual description text (below).

**Decision.** `tui.Theme()`'s `Focused.Base`/`Focused.Card` now call
`.Border(lipgloss.NormalBorder())` (the same box-drawing characters
`box.go` uses) instead of inheriting `ThemeBase`'s left-only
`ThickBorder`, still colored in the shared accent. `Padding(0, 1)`
replaces `ThemeBase`'s `PaddingLeft(1)` (which existed only to clear
the single left bar) with balanced left/right breathing room, matching
`box.go`'s `BoxLine`'s own "│ content │" convention. `Blurred.Base`
still hides its border via `lipgloss.HiddenBorder()`, now reserving the
same four-sided footprint rather than a one-sided one.

**Rationale.** Every clasm form is a single field in a single group (no
multi-field forms exist in this codebase -- confirmed when `Theme()`
was first written), so a full box reads as a small dialog card rather
than a form-with-a-sidebar-accent -- the same "boxed window" shape a
List/Picker/Manager screen already has. Verified via a throwaway test
rendering `Theme().Focused.Base.Render(...)` directly with a forced
true-color profile and inspecting the raw ANSI output (the same
technique used to verify Phase 20.17's border styling), rather than
driving a real interactive terminal.

**Consequences.** No signature changes -- `Theme()`'s return type and
every call site are unaffected; this is a pure style-value change
inside the function.

---

