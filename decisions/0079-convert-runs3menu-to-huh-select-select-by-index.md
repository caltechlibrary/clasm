---
id: "0079"
title: "Convert RunS3Menu to huh.Select; select by index, not by s3Item"
date: "2026-07-10"
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
uuid: "54879e94-8625-431f-badb-cccc36763590"
origin_host: "MACMINI-RD.local"
---

**Context.** `continue_next_time.txt`'s next-up item, now unblocked by
the pipe-testability resolution above: convert `RunS3Menu`
(`internal/workflow/s3_menu.go`), the S3 domain's 7-item action picker,
from `ui.PickList` to `huh.Select`. Two problems came up that the prior
session's scoping didn't anticipate:

1. `huh.Select[T]` requires `T comparable` (`Option[T comparable]`,
   confirmed from source), but `s3Item` holds an `action func(S3Actions,
   context.Context) error` field -- funcs aren't comparable, so
   `huh.Select[s3Item]` doesn't compile.
2. Converting the picker changes what "cancel/abort the menu" does.
   `ui.PickList`'s dedicated "0) Cancel" numbered option returned
   `ui.ErrCancelled`, which `isExitSignal` recognizes -- so pressing 0
   exited the whole program. `huh.Select` has no such numbered-cancel
   convention; its only cancellation signal is `huh.ErrUserAborted`
   (Ctrl-C/Esc), which `isExitSignal` does NOT recognize, so left
   unhandled it would propagate out of `RunS3Menu` entirely and still
   exit the whole program -- not the behavior the prior session asked
   for ("change this so aborting the newly-huh-converted top-level S3
   menu returns `ErrBackToDomainPicker` instead").

**Decision.**
- Select by `int` (index into `s3MenuItems`), not by `s3Item`:
  `pickS3MenuItem` builds `huh.Option[int]` from each item's label and
  its index, runs the `Select`, then looks up `s3MenuItems[idx]` after.
  Sidesteps the comparability constraint without changing `s3Item`'s
  shape (still holds a `func`, used everywhere else in this file).
- `RunS3Menu`'s exported signature is unchanged
  (`ctx, t, le, actions`), matching `RunMainMenu`/`RunKeyMgmtMenu`'s
  shape -- it now delegates to an unexported `runS3Menu(ctx, t, actions,
  menuInput, menuOutput)`. `le` is accepted but unused (huh doesn't read
  through it); a doc comment says so explicitly rather than leaving a
  reader to wonder if that's a bug. `menuInput`/`menuOutput` are nil in
  production (the picker runs interactively via `pickS3MenuItem`'s bare
  `form.Run()`, same as `object_browser.go`'s existing call sites) and
  are supplied by tests to drive the exact same `huh.Select` through the
  accessible-mode pipe path instead -- keeping the tested path identical
  to the production path, not a parallel fake.
- `huh.ErrUserAborted` from the picker maps to `ErrBackToDomainPicker`
  via a new pure function, `mapS3MenuPickerErr`, rather than an inline
  check -- because accessible mode (the only path integration tests can
  drive; see the pipe-testability entry above) has no way to produce
  `huh.ErrUserAborted` at all, this mapping can only be covered by
  calling the pure function directly with a synthetic error, not by
  driving `runS3Menu` end-to-end. Said so explicitly (this note) rather
  than shipping the mapping uncovered.
- `s3_menu_test.go`'s old `TestRunS3Menu_CleanExitOnCancelledPickList`
  (input `"0\n"`, asserting a clean whole-program exit) tested a
  `PickList`-specific affordance that no longer exists and asserted the
  *old*, now-deliberately-changed behavior -- removed rather than kept
  as a skipped/misleading test. Its replacement is
  `TestMapS3MenuPickerErr`.
- Every other `s3_menu_test.go` case kept its exact input strings
  (`"2\n7\n"`, `"3\n7\n"`, etc.) -- `huh.Select`'s accessible-mode
  1-indexed numbering happens to match `s3MenuItems`' order exactly, no
  renumbering needed. They now call `runS3Menu` directly (unexported)
  instead of `RunS3Menu`, with a `newTermOnly()` helper in place of
  `newPipeEditor` (no `LineEditor`/pipe needed for `t` now that the
  picker doesn't read through it) and `newHuhAccessibleInput` in place
  of raw strings for the menu's input.

**Rejected alternatives.**
- *Keep `isExitSignal`'s current "abort exits the whole program"
  behavior, defer the wording fix to a later pass* -- rejected because
  the prior session scoped the abort-behavior change as part of this
  same piece of work, not a separate one (this repo's own task list
  keeps the label-wording pass, e.g. "quit" vs. "cancel" text,
  separate, but the *behavior* change was explicitly bundled here).
- *Give `s3Item` an `Equal` method or make `action` a comparable
  reference (e.g. an int action-code) instead of switching to
  index-based selection* -- more invasive (every existing
  `s3MenuItems` literal and every call site that pattern-matches on
  `choice.action == nil` for "Back to domain picker" would need to
  change); index-based selection achieves the same result by touching
  only `pickS3MenuItem`.
- *Drop the now-unused `le` parameter from `RunS3Menu`* -- would ripple
  into `main.go`'s one call site for no functional benefit, and would
  make `RunS3Menu`'s signature diverge from `RunMainMenu`/
  `RunKeyMgmtMenu`'s while those two remain on `termlib`. Deferred until
  (if ever) all three domain loops are off `termlib`'s `PickList`.

**Consequences.** `internal/ui` (`PickList`, `ErrCancelled`) is
untouched and still used by `RunMainMenu`/`RunKeyMgmtMenu`/
`RunDomainPicker` -- this conversion is scoped to the S3 menu only, per
the prior session's explicit "don't let this expand" note. `go build`,
`go vet`, and `go test ./... -race` are clean. Next up, unstarted: the
three bucket-selection call sites
(`bucket_website.go`/`bucket_lifecycle.go`/`bucket_delete.go`), which
select `inventory.Bucket` values (already comparable, no func fields)
so shouldn't need the same index-based workaround.

---

