---
id: "0083"
title: "Terminal UI architecture: menu → action/list/manager taxonomy; shared internal/tui chrome package"
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
uuid: "a9d62d73-9132-49e0-a1d2-dedf192326ba"
origin_host: "MACMINI-RD.local"
---

**Context.** Direct follow-on to the decision above: once `huh`/
`bubbletea` are the committed direction, what's the concrete shape of
"every screen looks and behaves like the same tool"? Full design:
DESIGN.md, "Terminal UI Architecture: Menus, Actions, Lists, and
Managers."

**Decision.**
- Every navigation path resolves to one of three destinations reached
  through a menu that is never itself a destination: **guide menu**
  (`huh.Select` today), **action wizard** (a short prompt sequence that
  gathers parameters and executes one thing), **list** (a read-only
  scrollable resource display), or **manager** (a persistent stateful
  screen, e.g. the S3 object manager).
- New `internal/tui` package: the file manager's already-pure
  box-drawing/scroll/style helpers (`topBorder`, `bottomBorder`,
  `divider`, `splitDivider`, `mergeDivider`, `boxLine`, `boxRow2`,
  `padOrTruncate`, `runeLen`, `stripANSI`, `truncateVisible`,
  `scrollWindow`, `styleRow`) move there unchanged, and
  `internal/filemanager` imports them instead of keeping its own copy.
  `internal/ui` stays in place for as long as termlib-based call sites
  remain, shrinking over the course of the termlib removal rather than
  being replaced in one step.
- New List-tier component in `internal/tui`, replacing
  `internal/ui.PagedTable`/`DisplayBuckets`: single bordered box, frozen
  header row, scrollable body via the shared `scrollWindow` logic, sized
  to the real terminal via `tea.WindowSizeMsg`, a legend bar, rendered
  inline (no alt-screen). Quitting returns to the menu it was opened
  from (for S3 buckets, the S3 menu — not `ErrBackToDomainPicker`, which
  is one level further up).

**Rejected alternatives.**
- *Keep chrome duplicated per screen* — the drift risk this whole
  decision exists to avoid; one implementation shared via
  `internal/tui` instead of `internal/filemanager` and a new List
  component each maintaining their own box-drawing.
- *Build the List tier as a variant of `filemanager.Model`* — rejected:
  that `Model` carries file-manager-specific state (panes, tagging,
  sync, linked local directories) that's the wrong shape for a plain
  read-only list; a dedicated, smaller `internal/tui` component matches
  this project's existing preference for small, purpose-built pieces
  over one component doing everything (same reasoning as keeping
  `Confirm`/`ConfirmDestructive` separate from `PickList`).

**Consequences.** `internal/filemanager` is refactored to import
`internal/tui`'s helpers with no behavior change (its existing
`teatest`-based test suite should continue to pass unmodified — a
regression there would mean the extraction wasn't actually behavior-
preserving). `internal/ui.DisplayBuckets`/`PagedTable` and their tests
are retired.

---

