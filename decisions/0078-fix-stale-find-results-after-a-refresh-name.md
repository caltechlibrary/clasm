---
id: "0078"
title: "Fix stale Find results after a refresh; name single targets in confirm prompts; add an explicit manual refresh"
date: "2026-07-09"
status: accepted
kind: correction
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "b1ef1fbe-89b4-4de2-b3aa-9a03438d8efb"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-bucket testing (`test-clasm`) surfaced three more gaps
in the same session: after tagging and deleting some `.jsonl` objects
located via Find, the listing didn't reflect the deletion until some
later, unclear point; the delete confirm for a single object only said
"1 object(s)," not which one; and there was no direct answer to "how do
I get the window to update."

**Root cause 1 (stale Find results).** `pane.visible()` returns
`pane.find.results` -- a point-in-time flat snapshot -- whenever a Find
is active, completely bypassing `pane.entries` regardless of how
recently `entries` was refetched. The post-delete refresh
(`refreshAfterAction`) correctly reloaded `p.entries`, but the screen
kept showing the old Find snapshot until the operator manually pressed
Esc to exit Find. Fixed in the `listLoadedMsg` handler
(`model.go`): a successfully-applied (non-stale) listing for a pane now
always clears that pane's `find` too, since a fresh full-level listing
supersedes any earlier Find snapshot. Safe to do unconditionally: the
only loads that ever reach a pane while its `find` is still set are
post-action refreshes (ordinary navigation already clears `find` via
`pane.enter`), so there's no legitimate case where a landing reload
should leave a stale Find view in place.

**Root cause 2 (single-target confirms didn't name the target).**
Download/Upload/Delete/Sync's confirm prompts all read "N object(s)"/
"N file(s)," even for exactly one item -- a regression relative to
Feature 21's original single-object delete wizard, which did name the
object (`"Delete %s from %s?"`). Added `describeTargets`/`describeKeys`
(actions.go/sync.go): a single item's key/path is named directly;
multiple items still get a count. Applied to all four confirm prompts
(Download, Upload, Delete, Sync's two stages) for consistency, not just
the one the operator specifically flagged.

**Decision -- explicit manual refresh.** Even with root cause 1 fixed,
added `r` / `:refresh` (reloads the focused pane's current level,
clearing any active Find first) as a direct, always-available answer to
"how do I get the window to update" -- covers any future staleness
class this fix didn't anticipate, and covers the legitimate case of
something changing the bucket from outside this session entirely (the
AWS console, another terminal).

**Also clarified in this exchange, no code change needed:** switching
between one-pane and two-pane views is `l` -- prompts to link a
directory (opens double-pane) when unlinked, or goes straight to the
unlink confirm (collapses to single-pane) when already linked. The
hotkey legend now says "l Link (2-pane)" / "l Unlink (1-pane)" instead
of just "l Link"/"l Unlink" to make the pane-count effect explicit.

**Rejected alternatives.**
- *Prune only the deleted keys out of `find.results` instead of
  clearing `find` entirely* -- considered (would preserve the rest of
  the Find context); rejected for now as more moving parts than the
  bug warranted -- reopening the same search (`F`/`:find`) after a
  refresh is one keystroke, and the general "any landing reload clears
  find" rule is simpler to reason about and correct for every action,
  not just Delete.

**Consequences.** `model.go`'s `listLoadedMsg` handler, `actions.go`
(`describeTargets`, Download/Upload/Delete confirm titles, `r` hotkey,
`refreshFocused`), `commandline.go` (`:refresh`), `sync.go`
(`describeKeys`, both confirm titles), and `view.go` (legend wording)
all changed. New tests:
`TestModel_Delete_FromFindResultsRefreshesDisplay`,
`TestModel_Refresh_HotkeyReloadsFocusedPane`,
`TestModel_Refresh_ColonCommand`. Existing Download/Upload/Sync confirm-
title assertions updated to match the new single-item phrasing. All
tests pass; `go test -race ./...` clean.

---

