---
id: "0089"
title: "Remove termlib entirely: input via huh, output via io.Writer"
date: "2026-07-13"
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
uuid: "bb08237a-0c44-4da1-9c3e-8799a502310c"
origin_host: "MACMINI-RD.local"
---

**Context.** The Menu/Picker/List tier conversion (Phase 20.2-20.14) is
done, but `termlib` itself is still in `go.mod` with ~44 files importing
it — every action wizard's free-text/confirm prompts, plus
`internal/ui`'s lower-level helpers (`color.go`/`display.go`/
`picklist.go`/`prompt.go`) and `internal/workflow/confirm.go`. Per
DESIGN.md's "Not decided yet" note, the plan for this remaining surface
was deliberately left open ("may evolve as we work through the
transition"). Before converting anything, we audited every remaining
`termlib` symbol's actual call sites (not just its imports) to find out
exactly what needs replacing — see DESIGN.md, "Removing termlib: Action
Wizards and Output," for the full table.

**Decision.** Remove `termlib` from `go.mod` entirely. Its two real
roles split cleanly:
1. **Interactive input** (`ui.Prompt`, `Confirm`, `ConfirmDestructive`,
   built on `termlib.LineEditor.Prompt`) → rebuilt on `huh.Input`/
   `huh.Confirm`, using the same split-into-testable-core pattern
   already established for the Menu tier.
2. **Plain status/error output** (`termlib.Terminal.Printf`/`Println`/
   `Refresh`, used for things like "Exiting.", error text after a
   failed action, the progress ticker's periodic elapsed-time line) →
   a plain `io.Writer`, since none of these call sites use anything
   `termlib.Terminal` offers beyond buffered printing, and the
   buffering itself (`Refresh`) is unneeded for straight-line
   sequential text.

Two adjacent, smaller pieces:
- `internal/ui.PickList` and its test are deleted outright — dead code,
  every real call site was already converted in the Menu/Picker/List
  punch list; only comments still mention it.
- `termlib.PadRight`/`Truncate`/`Bold`/`Reset`/`Green`/`Red`/`Yellow`
  (column formatting + ANSI constants, used only in `internal/ui`) and
  `termlib.FormatDuration` (used only in `progress_ticker.go`) are
  reimplemented locally — each is small (10-20 lines) and has no other
  caller once traced.

**Rationale.**
- The surface audit found that of `termlib.Terminal`'s and
  `LineEditor`'s full APIs, only `Printf`/`Println`/`Refresh` and
  `Prompt` are ever called anywhere in this codebase — history, tab
  completion, `$EDITOR` composition, multi-line input, cursor movement,
  and color state are all unused. Replacing termlib with huh (for
  input) and a bare `io.Writer` (for output) removes a dependency
  without losing any feature this codebase actually exercises.
- Matches the project's already-stated direction (DESIGN.md, "Terminal
  UI Architecture," 2026-07-10): "`termlib` ... is being removed
  entirely before 0.0.2 in favor of `huh` and `bubbletea` exclusively."
  This decision is the concrete plan for the one part of that statement
  that hadn't been scoped yet — the action-wizard/output call sites.
- Keeping this pass scoped to *removal* (not restyling) matches the
  user's own explicit split: migrate off termlib first, standardize
  chrome (color libraries, spinner components, etc.) as a separate,
  later pass. Two considered pieces were deliberately kept mechanical
  rather than upgraded here:
  - The progress ticker (`progress_ticker.go`) is the one place with a
    plausible case for a real `bubbletea` spinner component instead of
    a periodic printed line. Decided to keep it a plain `io.Writer` for
    this pass and defer any spinner to the chrome-improvement pass.
  - `internal/ui`'s color/format helpers could adopt `lipgloss` (already
    a transitive dependency via `huh`/`bubbletea`) instead of local ANSI
    constants. Decided to keep local constants for this pass and defer
    a `lipgloss` migration to the chrome-improvement pass, so this
    removal doesn't grow a second, unrelated goal.

**Rejected alternatives.**
- *Keep termlib for LineEditor's readline features, drop only
  Terminal* — rejected because those features (history, completion,
  `$EDITOR`) are entirely unused; keeping the dependency around for
  capabilities nothing calls has no benefit.
- *Introduce a thin backwards-compatible `Terminal`/`LineEditor`
  shim inside clasm itself* — rejected as an unnecessary
  compatibility layer for a one-time internal refactor with no external
  consumers of these types.
- *Convert file-by-file, keeping termlib in go.mod until the last call
  site is gone* — the Menu/Picker/List tiers could do this because each
  call site's own picker/menu was independently swappable without
  touching its caller's signature. Here, `t`/`le` are threaded through
  nearly every `internal/workflow` function's signature, so Go's
  whole-module compilation means this has to land as one coordinated
  change (ordered into PLAN.md Phase 20.15/20.16), not 40 independent
  single-file diffs.

**Consequences.**
- `le *termlib.LineEditor` disappears from every function signature in
  the codebase — it was never called directly outside `ui.Prompt`,
  `ui.PickList` (deleted), and `Confirm`/`ConfirmDestructive`.
- `t *termlib.Terminal` becomes `io.Writer` wherever a function prints
  status/error text directly; drops out of the ~9 files where it was
  pure pass-through with no direct use.
- Every test that constructs `termlib.New(&buf)` to capture output is
  rewritten to use the `bytes.Buffer` directly as an `io.Writer`, or to
  drive the new huh-based prompt/confirm cores via the established
  accessible-mode pipe pattern.
- `go.mod`'s `github.com/rsdoiel/termlib` entry is removed once the last
  call site is converted.

---

