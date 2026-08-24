---
id: "0072"
title: "Extract `internal/s3diff`; add a dedicated Sync action to the file manager; use `x/exp/teatest` for `Model` tests"
date: "2026-07-09"
status: accepted
kind: decision
trigger: implementation
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "664b3104-d795-4a7a-b719-645be0cee81f"
origin_host: "MACMINI-RD.local"
---

**Context.** Implementing PLAN.md Phase 20.1 (this file's earlier
2026-07-09 entries designed it) surfaced three implementation-time
decisions the design pass didn't fully settle.

**Decision 1 — `internal/s3diff` package.** The plan's file list said
`bucket_sync.go`'s diff/walk/list helpers (`diffSync`, `walkLocalTree`,
`listAllBucketObjects`, `contentTypeFor`) would stay in
`internal/workflow`, "reused, not rewritten," by the new screen. That
doesn't fit Go's import rules once `internal/workflow/object_browser.go`
needs to call into `internal/filemanager` to launch the screen:
`filemanager` can't import back into `workflow` without a cycle. Moved
these helpers into a new, lower-level `internal/s3diff` package that
both `internal/workflow` (implicitly, via retirement -- see Decision 3)
and `internal/filemanager` depend on, preserving genuine code reuse
(same functions, not duplicated) without a cycle.

**Decision 2 — a dedicated Sync action, not just manual tag-and-act.**
The file manager as first built (single-pane, then double-pane with
manual per-directory tag-and-Upload/Download) covered Feature 21's old
single-object case and the old bulk-delete-by-prefix case, but not
Feature 20's automatic whole-tree diff (compute upload/delete candidates
by comparing an entire local directory against the entire bucket, dry
run, two-stage confirm). Flagged to the user as a real gap against this
file's own 2026-07-09 "Design the S3 object management UI/UX pass"
entry (Decision 2 there: "Sync's directory-mirroring workflow is kept
as a first-class, directly reachable capability"). The user chose to
build it properly rather than ship with manual tag-and-act as the only
path. Added a `S`/`:sync` action (DESIGN.md 21.6) reusing
`internal/s3diff.Compute`/`WalkLocalTree`/`ListAllBucketObjects` against
the *entire* linked directory and *entire* bucket (not scoped to either
pane's current navigated position) — matching the retired wizard's own
semantics exactly, gated by the same never-bundled upload-then-delete
two-stage confirm (Security Consideration #11).

**Decision 3 — retire `bucket_sync.go`'s wizard, not just
`bucket_browse.go`/`bucket_delete_objects.go`.** PLAN.md's work items
only named the latter two for retirement. Once Sync became a directly
reachable file-manager action (Decision 2) with real parity, keeping
`SyncDirectoryToBucket` around as dead, unreachable code (no menu entry
dispatches to it any more) would violate this project's own practice of
deleting confirmed-unused code rather than leaving stale copies
(`CLAUDE.md`). Deleted `bucket_sync.go` and `bucket_sync_test.go`
outright; their diff logic lives on in `internal/s3diff` (Decision 1),
tested there instead.

**Decision 4 — `x/exp/teatest` resolves PLAN.md's open testing
question.** Confirmed real and usable by pulling it into the module
(`go get github.com/charmbracelet/x/exp/teatest`) and driving the
`Model` through it, per this project's standing evaluation discipline.
`teatest.NewTestModel` runs the `Model` as an actual `bubbletea.Program`
against an in-memory terminal; `.Send` injects `tea.Msg`s (key
presses); `teatest.WaitFor` polls the rendered output for a substring.
One caveat worth recording: bubbletea's renderer only retransmits
screen lines that changed since the previous frame, so two sequential
`WaitFor` calls checking *different* substrings can race if both
substrings were already present in one earlier, since-drained frame —
check multiple substrings in a single `WaitFor` condition (or assert on
a status line's derived text) rather than assuming later calls see
everything still on screen. `go test -race` also caught one genuine
concurrency bug this pattern makes visible that a non-race-checked
manual test would have missed: `runDelete`'s background goroutine
called `pane.clearTags()` directly (a Model mutation) instead of only
sending text over its progress channel, racing with the render loop's
concurrent read of the same map. Fixed by moving that mutation into the
overlay-dismiss key handler, which runs on `Update`'s single goroutine
— the general rule going forward for this `Model`: background
goroutines started by an action may only ever send `progressLine`
values over a channel, never touch `Model`/`pane` fields directly.

**Rejected alternatives.**
- *Duplicate the diff helpers in `internal/filemanager` instead of
  extracting a shared package* — considered, given the plan's literal
  wording implied `bucket_sync.go` would stay put; rejected once it
  became clear that would mean two copies of the same key+size diff
  logic drifting apart over time, against this project's stated
  preference for simplicity over duplication.
- *Ship without a dedicated Sync action, relying on manual
  tag-everything-then-Upload* — genuinely workable and was the default
  path until the user was asked; rejected because it silently drops the
  auto-diff (only-changed-files) behavior the original wizard had, and
  doesn't literally satisfy the earlier Decision 2 commitment.
- *Write `Model` tests by manually driving `Update`/executing returned
  `tea.Cmd`s synchronously, skipping `teatest` entirely* — considered
  when `teatest`'s output-diffing first produced two flaky-looking
  failures; rejected once the actual cause (draining + unchanged-line
  suppression, not a fundamentally unreliable tool) was root-caused --
  `teatest` works well once that behavior is understood and tests
  assert accordingly.

**Consequences.** `internal/s3diff` is new, with its own tests
(`s3diff_test.go`). `internal/workflow/bucket_sync.go`/
`bucket_sync_test.go` are deleted. `internal/filemanager` gained
`sync.go`/`sync_test.go` and a `S`/`:sync` hotkey/command. PLAN.md Phase
20.1's file list and work-item checkboxes, and DESIGN.md's 21.6 section,
are updated to match what was actually built.

---

