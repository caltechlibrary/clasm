---
id: "0067"
title: "File manager command area: single-letter hotkeys plus a colon command line, both always active; no function keys"
date: "2026-07-09"
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
uuid: "997a5b44-9acb-4dd0-9ee3-796c08c42ce8"
origin_host: "MACMINI-RD.local"
---

**Context.** Traditional dual-pane managers (Midnight Commander) drive
their command area with function keys (F5 copy, F6 move, F8 delete).
Function-key mappings are unreliable across terminal emulators,
multiplexers, and SSH sessions in practice -- a real operational
concern for a tool meant for wider library/archive use, not just this
team's own terminal setup.

**Decision.** Use single-letter mnemonic hotkeys (`u` Upload, `d`
Download, `x` Delete, `f` Filter, `F` Find, `l` Link, `Tab` switch pane,
`Space` tag, `q` quit) instead of function keys, and add a
`:`-prefixed command line as a fully independent second path to every
action (`:upload`, `:delete`, `:find <pattern>`). Both drive the same
underlying action dispatch -- neither is a fallback for the other.

**Rejected alternatives.** *Function-key legend bar only* (the
mc/WinSCP convention, originally the leading option) -- rejected once
the terminal-remapping risk was raised; letter keys plus a
typed-command path removes the dependency on F-key passthrough working
correctly at all. *Hotkeys only, no command line* -- rejected; a typed
path is strictly more robust across terminal configurations and costs
little extra to support given both dispatch to the same handlers.

**Consequences.** Every action needs a name (verb) as well as a key
binding, since the command line and hotkey bar must both resolve to the
same dispatch table -- a small but real constraint on how actions get
implemented (`PLAN.md` Phase 20.1).

---

