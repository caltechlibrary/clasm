---
id: "0090"
title: "Chrome standardization: one shared indigo accent via lipgloss"
date: "2026-07-13"
status: accepted
kind: refinement
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
uuid: "be973b36-ab2d-45bd-b46e-79a17b13f719"
origin_host: "MACMINI-RD.local"
---

**Context.** With termlib fully removed (below), every screen in clasm
is either a `huh` field or a `bubbletea` component, but they render as
two unrelated visual languages: `huh`'s default `ThemeCharm` is a
colorful indigo/fuchsia/cream card with a thick colored border;
`internal/tui`'s List/Picker/Manager chrome is plain, uncolored ASCII
box-drawing. The user asked to standardize chrome as the next pass
after termlib removal, once it was clear the two tiers had never been
visually reconciled.

**Decision.** Adopt a single shared accent color — the adaptive indigo
`ThemeCharm` already uses (`#5A56E0` light / `#7571F9` dark) — applied
two ways: a new `tui.Theme() *huh.Theme` (built from `huh.ThemeBase()`,
adding only the indigo accent to focused titles/borders/selection,
omitting `ThemeCharm`'s fuchsia/cream/green/red extras) wired into
every `huh.NewForm(...)` call site (traced: exactly five in the whole
app); and the same indigo + bold applied to `internal/tui/box.go`'s
shared border/title-rendering functions, which `ListViewModel`,
`PickerModel`, and `internal/filemanager` all call directly — one
change re-skins all three tiers. Cursor-row reverse-video and
instance-state green/red/yellow are left alone (selection/data
semantics, not branding). Also folded into this pass: `progress_ticker.
go`'s printed line becomes a real `bubbles/spinner`-based component
(explicitly deferred out of the termlib removal for exactly this
reason), and `object_browser.go`'s one remaining bare `huh.Select`
bucket picker moves onto the shared `pickBucket`/`PickerModel` — the
only bucket-selection call site that hadn't already converted in Phase
20.4.

**Rationale.**
- Reusing `ThemeCharm`'s own indigo (rather than inventing a new color)
  keeps continuity with what's already been on screen since Phase
  20.2, and it's already adaptive to light/dark terminal backgrounds —
  no new color-theory work needed.
- A single accent, not `ThemeCharm`'s full five-color set, matches an
  internal ops tool's understated register better than a colorful
  consumer-CLI look (DESIGN.md's own framing throughout: "internal tool
  for Library staff... not public-facing").
- `internal/tui/box.go` is the one place style changes apply to every
  tier at once, confirmed by checking that `internal/filemanager` calls
  the exact same exported functions `ListViewModel`/`PickerModel` call
  internally — no per-tier copy to keep in sync.
- `lipgloss` needs no new NO_COLOR/non-TTY plumbing — it already
  detects both via `termenv` and no-ops its own ANSI codes, so this
  doesn't duplicate or conflict with the existing manual
  `ui.ColorEnabled()` gate (which stays scoped to the STATE column it
  already governs).
- `bubbles/spinner` and `lipgloss` are not new dependencies in
  substance: `bubbles` is already a direct dependency (its `key`
  sub-package is used throughout the Menu tier), and `lipgloss` is
  already an indirect one via `huh`/`bubbletea` — this is drawing on
  the charm ecosystem this project already standardized on, not
  introducing a new library.

**Rejected alternatives.**
- *Restyle `huh` to match `tui`'s plain/monochrome look* — considered
  and explicitly not chosen (asked directly): would mean fighting
  `huh`'s own default rendering to strip color back out, rather than
  giving the plainer tier a real style. Bringing `tui` up to meet `huh`
  reuses more of what's already proven on screen.
- *Adopt `ThemeCharm` verbatim, no custom theme* — rejected: its
  fuchsia/cream/green/red extras elsewhere in the theme would still
  leave `tui`'s boxes with no equivalent color to match against for
  everything but the accent; a custom subset theme is barely more work
  and gives `tui` one clear color to mirror.

**Consequences.**
- A new `internal/tui` file defines the shared `huh.Theme` and the
  lipgloss styles `box.go` uses — `internal/tui` gains a `huh` import
  it didn't previously need (still no import cycle: `tui` imports
  nothing from `ui` or `workflow`).
- Five `huh.NewForm(...)` call sites gain `.WithTheme(tui.Theme())`.
- `progress_ticker.go`'s public shape (`startProgressTicker`) is
  replaced by a `bubbletea`-based equivalent; callers (`create_ami_
  from_instance.go`, `show_cloud_init.go`, `backup_archive.go`) update
  to the new call shape. Its `interval time.Duration` parameter is
  dropped rather than carried over: under the old `fmt.Fprintf`-per-
  tick design it meant "how often a new status line prints" (all three
  callers passed the same `30*time.Second`); under a real animated
  spinner that value would mean "how often the glyph advances," and
  30s is far too slow to read as an animation, so keeping the argument
  would leave it dead or silently wrong everywhere it's called. A
  package constant, `DefaultSpinnerInterval = 120*time.Millisecond`,
  replaces it.
- `object_browser.go` drops its bespoke `selectBucket` construction in
  favor of `pickBucket`; `huhCancelledIsNil` stays in use for
  `confirmLink`/the local-directory `huh.Input` in the same function.

---

