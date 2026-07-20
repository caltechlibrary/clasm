# clasm TUI Reference

A visual reference for clasm's terminal UI, for testers and developers:
know what chrome (borders, hint lines, footer text) to expect on a
given screen, so a genuine difference between screens reads as "this is
a different tier, working as designed" rather than "this looks broken."
Written after a round of manual resize testing (2026-07-20) turned up
exactly this confusion -- see `DECISIONS.md`, "Full-height Menu tier
via live `WindowSizeMsg` tracking, applied at every depth," and `PLAN.md`
Phase 20.26/20.28.

Every mockup below was checked against real rendered output (`go
test`-driven, with the real `tui.Theme()` and a real `huh.Form`/
`quitKeyGuard`, not hand-guessed), not assumed. Box-drawing characters
match `internal/tui/box.go` exactly (`┌─┐│├─┤└─┘`).

clasm has five distinct chrome "tiers." Only one of them
(**Menu tier**) is what Phase 20.26 made full-height; the other three
boxed tiers (**Picker**, **List**, **Manager**) have had their own,
separate `tea.WindowSizeMsg` handling since Phase 20.4-20.12, well
before this session. **Plain prompts** never go full-height at all --
that's deliberate, not a gap.

## 1. Menu tier — `huh.Select` via `runMenuField`/`quitKeyGuard`

**Where:** `internal/workflow/domain_menu.go`. Every domain picker,
every per-domain action menu (Compute/S3/Key Management), and every
small fixed-list pick within a workflow (`pickString`/`pickComparable`
call sites, e.g. Backup Archive & Trim's bucket picker, Manage Tags'
Instance-vs-AMI choice).

**Distinguishing features:**
- A hint line (`(q to go back)` or `(q to exit)`) printed **outside and
  above** the box, via a plain `fmt.Fprintln` *before* the bubbletea
  Program even starts -- not part of the box itself.
- Full terminal height (Phase 20.26) -- the box's own interior pads
  with blank lines to fill whatever's left after the hint line and
  huh's own footer (`menuHintReservedLines = 2`, both accounted for).
- huh's own footer line, printed **outside and below** the box --
  wording depends on the field (a filterable Select shows
  `/ filter`; a non-filterable one omits it).

```
(q to go back)
┌ Choose an option ───────────────────────────────┐
│ Manage EC2 instances, AMIs, and launch          │
│ templates, or archive stale backups to S3.      │
│ > Show instances                                │
│   Show AMIs                                     │
│   Show launch templates                         │
│   ...                                           │
│                                                 │
│                                                 │
└─────────────────────────────────────────────────┘
↑ up • ↓ down • / filter • enter submit
```

## 2. Picker tier — `tui.RunPicker`/`PickerModel`

**Where:** `internal/tui/picker.go`. Selecting *one* item from a
fetched, variable-length collection -- an instance, an AMI, an S3
bucket, a key pair, a launch template. Not huh at all; its own
bubbletea `Model`.

**Distinguishing features:**
- No separate hint line outside the box -- everything (title,
  optional description, rows, filter status, legend) is **inside one
  continuous bordered box**.
- Its own legend wording, different from huh's:
  `↑/↓,k/j scroll  / filter  enter select  q Quit`.
- Already full-height and live-resizing before this session (own
  `tea.WindowSizeMsg` handling in `Update`).

```
┌ Select an instance ─────────────────────────────┐
│ Connects to this instance via SSM to list       │
│ and upload backup files.                        │
│ > i-0123abc - web (running, us-east-1)          │
│   i-0456def - db (stopped, us-west-2)           │
│                                                 │
│                                                 │
├─────────────────────────────────────────────────┤
│ filter: none                                    │
├─────────────────────────────────────────────────┤
│ ↑/↓,k/j scroll  / filter  enter select  q Quit  │
└─────────────────────────────────────────────────┘
```

## 3. List tier — `tui.RunListView`/`ListViewModel`

**Where:** `internal/tui/listview.go`. Read-only tabular listings --
"Show instances," "Show AMIs," "Show launch templates," "List S3
Buckets," Key Management's "Show resource lists."

**Distinguishing features:** identical shape to the Picker tier (same
shared chrome, `internal/tui/box.go`), minus selection -- the legend
omits `enter select` since there's nothing to choose:
`↑/↓,k/j scroll  / filter  q Quit`. A frozen header row (column titles)
sits above the scrollable body when the config provides one.

