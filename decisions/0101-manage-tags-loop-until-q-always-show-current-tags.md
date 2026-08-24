---
id: "0101"
title: "Manage Tags: loop until 'q', always show current tags, add a Show tags choice"
date: "2026-07-20"
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
uuid: "41067b27-d12c-4dd4-a6c6-86a9e32a7125"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md bug, reported directly: Manage Tags is missing a
"show tags" menu option, and "the tags shown at the top of the screen
don't update on change." The existing flow displayed tags once, applied
exactly one Add/Update/Remove, and exited -- so a second look at the
same resource's tags required leaving and re-entering the whole
workflow, and there was no way to just look without also being forced
into picking Add/Update/Remove.

**Decision.** `manageTagsForResource` becomes a loop: display current
tags (freshly fetched, not the original snapshot) -> pick an action
(now four: Show tags/Add/Update/Remove) -> act -> loop, until the
operator cancels. "Show tags" is deliberately close to a no-op (tags
are already re-shown every iteration) -- it exists because the operator
asked for it by name, not because the display was otherwise hidden.
The single-change logic (`applyOneTagChange`) is extracted from the
loop so it stays directly unit-testable on its own.

**Rationale.**
- Looping until 'q' matches this codebase's own established convention
  for action menus (`RunMainMenu`, `RunS3Menu`, `RunKeyMgmtMenu` all
  loop the same way) rather than introducing a one-off exception for
  Manage Tags.
- Re-fetching tags after every change (not reusing the pre-change
  snapshot) is the actual content of the bug fix -- looping alone
  wouldn't have helped if the redisplayed data was stale.

**A real, non-obvious finding worth recording.** huh's own
accessible-mode `Select` (used throughout this package's tests) cannot
signal "the input pipe is exhausted" as an error. Confirmed by reading
`internal/accessibility.PromptString` (huh v1.0.0) directly, not
assumed: on `scanner.Scan()` returning false, it silently falls back to
the field's default value and returns nil -- the package's own comment
there says as much ("no way to bubble up errors or signal
cancellation... but the program is probably not continuing if stdin
sent EOF"), an assumption that doesn't hold for a *looping* workflow
re-entering the same accessible-mode prompt more than once. A first
attempt at this loop relied on that exhaustion to end a test (matching
this package's usual `cancelledIsNil`/`io.EOF` convention) and instead
spun forever, silently re-selecting "Show tags" (option 1, the
resulting default) and reconstructing a `huh.Form` on every iteration --
caught via `go test -timeout` plus a goroutine dump showing the loop
"runnable" (CPU-bound in form construction), not blocked on I/O, not
assumed from reading the code alone.

**Fix for the above:** a `ctx.Err()` check at the top of the loop,
matching `runMainMenu`'s own convention in `menu.go` -- and, unlike that
precedent, actually load-bearing here rather than just stylistic
consistency. Tests cancel `ctx` explicitly at the exact point they want
the loop to end (`cancelAfterNFetches`, adapting `menu_test.go`'s own
`cancelingAction` pattern to trigger from a data-fetch closure instead
of a dispatched menu action), rather than relying on scripted input
running out.

**Rejected alternatives.**
- *Keep the one-shot behavior, just refresh tags before displaying them
  next time the operator re-enters Manage Tags* -- technically closes
  the letter of the bug (a fresh entry would show current data) but not
  the spirit of it: the operator's own report describes wanting to see
  the result without leaving the screen.
- *Rely on exhausted test input to end the loop* -- the initial
  approach; abandoned once shown to hang indefinitely rather than
  error, per the finding above.

**Consequences.** `manageTags` now builds and threads a per-kind
`fetchTags` closure into `manageTagsForResource`. `isCancellation`
extracted from `cancelledIsNil`'s existing check and widened to include
`io.EOF`, matching `isExitSignal`'s (menu.go) already-broader
definition -- a small, harmless generalization, though not what
actually fixed the hang (the ctx.Err() check did). New
`statefulTagsFakeEC2Client` (manage_tags_test.go) -- unlike the shared
`fakeEC2Client`, it actually tracks tag state across `CreateTags`/
`DescribeInstances` calls, needed to prove the refresh-after-change
behavior in a test. See `PLAN.md` Phase 20.29.

---

