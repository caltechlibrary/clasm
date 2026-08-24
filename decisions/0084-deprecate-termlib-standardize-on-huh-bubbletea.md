---
id: "0084"
title: "Deprecate termlib; standardize on huh/bubbletea before 0.0.2; drop screen-reader/accessible-mode as a TUI requirement"
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
uuid: "e0bde50c-a9ac-4882-9e59-c66f8a1a864e"
origin_host: "MACMINI-RD.local"
---

**Context.** Working through the S3 menu's huh.Select conversion and the
paged bucket-list display (both same-day, below) surfaced a values
question worth having directly rather than deciding by accretion: what
is clasm actually *for*, and what does that imply about its UI? The
user's framing: clasm exists to give this team a fluid alternative to
AWS's own web console (which they find bad and getting worse) and to
one-off Bash scripts — but that alternative only works if colleagues can
learn it in one sitting, which requires every screen to look and behave
like the same tool rather than a collection of differently-styled
prompts. Reviewing `internal/filemanager/view.go` directly (not just
recalling its design) found that its box-drawing/legend/scrolling code
is already pure functions with no dependency on `filemanager.Model`, and
that `'q'` already means quit there (`case "ctrl+c", "q": m.quitting =
true; return m, tea.Quit`) and it already renders inline (no
`tea.WithAltScreen`) — meaning the "consistent chrome, consistent keys"
goal is mostly already *in* the codebase, just not applied project-wide.
Separately, `termlib.Terminal.UpdateTerminalSize` was found to hardcode
`os.Stdout.Fd()` regardless of what writer a `Terminal` was actually
constructed with — a real defect for anything wanting genuine
terminal-height-aware sizing, and a concrete symptom of `termlib` being
a poorer fit than `bubbletea`'s own `tea.WindowSizeMsg` (sent to
`Update` once at start and again on every resize) for where this tool is
headed.

**Decision.** `termlib` is removed entirely before 0.0.2. All TUI
surfaces converge on `huh` (guide menus, action wizards) and `bubbletea`
(lists, managers) exclusively — see "Terminal UI architecture" and "TUI
keybinding conventions" below for what that looks like concretely.
Screen-reader/non-TTY accessible rendering is explicitly **not** a
requirement for clasm's TUI going forward: it's an internal tool for
Library staff managing AWS resources, not public-facing, distinct from
this workspace's Frontend Guidelines A11y requirement for browser-side
Web Components (unaffected by this decision). In an ideal world the
user would like the whole application to be screen-reader friendly, but
set that aside as not a hard requirement once it was clear it was in
real tension with the visual-consistency goal above.

**This explicitly supersedes** (left in place as accurate history, not
deleted or rewritten):
- "0.0.1 scope: ship on termlib as-is; postpone CloudFront and the
  UI/UX overhaul" (2026-06-30/07-09 era) — huh is no longer merely "the
  leading candidate for the next release"; it and bubbletea are now the
  committed direction, with a `termlib` removal target (before 0.0.2),
  not an open evaluation.
- "huh fields are pipe-testable via WithAccessible(true).WithInput/
  WithOutput" (2026-07-10, earlier the same day) — remains factually
  correct (the mechanics were verified against real huh source) but is
  no longer load-bearing for design decisions, since accessible-mode
  compatibility is no longer a goal. Testing anything built as a real
  `bubbletea` component going forward uses `teatest` instead (already
  proven against `internal/filemanager`'s `Model` in Phase 20.1), not
  huh's accessible-mode pipe pattern.
- "Decouple the S3 menu from resource-list display; add a generic paged
  table to internal/ui" (2026-07-10, earlier the same day) —
  `internal/ui.PagedTable`/`DisplayBuckets`, implemented and shipped
  earlier today, are retired in favor of a `bubbletea`-based List-tier
  component (see "Terminal UI architecture" below) less than a day
  after landing. The design was correct given its stated constraint
  (stay accessible); the constraint itself is what changed.

**Rejected alternatives.**
- *Keep termlib for simple prompts, use huh/bubbletea only for more
  complex screens* — rejected because a mixed system is exactly the
  "memorize different command sequences per screen" problem the user is
  trying to avoid; partial consistency isn't the goal, whole-tool
  consistency is.
- *Fix termlib's `os.Stdout`-hardcoding bug and keep using it* —
  rejected: `termlib` is the user's own separate project
  (`~/Laboratory/termlib`), and fixing that one defect wouldn't address
  the deeper mismatch (a blocking-prompt library isn't the right
  foundation for a genuinely chrome-consistent, live-resizing bordered
  UI). `termlib` served its purpose already — see Consequences.
- *Keep accessible-mode support as a stretch goal* — rejected per the
  user directly: nice-to-have in an ideal world, not required for an
  internal staff tool, and in real tension with the visual-consistency
  goal that matters more here.

**Consequences.** `termlib` is credited with having answered three real
design questions this project needed answered before committing to a
final UI approach: how to organize an AWS-management menu system that's
intuitive without deep AWS knowledge; how to make individual actions
(create a bucket, update a policy) quick and easy; and how to keep
workflows structured for future automation of repetitive tasks (see
"Structure workflows for future record/replay ('Recorded Scripts')",
2026-07-01 — unaffected by this decision; that design was already
UI-toolkit-agnostic by construction, via the params-struct/execute
split). Having answered those, it's no longer needed. The remaining
~40 `termlib`-based call sites are not converted all at once — see
"Terminal UI architecture"'s "Not decided yet" for pacing.

---

