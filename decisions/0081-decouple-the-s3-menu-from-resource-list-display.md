---
id: "0081"
title: "Decouple the S3 menu from resource-list display; add a generic paged table to internal/ui"
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
uuid: "83b26f84-8035-40b3-9e6d-9886dd2a714d"
origin_host: "MACMINI-RD.local"
---

**Context.** Following the `RunS3Menu` huh.Select conversion (below),
the user pointed out a UX problem exposed by that same code path: every
successful S3 menu action triggers `actions.Refresh(ctx)`, and
`refreshS3` (`cmd/clasm/main.go`) both re-fetches bucket data and prints
the *entire* bucket table (`ui.DisplayBuckets`) every time — so the S3
menu redisplay is cluttered with a resource list after every action, not
just when "Show resource lists" is chosen, and that table has no
pagination at all (unlike `ui.PickList`'s existing 50-item paging), so
it would print unboundedly for a large bucket count. Requested fix: the
S3 menu should show only the menu; "Show resource lists" becomes its
own dedicated, paged display with the column titles visible on every
page, and `n`ext/`p`revious/`q`uit navigation (`q` returns to the S3
menu). A mockup was drawn up and approved before any code was written,
per this project's design-before-code process for non-trivial changes.

**Decision.** Full design recorded in DESIGN.md, "S3 Resource List
Display — Paged, Accessible-Compatible" (2026-07-10), and PLAN.md Phase
20.3. Key points carried here for the decision record:
- Split "refresh" into re-fetching data (unchanged, still happens after
  every action) versus *displaying* it (now only on explicit "Show
  resource lists").
- New generic `internal/ui` component (`PagedTable`, PLAN.md Phase
  20.3), not a bucket-specific one: takes a banner-format callback and
  pre-rendered header/row strings, owns only windowing, chrome, and
  `n`/`p`/`q` input. `DisplayBuckets`'s existing `PadRight`/`Truncate`
  column formatting is reused to build the strings passed in.
  Deliberately generic **so Compute/Key Management's own resource
  listings can reuse the same mechanism later**, if/when those menus are
  migrated — not part of this piece of work, but designed not to
  preclude it, per the user's own framing ("we'll reuse this UI approach
  as needed migrating to huh for other parts of clasm").
- Stays fully accessible: sequential printing only (banner, header,
  page of rows, command prompt, read one line, repeat or return) — no
  cursor repositioning, so behavior is identical over a real TTY,
  `TERM=dumb`, or a piped input/output pair in tests. Note this
  mechanism doesn't involve `huh` at all -- it's the same plain
  `termlib`/`LineEditor.Prompt` style `PickList` already uses; paging a
  resource list and "migrating to huh" are orthogonal concerns here.

**Rejected alternatives.** See DESIGN.md's addendum for the full list
(unpaginated-with-scrollback, a `huh`/`bubbletea`-style redraw-in-place
viewport, and reusing `PickList` directly) — each rejected for the
reasons recorded there; not repeated here to avoid drift between the
two documents.

**Consequences.** `ui.DisplayBuckets` is replaced by a `PagedTable` call
site (its signature changed: now takes `le` and returns `error`, its
only call site updated); `refreshS3` splits into a silent-refresh half
and a separate `showS3ResourceLists` closure; `s3MenuItems`' "Show
resource lists" entry calls a new `S3Actions.ShowResourceLists` field
instead of `Refresh`. Compute/Key Management's current unpaginated
`DisplayInstances`/`DisplayImages`/`DisplayKeyPairs` are explicitly
unchanged — ask before extending this pattern to them.

**Implemented 2026-07-10, same session, test-first** (PLAN.md Phase
20.3): `internal/ui/paged_table_test.go` was written and run against a
stub before `paged_table.go` existed, then `PagedTable` was implemented
to make it pass; `display_test.go`'s two `DisplayBuckets` tests were
updated for the new signature plus a new pagination test; a real test
gap was caught and closed along the way — no existing `s3_menu_test.go`
case exercised choosing "Show resource lists" (menu item 1) at all,
before or after this change, so a `TestRunS3Menu_ShowResourceListsDispatchesToItsOwnAction`
was added. `go build`, `go vet`, and `go test ./... -race` all clean.

---

