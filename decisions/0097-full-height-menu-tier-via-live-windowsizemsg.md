---
id: "0097"
title: "Full-height Menu tier via live WindowSizeMsg tracking, applied at every depth"
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
uuid: "22752829-4b70-496c-b40c-c043a35962c4"
origin_host: "MACMINI-RD.local"
---

**Context.** Phase 20.24 (2026-07-13) cleared the screen at startup but
deliberately left the "full height" half of that request unimplemented
-- the domain picker (and every Menu-tier `huh.Select`) stayed a
compact, content-sized box, pending clarification of what "full
height" meant for a component with no built-in notion of it. Clarified
directly, 2026-07-20: the wrapping TUI chrome should carry a real
terminal height, driving how many rows the `huh.Select` shows; when a
menu has fewer options than that, the chrome should still indicate the
full screen rather than shrinking to content.

**Decision.** Use `huh.Select.Height(n)`/`huh.Form.WithHeight(n)`
directly -- confirmed by reading `huh` v1.0.0's source rather than
assumed: it already subtracts title/description height before sizing
the options viewport, and renders through `lipgloss.Style.Height`,
which pads short content with blank lines to reach `n`. Get `n` by
intercepting `tea.WindowSizeMsg` live (the same pattern
`internal/tui/picker.go`/`listview.go` already use for the Picker/List
tier) rather than a one-shot `x/term.GetSize` read before the form
starts, so a mid-session terminal resize is picked up the same way it
already is everywhere else in the app. Apply this at `runMenuField`,
the single shared entry point every Menu-tier `huh.Select` already
runs through -- so the root domain picker and every submenu (S3, EC2,
Key Management) become full-height together, not just the root picker
alone.

**Rationale.**
- huh's own `WindowSizeMsg` handling only shrinks a group to fit
  (`min(neededHeight, msg.Height)`) when `f.height == 0`; it never
  grows short content to fill unused space, so `WithHeight` must be
  called explicitly with a real value -- there's no simpler built-in
  toggle for this.
- Live tracking via `WindowSizeMsg` reuses an already-proven pattern in
  this codebase instead of introducing a second, weaker mechanism
  (`x/term.GetSize`) that would go stale on resize.
- Fixing this once in `runMenuField` avoids the exact inconsistency
  Phase 20.24 refused to introduce: only the root picker being
  full-height while every submenu stayed compact, undoing the
  chrome-consistency work of Phases 20.17-20.25.

**Rejected alternatives.**
- *One-shot `x/term.GetSize` before `Run()`* -- simpler, but blind to a
  terminal resize mid-menu, unlike every other screen in the app.
- *Full-height only at the root domain picker* -- explicitly the
  option Phase 20.24 already declined, for the inconsistency reason
  above.
- *Redesign the whole Menu tier onto a full-height bubbletea component,
  retiring `huh.Select` for navigation menus* -- the "bigger
  alternative" flagged in the 2026-07-14 hand-off as unscoped; not
  needed now that `huh.Select.Height`/`Form.WithHeight` turned out to
  already support this directly.

**Consequences.** `runMenuField`'s `quitKeyGuard` wrapper (currently
used only to guard the Quit keybinding while a field is filtering)
gains `WindowSizeMsg` interception and is extended to wrap the
non-filtering `form.Run()` path too, so both paths go through one
`tea.Model`. The `"(q to go back)"` hint `runMenuField` prints outside
the form's own box must be accounted for in the reserved-line budget
passed to `WithHeight`, or combined output overflows the terminal by
one line. See `PLAN.md` Phase 20.26.

---

