---
id: "0074"
title: "Fix two real-bucket navigation bugs and add a scrolling window to the file manager's pane listings"
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
uuid: "92fce755-dc16-4afd-b0da-ffb7f3d43b70"
origin_host: "MACMINI-RD.local"
---

**Context.** Trying the file manager against a real bucket
(`s3://thesis.caltech.edu`) surfaced two more real bugs beyond the
previous UX pass: the object listing had no way to scroll down to reach
an entry past the first screenful (specifically, `opensearch.xml`), and
navigating back up out of a drilled-into subdirectory didn't reliably
reach the root.

**Root cause 1 (no pagination/scrolling).** `View()` never bounded pane
listings to the terminal height at all -- every visible entry became a
box row, unconditionally. A bucket-root listing longer than one
screenful pushed the status line, command line, and hotkey legend off
the bottom of the terminal, with nothing to bring them back into view;
worse, an entry past the first screenful (`opensearch.xml`, sorting
after many `file-*`-style keys) was simply never rendered at all, with
no scroll key or indicator suggesting more existed. Fixed by adding
`paneItemWindowHeight` (derives a row budget from `m.height` minus a
fixed chrome-row count) and `scrollWindow` (keeps the cursor inside a
windowHeight-tall viewport, centering when there's room) -- the listing
now scrolls with the existing Up/Down keys, with a "[a-b of n]"
indicator on the pane header once it doesn't all fit. Overlay progress
logs (Upload/Download/Delete/Sync against many objects) get the same
treatment, tail-windowed rather than cursor-centered, since a log's
natural reading position is its most recent lines.

**Root cause 2 (broken "back to root" navigation) -- two distinct bugs,
both in prefix/path handling:**
- `parentOf` (now `parentOfS3Prefix` for the remote side) stripped the
  trailing slash from a nested S3 prefix's parent (`"logs/sub/"` ->
  `"logs"` instead of `"logs/"`). The next `s3:ListObjectsV2` call then
  used a bare string-prefix match instead of a directory-boundary one,
  silently hiding every bucket-root object that didn't happen to start
  with the literal string `"logs"` -- so "go up a level" landed on a
  corrupted, mostly-empty intermediate view instead of the actual
  parent directory.
- The local pane conflated two different path representations:
  `pane.prefix` is documented (and used by `loadLocalCmd` via
  `joinKey(root, prefix)`) as **root-relative**, but entering a local
  subdirectory assigned the entry's **absolute** filesystem path
  directly to `prefix`. One level deep this went unnoticed; a second
  level built a doubled, malformed path
  (`root + "/" + absolute-path-that-already-contains-root`), breaking
  navigation (including back to the linked root) beyond the first
  subdirectory.

  Fixed with `pane.toPrefix`/`pane.parentPrefixOf`, which convert an
  entry's identity (`entry.key` -- already bucket-relative for the
  remote side, an absolute path for the local side, per `entry`'s own
  doc comment) into the *pane's* prefix representation before it's
  assigned to `pane.prefix`, instead of assuming the two were always
  the same shape.

**Rejected alternatives.**
- *Persist a per-pane scroll offset in the Model, updated on every
  cursor move* -- considered; rejected in favor of computing the
  window as a pure function of `(cursor, total, windowHeight)` on every
  `View()` call. Simpler (no extra mutable state to keep in sync with
  cursor movement, filtering, or directory changes) and just as
  correct, since the window only ever depends on where the cursor
  currently is.
- *Give the local pane its own absolute-path-based prefix format
  instead of converting to root-relative* -- rejected; `pane.label()`
  and every action that reconstructs a full path via
  `joinKey(root, prefix)` depend on `prefix` being root-relative, and
  changing that contract everywhere would be a much larger change than
  fixing the one place (`navigateEnterOrJump`) that violated it.

**Consequences.** `internal/filemanager/entry.go`'s `parentOf` is split
into `parentOfLocal` (unchanged logic, correct for the local side's
no-trailing-slash convention) and `parentOfS3Prefix` (new, trailing-
slash-preserving). `pane.go` gained `toPrefix`/`parentPrefixOf`; `up()`
is now side-aware. `view.go` gained the scroll-window machinery.
New tests: `scroll_test.go` (scroll-window bounds, a 500-item listing
staying bounded to the terminal height, an entry past the first
screenful becoming reachable), `navigation_test.go` (both navigation
bugs, reproduced against the pre-fix code by hand-tracing the exact
failure before writing the fix). All pre-existing tests pass unchanged.

---