```
┌ EC2 Instances ──────────────────────────────────┐
│ INSTANCE ID  NAME  STATE    AMI ID   REGION     │
│ i-0123abc    web   running  ami-1    us-east-1  │
│ i-0456def    db    stopped  ami-2    us-west-2  │
│                                                 │
├─────────────────────────────────────────────────┤
│ filter: none                                    │
├─────────────────────────────────────────────────┤
│ ↑/↓,k/j scroll  / filter  q Quit                │
└─────────────────────────────────────────────────┘
```

## 4. Manager tier — `internal/filemanager.Model`

**Where:** `internal/filemanager`. S3's "Browse & Manage Objects" --
the only stateful, persistent interactive screen in clasm (everything
else is either read-only or a one-shot wizard). Single-pane (bucket
only) or double-pane (bucket + a linked local directory).

**Distinguishing features:** the richest legend (every hotkey the
screen supports), a status line (item count, tagged count, tagged
size), and a `:command` line mode (`f`/`/` to filter, `:` for a
colon-command) in addition to hotkeys. Two panes side by side when a
local directory is linked.

```
┌ s3://my-bucket ──────────────────────┬ ~/backups ───────────────────────┐
│ NAME              SIZE    MOD        │ NAME             SIZE   MOD      │
│ [ ] file1.txt     1.2 KB  ...        │ [ ] file1.txt    1.2 KB  ...     │
│ [x] file2.txt     3.4 KB  ...        │                                  │
│                                      │                                  │
├─────────────────────────────────────────────────────────────────────────┤
│ 2 items, 1 tagged (3.4 KB)                                              │
├─────────────────────────────────────────────────────────────────────────┤
│ u Upload  d Download  x Delete  m Metadata  f Filter  F Find  S Sync    │
│ r Refresh  l Unlink (1-pane)  Tab Switch  Space Tag  * Tag All  q Quit  │
└─────────────────────────────────────────────────────────────────────────┘
```

## 5. Plain prompts — `ui.Prompt`/`Confirm` (`huh.Input`/`huh.Confirm`)

**Where:** free-text and yes/no prompts throughout every workflow
(Name tag, directory paths, age thresholds, "Launch this instance?").
Still a bordered `huh` box (`tui.Theme()` applies here too), but
**deliberately not full-height** -- these never go through
`quitKeyGuard`/`WithHeight`, so they stay content-sized. This is by
design, not a gap: a one-line input field filling the whole terminal
would look broken, not helpful.

```
┌ Name tag ───────────────────────────────────────┐
│ >                                               │
└─────────────────────────────────────────────────┘
enter submit
```
```
┌ Launch this instance? ──────────────────────────┐
│                                                 │
│      Yes     No                                 │
└─────────────────────────────────────────────────┘
←/→ toggle • enter submit • y Yes • n No
```

## High-level screen map, tagged by tier

Domains and their menu items only -- not every prompt inside every
workflow (see the individual `internal/workflow/*.go` files for that
level of detail; it changes too often to keep a full tree in sync
here).

```
Domain Picker [Menu]
├─ Compute (EC2 & AMI) [Menu, 20 items]
│  ├─ Show instances / Show AMIs / Show launch templates   [List]
│  ├─ Create EC2 instance from AMI / cloud-init YAML / launch template
│  │                                                        [Picker + plain prompts]
│  ├─ Start / Stop / Terminate EC2 instance                 [Picker + plain prompt/Confirm]
│  ├─ Manage tags for an instance or AMI                     [Menu + Picker + plain prompts]
│  ├─ Create AMI from EC2 instance / Remove AMI               [Picker + plain prompts]
│  ├─ Show/export cloud-init for an instance or AMI            [Menu + Picker]
│  ├─ Show a launch template (detail / list versions / diff two) [Picker + Menu + List]
│  ├─ Create/Sync/Promote/Delete launch template (version(s))  [Picker + plain prompts + List (diff)]
│  └─ Archive stale backups to S3 and trim disk space           [Picker + Menu (bucket) + plain prompts]
├─ Key Management [Menu, 4 items]
│  ├─ Show resource lists (key pairs)                          [List]
│  ├─ Create / Import Key Pair                                  [plain prompts]
│  └─ Delete Key Pair                                            [Picker]
└─ S3 (Buckets & Static Websites) [Menu, 6 items]
   ├─ List S3 Buckets                                            [List]
   ├─ Create Bucket / Configure Static Website Hosting             [Menu (region) + plain prompts]
   ├─ Browse & Manage Objects                                       [Picker (bucket) + Manager]
   ├─ Manage Bucket Lifecycle Policies                               [Picker + Menu (storage class)]
   └─ Delete Bucket                                                   [Picker + plain prompts]
```
