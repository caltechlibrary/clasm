---
id: "0060"
title: "Fix termlib's LineEditor.Prompt for overlong prompt labels; keep awstools' own prompts short"
date: "2026-07-08"
status: accepted
kind: correction
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
uuid: "95060b5a-3ebf-4f00-911b-bee8f4a6c66f"
origin_host: "MACMINI-RD.local"
---

**Context.** A real bug, hit live: Import Key Pair's "Public key file
path" prompt initially embedded a long (~180 character) explanatory hint
directly in the prompt label passed to `ui.Prompt`/
`termlib.LineEditor.Prompt`. `LineEditor.Prompt`'s raw-mode redraw logic
computes its input viewport as `terminal width - prompt length`,
assuming the whole prompt fits on one terminal row, and repaints via
`"\r"`, which only returns to column 0 of the terminal's *current* row.
Once the prompt itself was wider than the terminal, printing it wrapped
onto multiple rows; every subsequent keystroke's redraw then reprinted
the *entire* prompt again after only a `"\r"`, re-wrapping and pushing
the display down further each time -- garbled, repeated prompt text,
with the input viewport (`vw`) clamped to 1 column, so typed characters
were never visibly readable. Not a cosmetic line-wrap issue.

**Decision.** Two changes, at two levels:
1. **Fixed the actual root cause in `termlib` itself**
   (`~/Laboratory/termlib/lineeditor.go`, this team's own library, not an
   external one): a new `splitSafePrompt(prompt, termWidth)` helper splits
   any prompt into a `head` (everything up to and including the last
   embedded `\n`, printed once and never revisited) and a `tail` (only
   the portion that could ever share a terminal row with typed input).
   If `tail` is still `>= termWidth` runes wide on its own, it's folded
   into `head` too (with a trailing newline), and `tail` becomes empty --
   editing then starts on its own blank row with the full terminal width
   available, instead of trying to redraw a prompt too wide for the
   existing single-row cursor math to track. `Prompt()` now calls this
   before entering its edit loop and uses the (now guaranteed-short)
   `tail` as `prompt` throughout the rest of the function -- `redraw()`
   itself needed no changes, since `curPromptLen`/`vw` now always operate
   on a value that's safe by construction.
2. **Simplified awstools' own prompt** back down to a short label,
   `"Public key file path (.pub -- e.g. ~/.ssh/id_ed25519.pub)"`, with the
   longer "not a private key / derive with `ssh-keygen -y -f
   <private-key> > file.pub`" guidance moved into
   `validatePublicKeyFile`'s own rejection error message instead --
   delivered reactively, exactly when the operator actually makes that
   mistake, rather than proactively in every single prompt.

**Rationale.**
- Fixing `termlib` closes this whole bug class everywhere it's used, not
  just this one prompt -- including this project's own pre-existing
  ~137-character "Key pair name (...)" prompt (`create_key_pair.go`) and
  ~116-character "IAM instance profile (...)" prompt
  (`create_instance_profile.go`), both of which had the same latent risk
  on a narrow enough terminal without ever being reported broken.
- `splitSafePrompt` also incidentally fixes embedded-newline prompts,
  which were never explicitly tested before and would have hit the same
  miscounted-`curPromptLen` problem (newlines counted toward row width
  even though they occupy zero visual columns).
- A short, single-purpose prompt label matches this project's dominant
  convention (see `backup_archive.go`'s "Backup directory (e.g.
  /opt/rdm_sql_backups)") better than either the original overlong
  version or the printed-hint-then-short-prompt workaround tried first
  -- and, now that `termlib` itself is fixed, the workaround is no longer
  load-bearing for correctness, just a style preference.

**Rejected alternatives.**
- *Insert a literal `\n` inside the prompt string, unfixed* — doesn't
  work on its own; the pre-fix `redraw()` counted the entire prompt
  (including embedded newlines) as occupying one row, so this needed the
  `termlib` fix to actually behave correctly.
- *Print the hint separately via `t.Println`, leave `termlib` alone* —
  tried first as a same-day workaround; superseded once the actual
  `termlib` bug was fixed properly, since papering over it in every
  affected caller (there were at least two pre-existing ones) is more
  maintenance than fixing the one shared library function.

**Consequences.** The fix was committed and pushed to `termlib`'s `main`
branch (commit `354195d`, "fix prompt and input bug"); `awstools/go.mod`
pins it directly by commit via a pseudo-version
(`github.com/rsdoiel/termlib v0.0.10-0.20260708184214-354195d36c57`,
resolved from the real GitHub remote, not a local-path `replace`) --
bump this to a proper tagged release version once `termlib` cuts one.
The new `splitSafePrompt` logic is fully unit-tested in `termlib` itself
(`lineeditor_test.go`, `TestSplitSafePrompt_*`), since it's a pure
function; the actual redraw behavior it fixes is still not reproducible
in either project's pipe-based test harness (`os.Pipe()` is never a TTY,
so `LineEditor.Prompt` always takes its plain `fallback()` path in
tests) -- confirming the real-terminal symptom is gone required manual
interactive testing, which the user did: Show, Create, Import, and
Delete Key Pair all confirmed working against real AWS on 2026-07-08.

---

