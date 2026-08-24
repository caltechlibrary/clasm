---
id: "0075"
title: "Add a loading spinner and an anchored Find pattern; fix a spinner/synchronous-test-drain interaction"
date: "2026-07-09"
status: accepted
kind: decision
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
uuid: "10b9791c-bb34-4367-a3b6-0cbde51bb730"
origin_host: "MACMINI-RD.local"
---

**Context.** Two more requests after trying the file manager against a
real bucket: Find and directory listings can take a real, noticeable
amount of time with no feedback that anything is happening (looks
frozen); and there was no way to search for a root-level file (e.g.
`index.html`) without also matching every same-named file in
subdirectories.

**Decision 1 -- loading spinner.** Added `github.com/charmbracelet/
bubbles/spinner` (already a transitive dependency via huh; now direct).
A pane's header shows an animated glyph + "Loading..." while its
listing is being (re)fetched (`Model.loadingRemote`/`loadingLocal`);
Find's status row shows the same glyph while a search hasn't finished
(`pane.find.done`). The spinner only ticks while `Model.isBusy()` is
true: `loadRemoteCmd`/`loadLocalCmd`/`runFind` each batch in a fresh
`spinner.Tick` to (re)start the animation, and the `spinner.TickMsg`
handler drops its own re-tick `Cmd` once nothing is busy, rather than
ticking forever. This was a real functional requirement, not just
efficiency: a bubbletea `Model` driven synchronously (no real
`tea.Program`, no real timers -- see this project's own test pattern,
`drainCmd`) would never terminate against a perpetually-ticking
spinner.

**Decision 1's follow-on bug and fix.** Even with the isBusy() gate,
`drainCmd`-based tests hung. Root cause: `Init()`'s returned
`tea.Batch` nests one sub-batch per pane
(`loadRemoteCmd`/`loadLocalCmd`, each itself now `tea.Batch(fetch,
tick)`), and `drainCmd` drains one batch branch all the way through
before moving to the next -- so while draining the remote pane's
branch, `loadingLocal` (already set the moment `Init()` *constructed*
the local branch's Cmd, before either branch actually ran) hadn't been
cleared yet, since that happens in the *other*, not-yet-visited branch.
`isBusy()` therefore saw stale state for the entire depth of the first
branch, and kept chasing tick -> tick -> tick forever. A real
`tea.Program` doesn't have this problem: it runs every in-flight `Cmd`
concurrently, so by the time a real spinner tick fires (tens of
milliseconds later), the sibling pane's load has typically already
resolved. Fixed at the test-helper level, not in production code (the
production isBusy()-gating is correct for real concurrent execution):
`drainCmd` now processes a `spinner.TickMsg` once (so `Update`'s
bookkeeping runs) but never chases the `Cmd` it returns -- ticks are
purely cosmetic and don't affect anything a test asserts on.

**Decision 2 -- anchored Find pattern.** A pattern starting with `/` is
now matched against an entry's *full* path (relative to the search's
starting point) instead of just its basename -- `/index.html` matches
only a root-level `index.html`, `/sub/index.html` matches only that
exact nested one, `/*.html` matches only root-level `.html` files
(`filepath.Match`'s `*` still doesn't cross `/`). Implemented as one
branch in `globMatch`, since both the local and S3 recursive listings
(`listLocalRecursive`/`listS3Recursive`) already carry each entry's
full relative path in `entry.name` -- no new traversal or data needed,
just a different match target.

**Rejected alternatives.**
- *Always tick the spinner, never stop* -- the original 2026-07-09 UX
  pass's approach; rejected once it surfaced the synchronous-test-drain
  hang above, and it wastes idle redraws in real usage too for no
  benefit.
- *Fix the hang by having Update clear loadingLocal/loadingRemote
  eagerly at Init() time instead of when their fetch actually starts* --
  rejected; the flags exist specifically to reflect whether a fetch is
  genuinely in flight, and weakening that to work around a test-only
  ordering artifact would make the flags lie in the one case (a
  slow-loading pane) they're supposed to represent correctly.
- *Require the anchor to be a full path with no wildcards (exact
  match only)* -- rejected; reusing plain `filepath.Match` on the full
  path costs nothing extra and lets `/*.html`-style single-level globs
  work too, consistent with the unanchored form already supporting
  globs.

**Consequences.** `internal/filemanager/model.go` gained `spin
spinner.Model`, `loadingRemote`/`loadingLocal`, `setLoading`/
`isLoading`/`isBusy`; `view.go`'s `paneRows` takes `loading bool, spin
string`; `listing.go`'s `globMatch` gained the anchored branch.
`go.mod` moved `github.com/charmbracelet/bubbles` from indirect to
direct. New tests: `box_test.go` (loading/spinner indicator presence),
`entry_test.go`/`model_test.go` (anchored pattern, unit and end-to-end).
`testhelpers_test.go`'s `drainCmd` no longer chases `spinner.TickMsg`
chains. All pre-existing tests pass unchanged; `go test -race ./...`
clean.

---

