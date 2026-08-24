---
id: "0087"
title: "Clear the screen on entry for every inline bubbletea screen"
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
uuid: "a2d67b7b-0796-4b92-993a-589ae1155a51"
origin_host: "MACMINI-RD.local"
---

**Context.** After the List tier's conversion (Phase 20.13), the user
reported that the List view "doesn't take advantage of the window
height so a significant number of lines aren't visible much of the
time," and separately wanted "the chrome more consistent" across
screens so switching between them isn't jarring. Follow-up narrowed the
first report to: the box *does* size to the real terminal height, but
paging scrolls content out of view -- and the desired consistency is
Picker/ListView/file-manager behaving identically to each other, not
adopting `tea.WithAltScreen()` (which `huh`, used for every Menu-tier
prompt, has no equivalent for at all -- adopting alt-screen for only
some screens would make transitions *more* jarring, not less).

**Root cause.** `windowHeight()` in each of `ListViewModel`/
`PickerModel`/filemanager's `Model` sizes its box to (terminal height −
a small fixed chrome overhead) -- nearly the *entire* terminal. None of
the three clear the screen on entry (DESIGN.md's own note: "Renders
inline, no `tea.WithAltScreen`, matching every other screen in
clasm"), so each one starts rendering wherever the cursor already sits
-- e.g. below a previous menu's prints. If that near-full-height box
doesn't fit in the rows remaining below the cursor, the terminal
scrolls to accommodate it, and bubbletea's redraw-in-place bookkeeping
(how many lines to move the cursor up by, to redraw the same frame in
place) goes stale relative to what the terminal actually did --
pushing the top of the box (title, header, and however many rows above
the scroll point) out of the visible viewport. This is exactly the
"significant number of lines aren't visible much of the time" report:
not a sizing bug, a scroll-desync bug, and it gets worse the fuller the
terminal already is when the screen launches.

**Decision.** Every inline bubbletea screen (`ListViewModel.Init`,
`PickerModel.Init`, filemanager's `Model.Init`) now returns
`tea.ClearScreen` (bubbletea's own built-in command for exactly this
situation -- its doc comment: "can be used to move the cursor to the
top left of the screen and clear visual clutter when the alt screen is
not in use") as (part of) its initial command, guaranteeing every one
of these screens always starts rendering from row 0. This makes the
already-correct `windowHeight` sizing reliable (the box always fits,
since there's nothing above the cursor to compete for terminal rows
with it) and, as a side effect, gives Picker/ListView/file-manager one
more point of behavioral consistency: each always wipes whatever was on
screen and starts crisp, rather than accumulating underneath the
previous screen's leftover output.

**Rejected alternatives.**
- *Shrink `windowHeight` to leave a safety margin below whatever's
  already on screen* -- rejected: there's no reliable way to know how
  many rows are already "used" above the cursor (that would require
  querying the terminal's actual cursor position, which bubbletea
  doesn't expose), so any fixed margin is either too conservative
  (wastes screen space the user explicitly wants used) or still
  breaks in a sufficiently full terminal.
- *Switch Picker/ListView/file-manager to `tea.WithAltScreen()`* --
  rejected per the user's own stated preference: `huh` (every
  Menu-tier prompt) has no alt-screen equivalent, so only some screens
  taking over the full terminal while others render inline beside
  whatever's already there would be a *new* inconsistency, not a fix
  for the existing one.

**Consequences.** `tea.ClearScreen` clears the primary screen buffer,
not the alternate one -- it doesn't touch terminal scrollback the way
real alt-screen entry/exit does, so this is fully compatible with the
existing "inline, no alt-screen" design decision; it complements it
rather than reversing it. No test changes were needed -- every
existing `teatest`/direct-`Model`-driven test already drains whatever
`Init()`/`Update()` commands are queued, including the new
`tea.ClearScreen`, without asserting on `Init()` returning `nil`.

---

