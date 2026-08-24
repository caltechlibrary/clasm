---
id: "0085"
title: "Add a Picker tier: resource selection gets its own internal/tui component, not huh.Select"
date: "2026-07-10"
status: accepted
kind: decision
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "12eb447d-921a-4d85-83d6-07c7de6ad21a"
origin_host: "MACMINI-RD.local"
---

**Context.** Starting Phase 20.4 (converting the three S3
bucket-selection call sites from `ui.PickList` to `huh.Select`, reusing
`object_browser.go`'s existing `runFieldWithHelp`/`huhCancelledIsNil`
pattern) the user stopped this before any code was written: "I think
this UI should feel the same whether I select a bucket, an AMI or an EC2
instance." A real gap in the prior day's taxonomy: `huh.Select`'s own
rendering looks nothing like the bordered-box/legend-bar chrome the List
and Manager tiers just adopted (DESIGN.md, "Terminal UI Architecture"),
so converting bucket-selection to `huh.Select` would have made the S3
domain show two different visual languages depending on whether a
screen displays a resource or selects one — precisely the inconsistency
the whole termlib-deprecation effort exists to avoid.

**Decision.** New `internal/tui.PickerModel`: reuses `ListViewModel`'s
exact chrome (`TopBorder`/`BoxLine`/`Divider`/`ScrollWindow`/`StyleRow`/
`BottomBorder`) but adds selection (`Enter` chooses the row under the
cursor and returns it; `q`/`ctrl+c` cancels) and, per the user's explicit
request, incremental filtering from the start ("this allows someone to
go directly to the thing they want if they know the name or part of the
name") — `/` enters filter-typing mode (matching the keybinding table
and `huh.Select`'s own default, not an always-on type-ahead that would
collide with `j`/`k` navigation), narrows by case-insensitive substring
match, `Esc` clears it. Works on pre-rendered rows, returns an index
(not a typed value) so `internal/tui` doesn't need generics — the same
pattern `pickS3MenuItem` already uses for `s3MenuItems`.

Per the user's request, DESIGN.md's taxonomy now includes a concrete
map: every current `ui.PickList` call site that selects one instance of
a fetched resource (bucket, EC2 instance, AMI, key pair, subnet, region,
role, lifecycle rule, storage class — ~25 call sites across Compute, Key
Management, and S3), each with its file:line and status. S3 buckets are
the pilot (Phase 20.4, converting now); everything else is explicitly
listed as not-yet-scheduled rather than silently left for someone to
rediscover later. Guide-menu-shaped choices (small, fixed option sets —
domain/action menus, Instance-vs-AMI kind pickers, the tag action menu,
remediation choices) are explicitly excluded from this map; they stay on
`PickList`/`huh.Select` since they're not selecting an instance of a
resource collection.

**Rejected alternatives.**
- *Convert bucket-selection to `huh.Select` as originally planned* —
  works functionally, but reintroduces the exact visual inconsistency
  this session's termlib-deprecation work exists to eliminate; huh's
  `Select` field can't be restyled to match `internal/tui`'s chrome
  without forking huh.
- *A `Selectable bool` flag on `ListViewModel` instead of a separate
  `PickerModel`* — would avoid a second component, but conflates two
  different interaction models (pure read-only browsing vs. choose-and-
  return) in one type, against this project's established preference
  for small, purpose-built components (the same reasoning that already
  kept the List tier separate from `filemanager.Model`).
- *Defer filtering, add it later if a list turns out to need it* — this
  session's own default instinct (matching how `ListViewModel` itself
  shipped without filtering, since bucket counts are usually small) —
  overridden here because the user asked for it explicitly and gave a
  concrete rationale (typing a known name/substring beats scrolling
  through a long AMI or instance list), not a hypothetical future need.

**Consequences.** Phase 20.4 is retargeted: bucket-selection converts to
`tui.RunPicker`, not `huh.Select`. New PLAN.md Phase 20.8 builds
`PickerModel` itself (a dependency of Phase 20.4). `object_browser.go`'s
existing `huh.Select`-based bucket pre-flight is unaffected by this
decision for now — revisiting it to also use `PickerModel` is a
separate, not-yet-scoped question, not implied automatically by this
one.

**Implemented 2026-07-10, same session** (PLAN.md Phase 20.8):
`PickerModel` built test-first. Two things worth keeping on record: (1)
filtering must pin the rendered content area to the *unfiltered* row
count, not however many rows currently match, or the box's height
shrinks/grows while typing a filter and reproduces the same inline-
rendering hiccup the List tier already found for exact/changing frame
heights — confirmed by an actually-failing test, fixed by padding to a
stable height (also better UX, `fzf`-style). (2) Two filter tests hit a
second, distinct `teatest` gotcha already documented in this codebase
(bubbletea skips retransmitting unchanged lines between consecutive
frames, so checking the same text across two separate `WaitFor` calls
can race) — fixed the same way `internal/filemanager`'s tests already
do: combine assertions into one `WaitFor`.

Phase 20.4 (bucket selection) also implemented, same session: each of
the three call sites (`ConfigureBucketWebsite`, `ManageBucketLifecycle
Policies`, `DeleteBucket`) now calls a shared `pickBucket` helper, then
delegates to an unexported, directly-testable core taking the resolved
bucket. `cancelledIsNil` recognizes `tui.ErrCancelled` alongside
`ui.ErrCancelled`. Full detail: PLAN.md Phase 20.4.

---

