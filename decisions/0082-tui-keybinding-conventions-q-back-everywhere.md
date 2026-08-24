---
id: "0082"
title: "TUI keybinding conventions: q=back everywhere, arrows/j-k navigate, Enter=select, Esc cancels only an in-progress step, persistent legend bar"
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
uuid: "06423fbc-9c67-4ab1-909c-2b9bfd98c505"
origin_host: "MACMINI-RD.local"
---

**Context.** Direct follow-on to the two decisions above: a concrete,
approved keybinding table so "consistent commands throughout the
application" means something specific enough to implement and test
against, not just a stated goal. Drafted, then approved by the user
with no corrections.

**Decision.**

| Key | Action | Where |
|---|---|---|
| `q` | Back to the parent screen | Everywhere |
| `↑`/`↓`, `k`/`j` | Navigate / scroll | Menus, lists, managers |
| `Enter` | Select / confirm / submit | Menus, lists, wizards |
| `Esc` | Cancel the *in-progress* action only — never closes a screen | Wizards, in-progress input |
| `/` | Filter | Menus, lists, managers |
| Legend bar | Always visible at the bottom of every screen, showing that screen's actual keys | Every screen |

Mostly formalizes precedent already in the codebase rather than
inventing new bindings: `'q'`/`ctrl+c` already quit the file manager;
huh's own `Select` default keymap already binds `↑/k`, `↓/j`, and `/`
for filter; `Esc`-cancels-not-closes matches the earlier "quit vs.
cancel" wording note (TODO.md, "UX improvements," 2026-07-09). The one
place this can't be applied uniformly: `huh.Select`'s own footer is
built solely from the focused field's `KeyBinds()`
(`SelectKeyMap` has no quit/back entry, and `KeyBinds()` isn't
overridable without forking huh), so menus get `q` bound at the `Form`
level (`Form.WithKeyMap`, `KeyMap.Quit` gains `"q"` alongside
`"ctrl+c"`) plus a separately-printed static hint line above the menu,
rather than a real legend-bar entry. List and manager tiers, which own
their full rendering, show `q` in an actual legend bar.

**Consequences.** `RunS3Menu`'s `huh.Select` gains `q` as an additional
`Quit` trigger, resolving through the already-existing
`mapS3MenuPickerErr`/`ErrUserAborted`→`ErrBackToDomainPicker` path — no
new dispatch logic. "Back to domain picker" is removed from
`s3MenuItems` (redundant with `q`); the `choice.action == nil` branch in
`runS3Menu` becomes dead code and is removed with it. "Show resource
lists" is relabeled "List S3 Buckets" (clearer, and matches what it
actually does once it's a dedicated List-tier screen rather than a
Refresh-and-print action) — label only, not the underlying
`S3Actions.ShowResourceLists` Go identifier, kept as-is since renaming
it carries no user-facing benefit and only adds diff noise.

**Implemented 2026-07-10, same session** (PLAN.md Phase 20.7): exactly
as scoped above. huh's own footer still can't show `q` (confirmed
against source, as expected), so a static `"(q to go back)"` hint is
printed via the existing `t.Println`/`t.Refresh()` before each menu
redisplay. Tests exercising "one action dispatch, then the loop ends"
were rewritten around a `context.WithCancel` + cancel-from-within-the-
test-action-closure pattern, since there's no longer a "Back to domain
picker" menu choice to select and accessible mode can't simulate the
`q`/ctrl+c abort that replaces it (matching `mapS3MenuPickerErr`'s
already-documented limitation). `go build`/`go vet`/`go test ./...
-race`/`gofmt -l` all clean. The `q`-binding's actual effect (does
pressing `q` really abort the `Select`) can only be confirmed by real
interactive use — noted, not yet done, same class of gap as this
session's other `huh`/`bubbletea` work.

---

