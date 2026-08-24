---
id: "0073"
title: "Fix three post-implementation UX gaps in the file manager and its huh pre-flight"
date: "2026-07-09"
status: accepted
kind: correction
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
uuid: "b9221568-85fb-4845-9e18-0c03c3bc8843"
origin_host: "MACMINI-RD.local"
---

**Context.** The user tried the file manager after Phase 20.1 shipped and
reported three real gaps against DESIGN.md: (1) the bucket-picker
pre-flight (huh) gave no indication of what keys/actions were available;
(2) the file manager's pane rows gave no clear visual indication of
which row the cursor was on or which rows were tagged; (3) the screen's
chrome didn't match DESIGN.md 21.4's bordered-box mockup -- it used
plain dashed-line separators instead.

**Root cause 1 (huh help footer missing).** `huh.Field.Run()` (called by
`object_browser.go` for the bucket `Select`, the link `Confirm`, and the
directory `Input`) is a shortcut for `huh.Run(field)`, which is itself
`NewForm(NewGroup(field)).WithShowHelp(false).Run()` -- it explicitly
disables the help footer. Fixed by adding `runFieldWithHelp(field)` (a
one-line `NewForm(NewGroup(field)).Run()`, leaving `Group`'s default
`showHelp: true` in effect) and calling it in place of `field.Run()` at
all three call sites.

**Root cause 2 (no selection indicator).** The pane rows only had a
single leading `>`/`*` character with no color or emphasis -- easy to
miss, especially in a wide terminal. Added reverse-video on the cursor
row and bold on tagged rows (`view.go`'s `styleRow`), gated by the same
NO_COLOR/non-TTY convention `internal/ui.ColorEnabled` already
establishes elsewhere in this codebase (`Model.colorEnabled`, computed
once at `New()`) -- falls back to the plain `>`/`*` markers alone when
color is disabled or stdout isn't a terminal.

**Root cause 3 (chrome didn't match the mockup).** The screen was never
actually built to render DESIGN.md 21.4's bordered-box mockup -- it used
`strings.Repeat("-", 78)` separator lines instead of the mockup's
`┌─┬┐├┼┤└┴┘│` box-drawing chrome. Rewrote `view.go` to render one
continuous bordered box: a title bar (`┌ clasm — S3 File Manager — ... ┐`),
a `┬`/`┴` divider splitting the pane area in double-pane mode, and
`├─┤` rules between the status line/command line/hotkey legend, sized
to the real terminal width (`tea.WindowSizeMsg`, falling back to a
fixed default before the first one arrives). Content within each box
row is padded/truncated to a rune-accurate visible width, correctly
accounting for the invisible ANSI escapes the reverse-video/bold
styling (root cause 2) adds -- verified with a dedicated test asserting
every rendered line between the outer borders has equal visible width.

**Also fixed while verifying this visually:** `joinKey(root, "")`
(used by `pane.label()`) was appending a spurious trailing slash at a
linked directory's root ("LOCAL: /path/on/disk/" instead of "LOCAL:
/path/on/disk") -- an empty `name` now returns `parent` unchanged.
Caught by rendering the Model directly (bypassing a running
`tea.Program`) via a small `drainCmd` test helper that synchronously
executes a `tea.Cmd` chain -- worth keeping as a pattern for visually
inspecting the `Model` without needing a real terminal.

**Rejected alternatives.**
- *Use `lipgloss.Border` per section instead of hand-rolled box-drawing*
  -- considered; rejected for this pass because lipgloss draws a
  complete border around each styled box independently, which produces
  doubled seams where sections touch (the mockup shows one continuous
  outer border with internal `├─┤`/`┬`/`┴` junctions) unless composed
  much more carefully than the plain string-building approach used
  here.
- *Leave selection color-gating unconditional (always emit ANSI)* --
  considered, since reverse/bold are text decorations rather than
  colors and the NO_COLOR spec is about colors specifically; rejected
  for consistency with this codebase's own existing (stricter)
  interpretation in `internal/ui.Highlight`, which already gates its
  own bold usage the same way.

**Consequences.** `internal/workflow/object_browser.go` gained
`runFieldWithHelp`. `internal/filemanager/view.go` was substantially
rewritten (box-drawing helpers, ANSI-aware width math); `entry.go`'s
`joinKey` got a one-line fix. New tests: `box_test.go` (padding/
truncation/alignment invariants under ANSI styling, the `joinKey`/
`label()` fix, a whole-view row-width consistency check) and a
`styleRow` NO_COLOR-gating test. All pre-existing `internal/filemanager`
tests pass unchanged.

---

