---
id: "0076"
title: "Give Filter the same \"/\"-anchored pattern convention as Find"
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
uuid: "846a5882-271d-4319-a759-cb1bb83a7f24"
origin_host: "MACMINI-RD.local"
---

**Context.** Typing `/index.html` into the current-level Filter (`f`)
had no visible effect: Filter is (correctly) a plain substring match,
so the literal text `/index.html` -- including the slash -- was
compared against basenames like `index.html`, which never contains a
`/` and so never matched. The operator's expectation, reasonably, was
that the `/`-anchor convention just added to Find (the previous
2026-07-09 entry) would mean the same thing here too, since both
features are typed the same way (a pattern following a `/`) and are
listed next to each other in the hotkey legend.

**Decision.** A filter starting with `/` is now matched via
`globMatch`'s anchored form -- reusing the exact function Find already
uses, not a second implementation -- instead of plain substring
`Contains`. Since Filter only ever operates on one already-fetched
level (not a recursive path), this collapses to an exact/glob match of
the current level's basenames: `/index.html` matches only a file named
exactly that, not `myindex.html5` (which a plain substring filter for
`index.html` would have matched too). Filter without a leading `/`
keeps its original substring behavior unchanged.

**On the spinner:** confirmed with the operator that Filter correctly
shows no spinner -- it's a synchronous, instant operation over already-
loaded rows, unlike Find's recursive scan, so there's nothing to
animate. Not a bug; Find's spinner (previous entry) already covers the
one operation here that can genuinely take a while.

**Consequences.** `pane.visible()` branches on a leading `/` before
falling back to substring matching. New test:
`TestPane_Visible_AnchoredFilterMatchesExactBasenameOnly`
(`pane_test.go`). All pre-existing tests, including
`TestModel_Filter_NarrowsCurrentLevel` (the original substring case),
pass unchanged.

---

