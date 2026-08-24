---
id: "0003"
title: "Numbered menu for pick lists"
date: "2026-06-30"
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
uuid: "dc2308ca-1752-464d-9947-dae571718ebf"
origin_host: "MACMINI-RD.local"
---

**Context.** The script needs to present lists of resources (AMIs, instances) for user selection. We must choose an interaction method.

**Decision.** **Use numbered menus for resource selection.**

**Rationale.**
- Works in all terminal environments (no special dependencies)
- Simple and familiar to users
- Easy to implement in pure Bash
- No external tools required (like fzf)

**Rejected alternatives.**
- *fzf fuzzy finder* — requires fzf installation; not available on all systems
- *arrow-key navigation* — complex to implement in pure Bash; requires ncurses or similar
- *Search/filter* — adds complexity for initial implementation

**Consequences.**
- For large lists (>20 items), may need pagination
- User must type number corresponding to selection
- Input validation needed for number range
- Can add fzf support as optional enhancement in future

---

