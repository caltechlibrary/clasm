---
id: "0065"
title: "File manager Find: recursive glob-on-basename search, not full-path glob or regex"
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
uuid: "598fc208-6b1f-4318-9ae9-05e36c3a6b69"
origin_host: "MACMINI-RD.local"
---

**Context.** The file manager needed a way to search recursively across
a directory/bucket subtree by name pattern (e.g. `*.go`, or `\.git` to
find git repository directories) -- a different operation from the
per-level substring filter (`f`), which only ever looks at what's
already listed at the current level.

**Decision.** Match a shell glob pattern (Go stdlib
`path/filepath.Match` semantics, including backslash-escaping) against
each entry's basename, evaluated recursively at every depth below the
focused pane's current position -- the same behavior as `find <dir>
-name '<pattern>'`. Both motivating examples (`*.go`, `\.git`) already
work under this exact semantics with no further feature needed.

**Rejected alternatives.** *Regex pattern support* -- no concrete case
surfaced that a shell glob can't already express; not built, revisit if
one comes up. *Full-path glob matching (e.g. `**`-style patterns
spanning directory separators)* -- unnecessary given per-basename
matching during a recursive walk already satisfies both stated examples
and matches `find -name`'s well-understood behavior; adding
path-spanning glob syntax would be new complexity solving a problem
that hasn't come up. *Search from the tree root always* -- rejected in
favor of starting from the focused pane's current position, matching
`find`'s own convention and avoiding an unbounded scan when the operator
only meant to search what they're currently looking at.

**Consequences.** S3-side Find pays the cost of a full recursive
`ListObjectsV2` (no `Delimiter`) under the current prefix when invoked
-- the same cost Feature 20 (Sync) and the old delete-by-prefix case
already paid, just now on-demand and user-triggered rather than
automatic every time the workflow runs. Needs a cancellable, live
progress indicator for large subtrees (DESIGN.md 21.7, `PLAN.md` Phase
20.1).

---

