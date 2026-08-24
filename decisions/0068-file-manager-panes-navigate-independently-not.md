---
id: "0068"
title: "File manager panes navigate independently, not synced to a shared relative path"
date: "2026-07-09"
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
uuid: "422036e5-1377-4dff-b485-87005ae5ba21"
origin_host: "MACMINI-RD.local"
---

**Context.** Double-pane mode could either lock both panes to the same
relative subfolder (so every listing is inherently a live diff,
reinforcing "these two trees should match") or let each side browse
anywhere independently, like a traditional dual-pane file manager
(Midnight Commander, WinSCP).

**Decision.** Independent navigation. Tagging happens in whichever pane
has focus; an action's destination is the other pane's current
position, not a shared path both panes must already agree on.

**Rejected alternatives.** *Synced navigation* (both panes always
mirror the same subfolder, every row annotated with an
upload/download/in-sync/conflict badge) -- rejected; it's a better fit
for "reconcile these two trees" specifically, but forecloses the more
general dual-pane use case (e.g. uploading from one local folder into
an unrelated bucket prefix that doesn't mirror it), and conflicts with
the tag-in-focused-pane/act-on-other-pane convention already decided,
which assumes the destination is wherever that pane happens to be
pointed.

**Consequences.** Diff-style badges (upload/download/in-sync) are not a
core mechanic of ordinary browsing -- they only apply within Sync's own
directory-mirroring workflow (DESIGN.md Feature 20, reachable through
this same screen), not to double-pane browsing in general.

---

