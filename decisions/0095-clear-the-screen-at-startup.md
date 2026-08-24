---
id: "0095"
title: "Clear the screen at startup"
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
uuid: "2bfdcb03-c672-4edf-89f9-ed8e8653509c"
origin_host: "MACMINI-RD.local"
---

**Context.** clasm printed its first line ("clasm x.x -- authenticated
as AWS account ...") directly into whatever the terminal already had on
screen -- old shell history, a previous command's output, etc. --
unlike a typical full-screen terminal application, which starts from a
clean slate. Requested directly, alongside a request to have the
startup screen use the terminal's full height -- addressed here only
partially; see the note in PLAN.md's corresponding phase entry about
what was deliberately not changed and why.

**Decision.** New `ui.ClearScreen(w io.Writer)`, sending the same two
escape sequences bubbletea's own `tea.ClearScreen` command sends
(`ansi.EraseEntireScreen` + `ansi.CursorHomePosition`, from
`github.com/charmbracelet/x/ansi` -- already a transitive dependency
via `bubbletea`, now promoted to direct), rather than a hand-rolled
escape string. Called once in `main()`, after the `-help`/`-license`/
`-version` early-exits (which must stay script/pipe-friendly, so they
must not inject terminal control codes) but before any other output,
including error paths (config load failures, AWS client construction
failures, etc.) -- the whole interactive session starts from a clean
terminal, not just the happy path.

**Rationale.** Reusing bubbletea's own escape sequences (via the
`x/ansi` package it's already built on) keeps this consistent with how
every List/Picker/Manager screen already clears itself on `Init()`,
rather than inventing a second, potentially-different way to clear a
terminal.

**Consequences.** New `internal/ui/clear.go` (+ test);
`github.com/charmbracelet/x/ansi` promoted from indirect to direct in
`go.mod`. `cmd/clasm/main.go` calls `ui.ClearScreen(out)` once, right
after the flag-based early exits.

---

