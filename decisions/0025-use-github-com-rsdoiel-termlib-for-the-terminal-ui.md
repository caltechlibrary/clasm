---
id: "0025"
title: "Use github.com/rsdoiel/termlib for the Terminal UI"
date: "2026-07-01"
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
uuid: "e1cf33cd-ce15-438d-916d-e431b5882e64"
origin_host: "MACMINI-RD.local"
---

**Context.** PLAN.md's Phase 3 (Terminal UI) originally called for a
stdlib-only implementation (`text/tabwriter` for tables, `bufio.Scanner`
for the numbered pick list and free-text prompts) — no interactive-input
library existed under the pre-approved `github.com/rsdoiel` or
`github.com/caltechlibrary` namespaces (CLAUDE.md) at the time DESIGN.md
was drafted. The user has since pointed at `github.com/rsdoiel/termlib`
("a light weight terminal interface library... ncurses light"), which
already exists and is under the `github.com/rsdoiel` namespace, so no
separate approval discussion is needed per CLAUDE.md's pre-approved
dependency list.

**Decision.** Build `internal/ui` on `termlib` instead of a hand-rolled
stdlib-only implementation:
- `DisplayInstances`/`DisplayImages` use `termlib.Terminal` (buffered
  output, flushed via `Refresh`) plus `termlib.PadRight`/`termlib.Truncate`
  for column formatting, replacing `ec2_ami_manager.bash`'s
  `printf "%-20s ..."` pattern
- `PickList[T]` and `Prompt` are built on `termlib.LineEditor.Prompt`
  (readline-style editing, history, `Ctrl+C`/`Ctrl+D` handling) rather
  than a plain `bufio.Scanner` loop
- Tests drive `LineEditor` through an `os.Pipe()` (not a TTY), which
  `LineEditor.Prompt` detects and falls back to plain line reading for —
  documented and intended by `termlib` itself for exactly this use case,
  so no fake/mock input abstraction was needed

**Rationale.**
- `termlib` is pre-approved (CLAUDE.md: `github.com/rsdoiel` namespace)
  and already provides exactly what Phase 3 needs: buffered flicker-free
  terminal output, readline-style prompts with history and `Ctrl+C`/
  `Ctrl+D` handling, and column-formatting helpers (`PadRight`,
  `Truncate`) that map directly onto the Bash version's `printf`/
  `${var:0:N}` conventions
- Still numbered-menu based, not raw-keystroke arrow navigation —
  consistent with the existing "Numbered menu for pick lists" decision
  (2026-06-30); `termlib`'s raw-mode keystroke reading (`ReadKey`/
  `KeyReader`) is not used here
- Avoids reimplementing readline-style editing/history/multi-line-paste
  handling that a hand-rolled `bufio.Scanner` loop would not have had

**Rejected alternatives.**
- *Stdlib-only (`text/tabwriter` + `bufio.Scanner`)* — the original
  PLAN.md approach; works, but reinvents editing/history niceties
  `termlib` already provides, for no benefit now that an approved library
  covers it
- *A third-party TUI/prompt library outside the `github.com/rsdoiel` /
  `github.com/caltechlibrary` namespaces* — would need discussion per
  CLAUDE.md; moot once `termlib` was pointed out

**Consequences.**
- `go.mod` gains `github.com/rsdoiel/termlib` (direct) and its transitive
  `golang.org/x/term`/`golang.org/x/sys` dependencies
- `DESIGN.md`'s Dependencies section and its "no dependency beyond the Go
  standard library and the AWS SDK" claim are updated to include
  `termlib`
- If a gap surfaces in `termlib` while building later phases (e.g. a
  table-drawing helper `internal/ui` ends up hand-rolling), flag it back
  to the user for `~/Laboratory/termlib/TODO.md` rather than working
  around it silently in `awstools`

---

