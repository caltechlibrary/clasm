---
id: "0156"
title: "Poll-loop progress output: fix `PollSnapshotUntilComplete` ahead of Phase 20.51's sibling poller"
date: "2026-08-18"
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
uuid: "77420dc3-4f07-4434-b640-abc2cbbc3837"
origin_host: "MACMINI-RD.local"
---

**Context.** `PollSnapshotUntilComplete` (Phase 20.49) prints nothing
while it waits for OpenSearch to finish creating a snapshot -- the
terminal shows whatever was last drawn (typically a picker's own hint
line) throughout the wait. Confirmed live 2026-08-17 against
CaltechAUTHORS production: a real 59-shard/~6.4GB snapshot took ~3
minutes, during which the terminal looked identical to a hung prompt.
Not a correctness bug -- the workflow completes correctly either way --
but the user explicitly asked to close this UX gap before the next
release (TODO.md, "target: 2026-08-18").

**Decision.** Thread an `io.Writer` into the poll loop and print
progress on every tick, not just report the end result: one line before
the loop starts ("waiting for snapshot ... to complete -- this can take
several minutes for a large index set") and one line per `pollInterval`
tick showing elapsed time. Fix this now, ahead of implementing Phase
20.51's `PollRestoreUntilComplete` (PLAN.md Phase 20.51, work item 11) --
that new sibling poller has the identical polling shape, so factoring the
print-on-tick behavior into a small shared helper both functions call
means Phase 20.51 inherits progress output for free instead of repeating
the same gap a fourth time (the third time, counting the earlier
"silent-scroll" bug class this project has already hit twice, is a
different mechanism but the same root lesson: a redraw/wait with no
visible progress reads as broken).

**Rationale.**
- Scoped to the OpenSearch snapshot/restore polling family only -- other
  poll loops in this codebase (`WaitForSSMOnline`, `checkCloudInitCompletion`)
  already have their own established UX (a spinner or a fixed one-shot
  wait) and haven't been reported as confusing in practice.

**Consequences.** See PLAN.md Phase 20.53, DESIGN.md "Poll-Loop Progress
Output: OpenSearch Snapshot/Restore Polling."

---

