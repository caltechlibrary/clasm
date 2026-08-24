---
id: "0039"
title: "Move \"Show resource lists\" to the top of the Compute menu; rename from \"Refresh\""
date: "2026-07-02"
status: accepted
kind: refinement
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
uuid: "c801e689-ba63-42e6-b45f-8df296228ae5"
origin_host: "MACMINI-RD.local"
---

**Context.** User feedback: "Refresh resource lists" sat near the bottom
of the Compute menu (item 11 of 12), and "Refresh" was ambiguous about
what it actually does (re-fetch from AWS and redisplay both tables, not
just repaint the screen). Since every other successful action already
triggers an automatic refresh afterward (2026-06-30, "Refresh data after
each operation"), this item's real purpose is letting the operator
deliberately re-orient -- see current state -- without taking an action,
which is a natural first move on entering the domain, not action #11 of
12.

**Decision.** Renamed to "Show resource lists" and moved to menu
position 1; every other item shifts down by one, "Back to domain picker"
stays last (position 12, unchanged, since the total item count didn't
change). The underlying behavior and `MenuActions.Refresh` field name
are unchanged -- this is a label and position change only.

**Rationale.** Matches how the operator actually uses this tool: check
what's running, then act -- not "act ten times, and eleventh, maybe
check." "Show" also describes the operator-visible effect (the two
tables reappear) rather than an AWS-side connotation ("refresh" could
read as "refresh the AWS resources themselves").

**Rejected alternatives.** None seriously considered -- this is a small,
low-risk UX tweak; no behavior, permissions, or data flow changes.

**Consequences.**
- `internal/workflow/menu.go`'s `mainMenuItems` reordered; every test in
  `menu_test.go` that referenced a menu item by number was updated to
  match (item numbers shift by one; "Back to domain picker" stays 12).
- `DESIGN.md`'s Compute Menu ASCII diagram and `TEST_PLAN_REAL_AWS.txt`'s
  menu-order checklist updated to match, preserving existing `[ok]`
  markers against their renamed/renumbered items (the capability was
  already verified; only its label and position changed). Also fixed
  unrelated staleness noticed while there: `TEST_PLAN_REAL_AWS.txt` still
  said item 12 was "Exit" from before the domain-picker refactor
  (2026-07-02, "Redesign navigation as a domain picker...") -- corrected
  to "Back to domain picker".

---

