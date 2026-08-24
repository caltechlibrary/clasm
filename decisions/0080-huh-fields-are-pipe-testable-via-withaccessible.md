---
id: "0080"
title: "huh fields are pipe-testable via WithAccessible(true).WithInput/WithOutput"
date: "2026-07-10"
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
uuid: "85929f17-dcd4-4dfb-aecc-02db6f68fa88"
origin_host: "MACMINI-RD.local"
---

**Context.** `internal/workflow/object_browser.go` (the only existing huh
usage) has zero test coverage. huh's real interactive `Field.Run()`/
`Form.Run()` isn't pipe-testable the way `termlib`'s `newPipeEditor`
pattern is (see `confirm_test.go`). The prior session confirmed huh's
accessible-mode text format from source but left untried whether
`huh.NewForm(...).WithAccessible(true).WithInput(r).WithOutput(&buf).
Run()` -- forcing accessible mode explicitly rather than relying on
`TERM=dumb` auto-detection -- gives a clean, reliable pipe-testable
path. This had to resolve before converting `RunS3Menu` and the three
bucket-selection call sites (the next piece of work) to more untested
huh code.

**Decision.** Yes -- confirmed by direct experiment (a scratch test file,
written, run, and deleted this session; `go test`/`go vet` clean
throughout). `huh.NewForm(huh.NewGroup(field...)).WithAccessible(true).
WithInput(r).WithOutput(&buf).Run()` drives `Select`, `Confirm`, and
multi-field groups correctly from a plain `io.Reader`/`io.Writer` pair,
with no terminal or `bubbletea` program involved -- `Form.RunWithContext`
branches straight to `Form.runAccessible`, which calls each field's own
`RunAccessible` in turn. This is the pattern to use for the
S3-menu/bucket-selection conversions and retroactively for
`object_browser.go` -- **with one correction to this same session's
earlier finding**, below.

Confirmed behavioral details worth keeping in mind writing those tests:
- **Correction, found while implementing the `RunS3Menu` conversion:**
  `r` must NOT be a `strings.NewReader`-style reader that returns
  everything it has in one `Read` call. `accessibility.PromptString`
  builds a brand-new `bufio.Scanner` on every single `RunAccessible`
  call (there's no persistent, reused scanner the way `termlib`'s
  `LineEditor` has one for its whole lifetime) -- so if the *first*
  field's `Read` call greedily returns every remaining byte (as
  `strings.Reader.Read` does when its buffer is smaller than the
  request), that Scanner buffers and then discards everything past the
  first newline when it returns, silently starving every field after it
  -- both across two separate `Form.Run()` calls in a loop (as
  `RunS3Menu` makes, once per menu redisplay) and within a single
  multi-field `Form` (as `object_browser.go`'s three-field pre-flight
  makes). This was NOT caught by this session's first three
  experiments, because each one's second/third field happened to
  assert a value that coincided with that field's own zero-input
  default -- a false-positive risk worth flagging on its own. Confirmed
  the actual bug with a repro using a *non-default* expected second
  value, and confirmed the fix: use a reader that returns at most one
  newline-terminated line per `Read` call (matching how a real terminal
  in canonical mode delivers input, one `Read` per Enter keypress) --
  implemented as `lineAtATimeReader`/`newHuhAccessibleInput` in
  `internal/workflow/huh_accessible_test.go`, reusable across every huh
  call site's tests. Use `newHuhAccessibleInput(s)`, never
  `strings.NewReader(s)`, when feeding more than one field/prompt
  through this pattern.
- `Select.RunAccessible` reprompts on out-of-range input, writing
  `"Invalid: must be a number between %d and %d"` before re-asking --
  same reprompt-until-valid shape as `newPipeEditor`'s `Confirm` tests.
- A `Select` field backed by a pointer `Value(&v)` accessor has an
  implicit default (its initially-selected option, normally index 0),
  which `PromptInt` returns on a blank line -- so premature EOF is not
  the same as "no value set"; it silently resolves to that default
  rather than erroring. Confirmed via `accessibility.PromptString`'s own
  comment: `"no way to bubble up errors or signal cancellation ... but
  the program is probably not continuing if stdin sent EOF"`. Test
  input must supply one complete line per field; don't rely on EOF as
  an error signal, and don't assert a value that coincides with this
  default (see the correction above -- it masks real bugs).
- `Form.runAccessible` discards each field's own `RunAccessible` error
  return (`_ = field.WithAccessible(true).RunAccessible(w, r)`) --
  harmless in practice since each field's own `RunAccessible` already
  loops internally until it gets a valid value, so it only returns once
  successful.
- `huh.ErrUserAborted` (the normal-mode Ctrl-C/Esc signal that
  `huhCancelledIsNil` maps to a clean return) has no accessible-mode
  equivalent -- there is no keyboard to interrupt a plain
  `io.Reader`/`io.Writer` pair, so cancellation-path tests for
  huh-backed menus need a different signal than "user aborted" if one
  is needed at all (see "Convert RunS3Menu to huh.Select" below for how
  this was handled: unit-test the abort-mapping as a standalone pure
  function instead of through the pipe path).

**Rejected alternatives.**
- *Rely on `TERM=dumb` auto-detection instead of explicit
  `WithAccessible(true)`* -- works too (`NewForm` sets it automatically
  when `TERM=dumb`), but requires mutating the test process's
  environment (`t.Setenv("TERM", "dumb")`), which is less explicit and
  risks leaking into unrelated parallel tests; calling
  `WithAccessible(true)` directly is scoped to the one `*Form` under
  test.
- *Test only via `Field.RunAccessible` directly, skip `Form`* --
  considered, since `Field.RunAccessible` is itself public and
  no longer just an internal implementation detail (`WithAccessible`
  is now deprecated in its favor per the huh v1.0.0 source). Rejected
  for the *multi-field* menu/pre-flight tests specifically, since
  `RunS3Menu`'s conversion and `object_browser.go`'s existing pre-flight
  both group multiple fields in one `huh.Group`, and `Form` is what
  sequences them in production code -- testing through `Form` keeps the
  test closer to the real call path.

**Consequences.** Unblocks converting `RunS3Menu`
(`internal/workflow/s3_menu.go`) and the three `ui.PickList`
bucket-selection call sites
(`bucket_website.go`/`bucket_lifecycle.go`/`bucket_delete.go`) to
`huh.Select` with test coverage from the start, and backfilling
`object_browser.go`'s existing zero coverage using the same pattern.
No production code changed by this decision -- it's a testing-approach
resolution only.

---

