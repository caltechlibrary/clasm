# AWS Tools — awsops — Design

> **2026-07-01: Retargeted to Go.** See `DECISIONS.md` ("Retarget
> implementation from Bash to Go") for why. `ec2_ami_manager.bash` remains in
> this repo, unchanged, as the working reference for the behavior this
> document describes, until the Go version reaches parity and is verified
> against real AWS.
>
> **2026-07-02: Domain-picker redesign.** Scope is expanding beyond EC2/AMI
> to Key Management, S3 (including static website hosting), and CloudFront.
> The single flat main menu is replaced by a domain picker (Compute / Key
> Management / S3 / CloudFront) with a domain-scoped submenu underneath —
> see "Navigation: Domain Picker" below. This is additive: Compute's
> existing 12 features (below) are unchanged in behavior, only regrouped
> under one submenu. Real-AWS verification of Compute (Phase 16) continues
> in parallel with this redesign — see `PLAN.md`. See `DECISIONS.md`,
> "Redesign navigation as a domain picker; add Key Management, S3, and
> CloudFront domains".
>
> **2026-07-08: Bash retired.** Phase 16's real-AWS verification
> (`TEST_PLAN_REAL_AWS.txt`, 112/112 checks) is complete. `ec2_ami_manager.bash`,
> `ami_copy.bash`, `ami_copy_basic_steps.md`, and `tests/*.bats` have been
> deleted from this repo; `awsops` is now the sole implementation and the
> working reference for Compute domain behavior. See `DECISIONS.md`,
> "Retire ec2_ami_manager.bash, ami_copy.bash, and the Bash test suite".

## Overview

An interactive Go CLI for administering AWS EC2 instances and AMIs for
this team's infrastructure, across two regions (us-west-1, us-west-2 —
narrowed from an original four; see `DECISIONS.md`, "Narrow configured
regions to us-west-1/us-west-2"). The tool is general-purpose — nothing
in its
mechanisms (tagging, backup archival, cloud-init inspection) is
RDM-specific (see `DECISIONS.md`, "Name the CLI binary `awsops`") — but
this team's Invenio RDM deployments are its primary use case today, and
several features (the Postgres/OpenSearch/Redis crash-consistency
guidance, the backup-directory convention) are grounded in operational
facts observed on those instances. Beyond EC2/AMI lifecycle management,
the tool is meant to help with ongoing *administration* of these
instances (e.g. backup hygiene, inspecting deployed configuration) and to
speed up and de-risk development, test, and deployment workflows more
broadly — not just be a thin wrapper over
`RunInstances`/`CreateImage`/`DeregisterImage`. The core EC2/AMI feature
set and UX below are unchanged from the Bash version — only the
implementation language and AWS access layer change; everything from
"Show/Export Cloud-Init" onward is new scope that came out of this design
review.

This team's AWS footprint splits into two broad concerns: deploying and
operating Invenio RDM instances (Compute: EC2/AMI, plus the SSH key pairs
they launch with) and publishing static websites (S3 buckets as origin,
CloudFront serving and caching in front of them). The tool's navigation
now reflects that split directly — see "Navigation: Domain Picker" below
— rather than growing a single ever-longer menu.

## Non-Goals

`awsops` is an interactive replacement for ad hoc, day-2 AWS Console
work — "what's running right now, let me tag/start/stop/snapshot/back up
this specific thing" — with this team's safety gates and domain
knowledge (crash-consistency guidance, backup hygiene, the Project/
Environment tagging convention) built in. It is deliberately **not**:

- **A declarative infrastructure-as-code tool.** It doesn't define
  desired state, diff against reality, or reconcile drift. Terraform,
  Pulumi, and AWS CDK already solve that problem well; if this team ever
  wants version-controlled, reproducible environment definitions, one of
  those is the right tool, not a `awsops` feature to grow toward.
- **An AMI-baking pipeline.** Packer (and AWS EC2 Image Builder) already
  automate "base AMI + cloud-init/provisioning script → new AMI." The
  deferred "Bake AMI from cloud-init" idea (below) is v1's primitives
  composed by hand, not a competing pipeline tool.
- **A general-purpose AWS CLI replacement.** It wraps a curated, opinionated
  subset of operations this team actually performs, not the full breadth
  of any single AWS service's API.

Scope decisions in this document (curated instance-type lists over full
API listings, a fixed Project/Environment tagging vocabulary rather than
free-form policy, no "pick a different AMI" recovery path once one is
committed) follow from staying inside this lane — see `DECISIONS.md` for
the specific trade-offs each one made.

## Configuration

`awsops` reads its own operational settings — never AWS credentials or
profile selection, which remain entirely the AWS SDK's responsibility
via its standard chain (`~/.aws/credentials`, `~/.aws/config`,
environment variables, SSO; see "Assumptions" #1, unchanged) — from an
optional YAML file at `~/.awsops` (overridable with `-config <path>`).
See `DECISIONS.md`, "Add a `~/.awsops` YAML config file for awsops' own
operational settings".

- **Entirely optional.** If the file doesn't exist at the resolved path
  (default or `-config`-specified), built-in defaults apply and the
  tool behaves exactly as it always has — no config file is required to
  run `awsops`.
- **Fails loudly on a real mistake.** If the file exists but is
  malformed YAML, `awsops` exits with a clear parse error rather than
  silently falling back to defaults — a botched config that's silently
  ignored could mask a typo (e.g. a misspelled region) behind confusing
  "why isn't my region showing up" behavior.
- **Per-field defaults, not all-or-nothing.** If the file exists and
  parses but a given setting is absent or empty, that setting's own
  built-in default applies. A config file only needs to mention what it
  actually wants to override; it never needs to restate everything.
- **A single flat struct, not a versioned schema.** `internal/config.Config`
  has one YAML-tagged field per setting. Adding a new setting later means
  adding a field, a default constant, and wiring it into whatever
  consumes it — no migration machinery, which would be over-engineering
  for a single-operator-maintained local dotfile (not a multi-tenant
  service config).

### `regions`

```yaml
regions:
  - us-west-1
  - us-west-2
```

Defaults to `[us-west-1, us-west-2]` if unset or the file doesn't exist
(see `DECISIONS.md`, "Narrow configured regions to us-west-1/us-west-2").
These are the regions every region-fanned-out feature (instance/AMI
listing, key pair listing, official Ubuntu AMI lookup, and eventually
Key Management once it ships) iterates over.

### `backup_directories`

```yaml
backup_directories:
  - pattern: "rdm-*"
    directory: /opt/rdm_sql_backups
  - pattern: "newt-*"
    directory: /opt/newt/backups
```

An ordered list of glob patterns (`path.Match` syntax: `*`, `?`,
`[...]`), matched against the picked instance's Name tag, first match
wins. Feature 11 (Backup Archive & Trim) uses the matching rule's
directory to pre-fill its "Backup directory" prompt — still an editable
value, never a silent default, consistent with that prompt's other
fields. No match (including an untagged instance with a blank Name)
leaves the prompt with no default, exactly like today. See
`DECISIONS.md`, "Configure per-instance backup directories by Name
pattern". Built to accommodate, not yet implementing, further settings
this same file would naturally hold: per-domain defaults once
S3/CloudFront ship (e.g. a default bucket), or overrides for the curated
instance-type/Ubuntu-release lists if those ever need site-specific
tuning.

## User Experience Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  awsops — AWS Operations CLI                                    │
├─────────────────────────────────────────────────────────────────┤
│  Pick a domain:                                                 │
│  1) Compute (EC2 & AMI)                                         │
│  2) Key Management                                              │
│  3) S3 (Buckets & Static Websites)                              │
│  4) CloudFront                                                  │
│  5) Exit                                                        │
└─────────────────────────────────────────────────────────────────┘
```

Picking a domain drops into that domain's own listing + menu loop. The
Compute domain (below) keeps today's exact shape:

```
┌─────────────────────────────────────────────────────────────────┐
│  awsops — Compute (EC2 & AMI)                                   │
│  Regions: us-west-1, us-west-2                                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ===== CURRENT EC2 INSTANCES =====                              │
│  ID           Name        State    AMI ID        Region         │
│  i-012345...  web-server  running  ami-abc123...  us-east-1     │
│  i-67890...   db-server   stopped  ami-def456...  us-west-2     │
│                                                                 │
│  ===== AVAILABLE AMIs (owned by account) =====                  │
│  AMI ID          Name              Creation Date    Region      │
│  ami-abc123...  base-ubuntu-2404  2026-01-15      us-east-1     │
│  ami-def456...  app-server-v2     2026-02-20      us-west-2     │
│  ami-ghi789...  custom-ami        2026-03-10      us-east-1     │
│                                                                 │
│  ===== COMPUTE MENU =====                                       │
│  1) Show resource lists                                         │
│  2) Create EC2 instance from AMI                                │
│  3) Create EC2 instance from cloud-init YAML                    │
│  4) Start EC2 instance                                          │
│  5) Stop EC2 instance                                           │
│  6) Terminate EC2 instance                                      │
│  7) Manage tags for an instance or AMI                          │
│  8) Create AMI from EC2 instance (running or stopped)           │
│  9) Remove AMI                                                  │
│ 10) Show/export cloud-init for an instance or AMI               │
│ 11) Archive stale backups to S3 and trim disk space             │
│ 12) Back to domain picker                                       │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

(Illustrative — the real listing also includes Project, Environment,
Public IP, and Private IP columns; see Feature 1 and Feature 12 below.)
Key Management, S3, and CloudFront follow the same listing-then-menu
pattern; their specific
listings and menus are documented under their own feature sections below
rather than repeated here.

## Navigation: Domain Picker

On startup, before any resource listing or menu, the tool shows the
domain picker above. Picking a domain fetches and displays that domain's
resources, then shows a domain-scoped numbered menu — its own "Refresh"
and "Back to domain picker" entries, in addition to that domain's
actions — and returns to that same domain's listing after each action
completes. "Back to domain picker" returns to the picker; "Exit" from
inside any domain menu exits the whole tool, not just that domain, so an
operator working in S3 doesn't have to back out twice.

Domain-specific notes:
- **Compute** fans its resource listing out across all four configured
  regions (Feature 1), unchanged from today.
- **Key Management** also fans out across the configured regions — key pairs
  are a per-region resource.
- **S3** buckets share a single global namespace but each has a home
  region; the listing shows that region per bucket, the same style as
  Compute's per-resource region column today.
- **CloudFront** is a genuinely global service (its control-plane API is
  always `us-east-1`, regardless of where origins live) — its listing is
  not region-fanned-out at all, the one domain that behaves differently
  here.

This structure is additive and mechanical: each domain's menu loop and
resource-listing call were already separable pieces of Compute's existing
single-menu implementation (see "Architecture" below), so introducing the
domain picker is a refactor of `internal/ui`/`internal/workflow`'s menu
wiring, not a rewrite of any of Compute's existing workflows.

## Color Output

When color is enabled (respects `NO_COLOR` and falls back to plain text
on a non-TTY, `ui.ColorEnabled()`), two things are colorized:
- The STATE column in the instance listing (running=green,
  stopped/terminated=red, pending/stopping=yellow).
- Every pick-list prompt's header line (e.g. "Select an instance to
  start"), printed in bold *before* the numbered list it introduces --
  so picking the wrong main-menu action (e.g. Start instead of Stop) is
  visible immediately, without reading through the list first. See
  DECISIONS.md, "Highlight PickList's prompt header when color is
  enabled".

## Terminal UI Architecture: Menus, Actions, Lists, and Managers (Design Addendum, 2026-07-10)

**Status: designed 2026-07-10, implementation starting with the S3
domain.** Supersedes "S3 Resource List Display — Paged, Accessible-
Compatible" above (`internal/ui.PagedTable`/`DisplayBuckets` are
retired, not extended) and the 0.0.1-era framing of huh as merely "the
leading candidate for the next release" (DECISIONS.md, "0.0.1 scope:
ship on termlib as-is..."). Full rationale and rejected alternatives:
DECISIONS.md, "Deprecate termlib; standardize on huh/bubbletea before
0.0.2."

**Motivation.** clasm exists to replace ad hoc AWS Console clicking and
one-off Bash scripts with something a whole team can use fluidly,
without each person memorizing a different command sequence per screen
-- otherwise it offers no real advantage over writing Bash against the
AWS CLI directly. That only works if every screen, however different
its purpose, looks and behaves like part of the same tool. `termlib`
(a stepping-stone library used to figure out the menu/action shape this
tool needed, not the destination) is being removed entirely before
0.0.2 in favor of standardizing on `huh` and `bubbletea` exclusively.

**Taxonomy.** Every navigation path in clasm passes through one or more
*connectors* (never a destination themselves) to reach one of three
*destinations*:

Connectors:
- **Guide menu** — a `huh.Select` today (works well, simple to teach),
  or a small `bubbletea` screen later if a menu ever needs more than a
  flat pick-one, over a small, fixed set of options (e.g. the S3
  domain's 6 actions). Routes the operator toward a destination below.
- **Picker** — chooses *one instance* of a fetched, variable-length
  resource collection (a specific S3 bucket, EC2 instance, AMI, key
  pair, ...) to feed into an action wizard or manager. Distinct from a
  guide menu because the option list is dynamic and can be long enough
  to need scrolling/filtering, not a small fixed menu. See "Picker
  tier" below.

Destinations:
- **Action wizard** — a short prompt sequence (huh fields, or termlib
  prompts as they're migrated off) that gathers parameters and executes
  one thing (Create Bucket, Delete Bucket, ...).
- **List** — a read-only, scrollable display of a resource collection
  (S3 buckets, EC2 instances, AMIs, key pairs). Was
  `internal/ui.PagedTable` (plain sequential prints); becomes a
  `bubbletea` component (below) so it shares real chrome with the
  manager tier instead of approximating it with static text.
- **Manager** — a persistent, stateful `bubbletea` screen for ongoing
  interactive work against a resource. The S3 object manager
  (`internal/filemanager`) is the only one today.

**Shared chrome: `internal/tui`.** The file manager's box-drawing/
legend/scrolling code (`internal/filemanager/view.go`) is already
implemented as pure functions with no dependency on `filemanager.Model`
— `topBorder`, `bottomBorder`, `divider`, `splitDivider`,
`mergeDivider`, `boxLine`, `boxRow2`, `padOrTruncate`, `runeLen`,
`stripANSI`, `truncateVisible`, `scrollWindow`, `styleRow`. These move,
unchanged, into a new `internal/tui` package, and `internal/filemanager`
imports them instead of keeping its own copy — one implementation, not
two that can drift apart. `internal/ui` (`PickList`,
`DisplayInstances`/`Images`/`KeyPairs`, `Confirm`, color helpers) stays
in place for as long as termlib-based call sites remain; it shrinks
over the course of the termlib removal rather than being replaced in
one step.

**List tier: a new `internal/tui` component.** Replaces
`internal/ui.PagedTable`/`DisplayBuckets`. A single bordered box (no
split panes), a frozen header row, a scrollable body reusing the same
cursor-centered `scrollWindow` logic the file manager's panes use,
sized to the real terminal via `tea.WindowSizeMsg` (not a fixed or
computed-from-`termlib` page size — `tea.WindowSizeMsg` is sent to
`Update` once when the program starts and again on every resize, except
on Windows, which has no `SIGWINCH`; an initial size still arrives
there, just no live updates), a legend bar at the bottom, rendered
inline (no `tea.WithAltScreen`, matching every other screen in this
app). Quitting (`q`) returns to the menu it was opened from — for "List
S3 Buckets" that's the S3 menu, not `ErrBackToDomainPicker` (which backs
out of the whole S3 domain, one level further up).

**Picker tier: a new `internal/tui` component.** The user's own framing:
"this UI should feel the same whether I select a bucket, an AMI or an
EC2 instance" -- resource *selection* is exactly the kind of screen
`huh.Select` would otherwise handle, but `huh.Select`'s own rendering is
visually distinct from the bordered-box/legend-bar chrome the List and
Manager tiers use, which would make the S3 domain alone show two
different visual languages depending on whether a screen shows a
resource or picks one. `internal/tui.PickerModel` reuses the exact same
chrome as `ListViewModel` (`TopBorder`/`BoxLine`/`Divider`/`ScrollWindow`/
`StyleRow`/`BottomBorder`) -- same box, same scroll behavior -- but adds
selection: `Enter` chooses the row under the cursor and returns it,
`q`/`ctrl+c` cancels. A dedicated `PickerModel` rather than a
`Selectable bool` flag on `ListViewModel`, matching this project's
existing preference for small, purpose-built components over one
component doing everything (the same reasoning already used to keep the
List tier itself separate from `filemanager.Model`).

Like `ListViewModel`, `PickerModel` works on pre-rendered rows and
returns an *index*, not a typed value, so `internal/tui` doesn't need
Go generics -- each caller maps the chosen index back into its own
typed slice (`buckets[idx]`, `instances[idx]`, ...), the same pattern
`pickS3MenuItem` already uses for `s3MenuItems`.

**Filtering, included from the start** (the user's own request: "this
allows someone to go directly to the thing they want if they know the
name or part of the name"): `/` enters filter-typing mode (matching the
keybinding table below, and `huh.Select`'s own default `/` binding --
not an always-on type-ahead, since `j`/`k` must stay unambiguous
navigation keys), narrows visible rows by case-insensitive substring
match against each row's rendered text, `Enter` commits and keeps
navigating the narrowed list, `Esc` clears it -- the same shape as
`internal/filemanager`'s pane filter and `ui.PickList`'s own existing
substring-filter convention (`filterByLabel`), just applied to a real
chrome-consistent box instead of a plain numbered list or huh's default
field styling.

**The map: every current resource-selection call site.** `internal/ui.PickList`
is used in ~40 places today; most are guide-menu-shaped (a small, fixed
set of actions -- "Choose an option," "Add/Update/Remove," Instance-vs-
AMI kind pickers) and are NOT Picker candidates, they stay as menu-tier
`PickList`/`huh.Select`. The ones below select *one instance of a
fetched resource collection* and are the Picker tier's actual scope,
listed here as the "clear map of specific instances using the common
model" the user asked for -- S3 buckets are the pilot (Phase 20.4);
everything else is deliberately not scheduled yet (see "Not decided
yet" below), listed so the eventual conversions have a concrete
checklist to work from rather than needing to be rediscovered later:

This was the original, preliminary map, written when Phase 20.4 was the
only conversion underway. It's superseded by the "Full conversion punch
list" immediately below, which is the one kept current -- statuses here
are left as a historical snapshot except for the terminal state (every
row below is now done); see the fuller table for the actual phase each
one landed in and, where it differs, which tier it was actually
reclassified into (e.g. "Storage class (transition)" and "Instance type
(curated list)" turned out to be small fixed option sets and became
Menu tier, not Picker tier).

| Resource | Domain | Current call site(s) | Status |
|---|---|---|---|
| S3 bucket | S3 | `bucket_website.go:43`, `bucket_lifecycle.go:530`, `bucket_delete.go:31` | **done, Phase 20.4** |
| S3 lifecycle rule | S3 | `bucket_lifecycle.go:107,447,491` | **done, Phase 20.12 (Picker tier)** |
| Storage class (transition) | S3 | `bucket_lifecycle.go:261,364` | **done, Phase 20.11 (reclassified: Menu tier)** |
| EC2 instance | Compute | `backup_archive.go:77`, `create_ami_from_instance.go:94`, `show_cloud_init.go:35`, `power_state.go:42,113`, `terminate_instance.go:50`, `manage_tags.go:135` | **done, Phase 20.12** |
| AMI | Compute | `launch_from_cloud_init.go:31`, `launch_instance.go:55`, `show_cloud_init.go:60`, `manage_tags.go:154`, `remove_ami.go:61` | **done, Phase 20.12** |
| Subnet | Compute | `launch_prompts.go:43` | **done, Phase 20.12** |
| Instance type (curated list) | Compute | `launch_prompts.go:169` | **done, Phase 20.11 (reclassified: Menu tier)** |
| IAM instance profile / role | Compute | `create_instance_profile.go:71,106` | **done, Phase 20.12** |
| Region | S3, Key Management | `bucket_create.go:26`, `keymgmt_common.go:25` | **done, Phase 20.11 (reclassified: Menu tier)** |
| Key pair | Key Management | `create_key_pair.go:94`, `keypair_delete.go:47` | **done, Phase 20.12** |

**Full conversion punch list (2026-07-10).** The map above covers only
Picker candidates. Per the user's request for "a clear map of specific
instances using the common model" spanning all three targets, here is
every current `ui.PickList`/`ui.Display*` call site in the codebase,
classified by which tier it converts to. Nothing here is scheduled
beyond what's already marked done — this is a checklist to work from,
not a committed roadmap (see "Not decided yet" below).

*Menu tier (→ `huh.Select`, small fixed option sets — not fetched, not
long enough to need scrolling/filtering):*

| Menu | Call site(s) | Status |
|---|---|---|
| S3 domain menu | `s3_menu.go` (`pickS3MenuItem`) | **done, Phase 20.2/20.7** |
| Lifecycle rule action (Add/Edit/Remove/View) | `bucket_lifecycle.go` (`pickLifecycleAction`) | **done, Phase 20.9** |
| Domain picker | `domain_menu.go:60` | **done, Phase 20.10** |
| Compute main menu | `menu.go:85` | **done, Phase 20.10** |
| Key Management menu | `keymgmt_menu.go:59` | **done, Phase 20.10** |
| Instance-vs-AMI kind (show/export cloud-init) | `show_cloud_init.go:22` | **done, Phase 20.11** |
| Instance-vs-AMI kind (manage tags) | `manage_tags.go:119` | **done, Phase 20.11** |
| Tag Add/Update/Remove action | `manage_tags.go:171` | **done, Phase 20.11** |
| Select a tag to update/remove (small, in-memory, per-resource) | `manage_tags.go:196,212` | **done, Phase 20.11** |
| Bucket-purpose enum (Website/Backup/Internal) | `bucket_create.go:71` | **done, Phase 20.11** |
| Region (configured list, S3) | `bucket_create.go:26` | **done, Phase 20.11** |
| Region (configured list, Key Management) | `keymgmt_common.go:25` | **done, Phase 20.11** |
| Instance type (curated static list + "Other") | `launch_prompts.go:169` | **done, Phase 20.11** |
| Storage class, guided backup flow (curated 4) | `bucket_lifecycle.go:296` | **done, Phase 20.11** |
| Storage class, generic editor (full enum) | `bucket_lifecycle.go:399` | **done, Phase 20.11** |
| AZ-incompatibility remediation choice | `instance_type_az_check.go:144` | **done, Phase 20.11** |
| ENA-incompatibility remediation choice | `instance_type_ena_check.go:66` | **done, Phase 20.11** |

*Picker tier (→ `tui.Picker`, fetched/variable-length resource
collections):*

| Resource | Call site(s) | Status |
|---|---|---|
| S3 bucket | `bucket_website.go`/`bucket_lifecycle.go`/`bucket_delete.go` (`pickBucket`) | **done, Phase 20.4** |
| EC2 instance | `backup_archive.go:77`, `create_ami_from_instance.go:94`, `show_cloud_init.go:35`, `power_state.go:42,113`, `terminate_instance.go:50`, `manage_tags.go:135` | **done, Phase 20.12** |
| AMI | `launch_from_cloud_init.go:31`, `launch_instance.go:55`, `show_cloud_init.go:60`, `manage_tags.go:154`, `remove_ami.go:61` | **done, Phase 20.12** |
| Subnet | `launch_prompts.go:43` | **done, Phase 20.12** |
| IAM instance profile (fetched, + none/create-new) | `create_instance_profile.go:71` | **done, Phase 20.12** |
| IAM role (fetched, to attach) | `create_instance_profile.go:106` | **done, Phase 20.12** |
| Key pair (fetched, + create-new) | `create_key_pair.go:94` | **done, Phase 20.12** |
| Key pair (fetched, to delete) | `keypair_delete.go:47` | **done, Phase 20.12** |
| S3 lifecycle rule (view/edit/remove) | `bucket_lifecycle.go:142,482,526` | **done, Phase 20.12** |

*List tier (→ `tui.ListView`, read-only resource displays — the
`ui.Display*` family):*

| Listing | Function | Status |
|---|---|---|
| S3 buckets | `ui.DisplayBuckets` | **done, Phase 20.6** |
| EC2 instances | `ui.DisplayInstances` | **done, Phase 20.13** |
| AMIs | `ui.DisplayImages` | **done, Phase 20.13** |
| Key pairs | `ui.DisplayKeyPairs` | **done, Phase 20.13** |

`ListViewModel` gained the same `/`-filter behavior as `PickerModel`
(Phase 20.14) -- see "Filtering, included from the start" above, which
was written for Picker but always intended for both per the keybinding
table below; the two models now share a `filterState` helper
(`internal/tui/filter.go`) rather than each keeping its own copy.

**Keybinding conventions** (DECISIONS.md, "TUI keybinding
conventions"):

| Key | Action | Where |
|---|---|---|
| `q` | Back to the parent screen | Everywhere |
| `↑`/`↓`, `k`/`j` | Navigate / scroll | Menus, pickers, lists, managers |
| `Enter` | Select / confirm / submit | Menus, pickers, lists, wizards |
| `Esc` | Cancel the *in-progress* action only — never closes a screen | Wizards, in-progress input |
| `/` | Filter | Menus, pickers, lists, managers |
| Legend bar | Always visible at the bottom of every screen, showing that screen's actual keys | Every screen |

Menus (still `huh.Select` for now) can't show a custom footer entry:
huh's help line is built solely from the focused field's own
`KeyBinds()`, and `SelectKeyMap` has no quit/back entry to add one to
without forking huh. `q` is bound at the `Form` level instead
(`Form.WithKeyMap`, adding `"q"` alongside the default `"ctrl+c"` on
`KeyMap.Quit`), which already resolves to the same `huh.ErrUserAborted`
path `RunS3Menu`'s `mapS3MenuPickerErr` maps to `ErrBackToDomainPicker`
— no new dispatch logic needed. Since that won't appear in huh's own
footer, a short static hint line is printed above the menu instead
(e.g. "(q to go back)"), fully within this project's own control.
Picker, list, and manager tiers, which fully own their rendering, show
`q` in a real legend bar instead.

**Accessibility.** Screen-reader/non-TTY accessible rendering is not a
requirement for clasm going forward — it's an internal tool for Library
staff managing AWS resources, not public-facing (distinct from the
Frontend Guidelines' A11y requirement for browser-side Web Components
elsewhere in this workspace, which this doesn't affect). The prior
session's huh-accessible-mode pipe-testability investigation (DECISIONS.md,
2026-07-10, "huh fields are pipe-testable...") remains factually
accurate but is no longer load-bearing for design decisions; testing
shifts to `teatest` (already proven against `internal/filemanager`'s
`Model`) for anything built as a real `bubbletea` component.

**Superseded 2026-07-13** by "Removing termlib: Action Wizards and
Output" immediately below, which is now the committed plan for exactly
this remaining work.

## Removing termlib: Action Wizards and Output (Design Addendum, 2026-07-13)

**Status: designed 2026-07-13, not yet implemented.** Closes out the
"Not decided yet" paragraph above by giving the remaining ~40 `termlib`
call sites (every action wizard, plus `internal/ui`'s lower-level
helpers) a committed conversion plan, per DECISIONS.md, "Remove termlib
entirely: input via huh, output via `io.Writer`." Menu/Picker/List tiers
are unaffected — they're already fully converted (Phase 20.2-20.14).

**Surface audit.** Every remaining `termlib` symbol was traced to its
actual call sites (not just its imports) across the ~44 files that still
reference it:

| Symbol | Refs | Actual usage |
|---|---|---|
| `termlib.Terminal` | 109 | Only `.Printf`/`.Println`/`.Refresh()` are ever called anywhere in this codebase — no cursor movement, no color state (`Move`/`Clear`/`SetFgColor`/etc. are unused). It's used purely as a buffered `io.Writer`. |
| `termlib.LineEditor` | 83 | Only `.Prompt()` is ever called. History (`AppendHistory`/`SetHistory`/`History`), tab-completion (`Completer`), `$EDITOR` composition, and multi-line input (Ctrl+J) are all unused — no call site needs anything beyond single-line text entry with Ctrl+C/Ctrl+D handling. |
| `termlib.PadRight` / `Truncate` | 40 / 12 | Column formatting, `internal/ui/display.go` only. |
| `termlib.Bold` / `Reset` / `Green` / `Red` / `Yellow` | 4 / 5 / 2 / 1 / 1 | ANSI constants, `internal/ui/color.go` (`Highlight`) and `display.go` (`stateColor`) only. |
| `termlib.FormatDuration` | 2 | 10-line `m:ss`/`h:mm:ss` formatter, `progress_ticker.go` and `create_ami_from_instance.go`. |
| `termlib.New` / `NewLineEditor` | 6 / 3 | Constructors — `cmd/clasm/main.go` and tests only. |
| `termlib.ErrInterrupted` | 4 | Ctrl+C sentinel from `LineEditor.Prompt`, checked in `isExitSignal`/`mapMenuPickerErr`-style error mapping. |

Only three files call `le.Prompt()` directly: `internal/ui/prompt.go`
(`Prompt`), `internal/ui/picklist.go` (`PickList` — see below), and
`internal/workflow/confirm.go` (`Confirm`/`ConfirmDestructive`). Every
other file that imports `termlib` merely threads `t`/`le` through its
own signature to reach one of these three, or to call `t.Println`/
`t.Printf` directly for status/error text. This means `le
*termlib.LineEditor` disappears from every signature in the codebase
once these three functions are rebuilt — there is no other direct
caller to migrate.

**`internal/ui.PickList` is dead code.** Every real call site was
already converted to `huh.Select`/`tui.Picker` in the Phase 20.2-20.13
punch list; only comments still reference it (`internal/tui/picker.go`,
`object_browser.go`, `s3_menu.go`). `internal/ui/picklist.go` and
`picklist_test.go` are deleted outright, not migrated.

**Mapping: termlib construct → replacement.**

- **`ui.Prompt`** (free-text input, optional default + validator; ~30
  call sites) → rebuilt on `huh.NewInput()`, following the same
  split-into-testable-core pattern already used for every Menu/Picker
  conversion (Phase 20.2 etc.): a thin public wrapper plus an
  `input io.Reader, output io.Writer`-accepting core that tests drive
  via huh's accessible-mode pipe path. `WithDefault`/`WithValidator`
  map to `huh.Input.Value(&s)` with a default pre-fill and
  `.Validate(func(string) error)` — huh already re-prompts on a
  validator error without any surrounding loop needed.
- **`Confirm`** (y/n, re-prompt on unrecognized input) → `huh.NewConfirm()`.
  The re-prompt-on-bad-input loop disappears entirely: a toggle
  can't produce unrecognized input.
- **`ConfirmDestructive`** (type-to-confirm, single attempt, mismatch
  cancels rather than re-prompting) → `huh.NewInput()` with *no*
  validator (a validator would make huh re-prompt until correct,
  changing the single-attempt semantics); the exact-match check runs
  after the field returns, same as today. The instructional text
  currently printed via `t.Printf` before the prompt becomes the
  field's `.Description()`.
- **Plain status/error output** (`t.Println`/`t.Printf` for things like
  "Exiting.", "Error: %s", the progress ticker's periodic elapsed-time
  line, `loadUserData`'s "looks like an existing file" note) → the
  `*termlib.Terminal` parameter becomes a plain `io.Writer`; `t.Println`/
  `t.Printf` become `fmt.Fprintln`/`fmt.Fprintf`; `t.Refresh()` calls are
  deleted outright (nothing buffers anymore, so there's nothing to
  flush). In the ~9 files where `t`/`le` were pure pass-through (never
  called directly, only forwarded to a callee), the parameter is
  dropped or renamed to `w io.Writer` depending on whether that file's
  own callees still need one.
- **`progress_ticker.go`** — mechanical parity only: `*termlib.Terminal`
  → `io.Writer`, `termlib.FormatDuration` → a local reimplementation
  (same `m:ss`/`h:mm:ss` rounding). No new bubbletea spinner component
  in this pass — explicitly deferred to a later chrome-improvement pass
  (see TODO.md) rather than mixed into a pure removal.
- **`termlib.Bold`/`Reset`/`Green`/`Red`/`Yellow`, `PadRight`/`Truncate`**
  → reimplemented locally in `internal/ui` as the same small set of ANSI
  constants and rune-aware pad/truncate helpers actually used (~20
  lines total). No new dependency (e.g. `lipgloss`) introduced in this
  pass — deferred to the later chrome-standardization pass so this
  removal stays scoped to "delete termlib," not "restyle everything."
- **`termlib.ErrInterrupted`** → once every input path runs through huh,
  Ctrl+C during input surfaces as `huh.ErrUserAborted`, which the
  Menu-tier conversions already map (`mapMenuPickerErr`,
  `huhCancelledIsNil`). Each remaining `errors.Is(err,
  termlib.ErrInterrupted)` check is replaced with the equivalent
  `huh.ErrUserAborted` check at its call site, not a blanket rename —
  some of these sites may find the check is already redundant once
  the underlying prompt is gone.
- **`cmd/clasm/main.go`** — `termlib.New(out)`/`termlib.NewLineEditor(...)`
  construction is deleted; `os.Stdout` is passed directly wherever an
  `io.Writer` is still needed.

**Sequencing.** Unlike the Menu/Picker/List conversions (each
independent, one call site at a time), this refactor changes a type
threaded through nearly every `internal/workflow` function signature —
Go requires the whole module to compile together, so it can't ship as
40 independent single-file changes. See PLAN.md Phase 20.15 (foundational
helpers) and 20.16 (mechanical propagation, domain by domain) for the
ordered work breakdown.

## Chrome Standardization: A Shared lipgloss Palette (Design Addendum, 2026-07-13)

**Status: designed 2026-07-13, not yet implemented.** With termlib gone
(Phase 20.15/20.16), every screen in clasm is now either a `huh` field
or a `bubbletea` component — but they don't yet look like one system.
`huh`'s default theme (`ThemeCharm`) renders a colorful indigo/fuchsia/
cream card with a thick colored left border; `internal/tui`'s List/
Picker/Manager chrome (`box.go`/`style.go`) is plain ASCII box-drawing
with no color at all beyond the cursor row's reverse-video and the
instance-state column's green/red/yellow. An operator moving from a
Menu-tier `huh.Select` into a Picker or List sees two unrelated visual
languages depending on which tier they happen to be in, not a
deliberate design.

**A single shared accent, not a repaint.** Rather than inventing a new
palette, this reuses the one color `huh`'s own default theme already
established and has been on screen since Phase 20.2: the adaptive
indigo `ThemeCharm` uses for focused titles/borders (`#5A56E0` light /
`#7571F9` dark — already light/dark-terminal-aware via
`lipgloss.AdaptiveColor`). Two pieces:

1. **`tui.Theme() *huh.Theme`** — built from `huh.ThemeBase()`
   (structural styling only: spacing, borders-as-shapes, no color) with
   *only* the indigo accent applied to focused titles, borders, and the
   selected-option marker (bold + indigo, mirroring exactly what
   `ThemeCharm` already does for those same elements) — deliberately
   omitting `ThemeCharm`'s fuchsia highlight, cream backgrounds, and
   green/red confirm-button colors. A single accent suits an internal
   ops tool better than a five-color rainbow; this is a restrained
   subset of `ThemeCharm`, not a new invention.
2. **`internal/tui/box.go`'s border/title rendering** (`TopBorder`,
   `BottomBorder`, `Divider`, `SplitDivider`, `MergeDivider`) styled
   with the same indigo + bold via `lipgloss.NewStyle()`. Because
   `ListViewModel`, `PickerModel`, *and* `internal/filemanager` (the one
   Manager-tier screen) all call these same shared functions directly
   (confirmed: no per-tier copies survived the Phase 20.5 extraction),
   styling them once re-skins all three tiers in a single change.

**Deliberately unchanged:**
- **Cursor-row selection** stays reverse-video (`style.go`'s
  `StyleRow`) — the existing mc/ranger/WinSCP-style convention is
  already correct and unrelated to color branding.
- **Instance-state colors** (green=running, red=stopped/terminated,
  yellow=pending/stopping, `internal/ui/display.go`'s `stateColor`)
  stay as-is — semantic data indicators, not decorative chrome.
- **NO_COLOR/non-TTY handling** needs no new plumbing: `lipgloss`
  already detects both automatically (via `termenv`, checking the
  output file descriptor and the `NO_COLOR` env var) and no-ops its own
  ANSI codes accordingly — the existing manual `ui.ColorEnabled()` gate
  stays scoped to the STATE column it already governs, unrelated to
  this addendum's border/title styling.

**Every `huh.NewForm(...)` call site gets `.WithTheme(tui.Theme())`.**
Traced directly (not estimated): there are exactly five constructors in
the whole app — `internal/ui/prompt.go` (`Prompt`), `internal/workflow/
confirm.go` (`Confirm`, `ConfirmDestructive`), `internal/workflow/
domain_menu.go` (`runMenuField`, the Menu tier's shared entry point),
and `internal/workflow/object_browser.go` (`runFieldWithHelp`). Every
other `huh.Select`/`huh.Input`/`huh.Confirm` in the app already funnels
through one of these, a direct consequence of the shared-helper pattern
established across Phase 20.2-20.16 — so five edits cover the entire
app's `huh` surface.

**Progress ticker becomes a real spinner.** `internal/workflow/
progress_ticker.go`'s periodic `"  ... waiting for AMI (elapsed 1:23)"`
printed line is the one place in the app that isn't a `huh` field or a
`bubbletea` component — deferred out of the termlib-removal pass
specifically to avoid mixing "remove termlib" with "improve chrome"
(DECISIONS.md, "Remove termlib entirely..."). It becomes a small
`bubbletea` component using `github.com/charmbracelet/bubbles/spinner`
(already a direct dependency — `bubbles` is the same charm-ecosystem
package `key` already comes from), styled with the same indigo accent,
running inline for the duration of the wait and clearing itself when
the operation completes.

**`object_browser.go`'s bucket pre-flight moves onto `PickerModel`.**
Resolves the one item DESIGN.md's "Terminal UI Architecture" section
left explicitly undecided: `BrowseAndManageObjects`'s `selectBucket`
(a bare `huh.Select` over buckets) is the only bucket-selection call
site in the app that isn't already on `pickBucket`/`tui.RunPicker`
(every other one converted in Phase 20.4). Replacing it makes bucket
selection look identical everywhere, and lets this one call site drop
its own bespoke `huhCancelledIsNil`-for-bucket-selection path in favor
of the same `cancelledIsNil` convention `pickBucket`'s other callers
already use. `confirmLink` (huh.Confirm) and the local-directory
`huh.Input` in the same function are wizard-shaped, not picker-shaped,
and stay as `huh` fields.

## UX Refinements: Contextual Text and a Full Box Border (Design Addendum, 2026-07-13)

Two follow-on refinements to the chrome-standardization pass above,
requested directly after it landed.

**Every Menu/Picker-tier screen gains contextual description text.**
The domain picker (and every other bare-title screen) explained nothing
about what each choice meant — an operator new to a screen had only the
title and the row labels to go on. Every Menu-tier `huh.Select` gains a
`.Description(...)` (huh's own built-in field, previously unused);
`tui.PickerConfig` gains a `Description string`, rendered as its own
line directly below the top border (the same chrome shape `Header`
already has: the line itself plus a `Divider`), above any `Header`/
rows. See `DECISIONS.md`, "Contextual description text on Menu/Picker-
tier screens," for the full call-site inventory and the reasoning
behind which functions got a threaded parameter versus a single
description written directly into the function body. List-tier's
tabular "Show resource lists" displays deliberately don't get this —
they aren't "just a pick list," and their column headers already carry
the relevant context.

**huh fields get a full box border, matching tui's chrome shape, not
just its color.** Phase 20.17 gave `huh` and `tui` the same accent
color, but `huh.ThemeBase()`'s default is a thick bar down the left
side of a field only — not the full `┌─┐│ │└─┘` rectangle `tui/box.go`
draws. `tui.Theme()`'s `Focused.Base`/`Focused.Card` now use
`lipgloss.NormalBorder()` on all four sides, in the shared accent, with
balanced padding replacing the old single-side clearance — see
`DECISIONS.md`, "huh fields get a full box border to match tui's
chrome."

## Full-height Menu Tier (Design Addendum, 2026-07-20)

**Status: designed 2026-07-20.** Resolves the "full height" half of
Phase 20.24's deferred request (see that phase's own note and
`continue_next_time.txt`, 2026-07-14 hand-off), which was deliberately
left unimplemented pending clarification of what "full height" meant
for the huh-based Menu tier. Clarified directly: the wrapping chrome
should carry a real terminal height, and every Menu-tier `huh.Select`
should be told how many rows it has to work with — not just the root
domain picker.

**Mechanism.** `huh.Select.Height(n)` (and the equivalent
`huh.Form.WithHeight(n)`, which cascades to every field in a group)
already does exactly this — confirmed by reading `huh` v1.0.0's source,
not assumed: `updateViewportHeight` (`field_select.go`) subtracts the
title/description lines from `n` before sizing the options viewport,
floored at huh's own `minHeight`; `Select.View()` then renders through
`lipgloss.Style.Base.Height(s.height)`, and lipgloss's `Style.Height`
pads short content with blank lines to reach that height. So a 3-item
menu given `Height(24)` still renders as a 24-line box — the wrapping
chrome stays full-height even when a menu has far fewer than 24
options, matching every List/Picker-tier screen's existing behavior.

**Why this isn't automatic today.** Every Menu-tier `huh.Form` already
receives `tea.WindowSizeMsg` (bubbletea always sends one at startup,
and again on resize except on Windows) whether it runs via the plain
`form.Run()` path or `runMenuField`'s `quitKeyGuard`-wrapped
`tea.NewProgram(guard).Run()` path (filterable fields). But
`huh.Form.Update`'s own `WindowSizeMsg` handling (`form.go:533-554`)
only *shrinks* a group to `min(neededHeight, msg.Height)`, and only
when `f.height == 0` (i.e., nothing has called `WithHeight` yet) — it
never grows short content to fill unused space. Reaching full height
requires explicitly calling `WithHeight` with the real terminal height.

**Live tracking, not a one-shot read.** Rather than reading the
terminal size once via `x/term.GetSize` before constructing the form
(simple, but blind to a resize mid-menu), the Menu tier intercepts
`tea.WindowSizeMsg` itself and calls `WithHeight` on every resize — the
same pattern `internal/tui/picker.go` and `listview.go` already use for
the Picker/List tier, so the Menu tier gains the identical live-resize
behavior instead of a second, weaker mechanism. `runMenuField`'s
existing `quitKeyGuard` wrapper (used today only for the filterable-
field path, to guard the Quit keybinding while filtering) is the
natural place to add this interception — it already wraps `*huh.Form`
in a custom `tea.Model`. The plain `form.Run()` path (used by
non-filterable Menu-tier selects today) has no such wrapper at all;
this addendum extends the same wrapper to that path too, so both go
through one `tea.Model` that both guards the quit key (when filtering)
and maintains full-height sizing (always), rather than diverging by
field type.

**Scope: every Menu-tier `huh.Select`, not just the root domain
picker.** Phase 20.24 explicitly declined to make only the root picker
full-height, reasoning that it would look inconsistent with every
Menu-tier screen one level deeper (S3/EC2/Key Management submenus),
undoing the chrome-consistency work of Phases 20.17-20.25. Since
`runMenuField` is the single shared entry point every Menu-tier
`huh.Select` already runs through (`menu.go`, `s3_menu.go`,
`keymgmt_menu.go`, `domain_menu.go`'s `pickString`/`pickComparable`),
fixing it there gets every depth for free — no per-call-site change.

**Reserved chrome.** `runMenuField` prints its `"(q to go back)"` hint
via a plain `fmt.Fprintln(w, hint)` *before* the form runs, outside the
form's own bordered box — a line of chrome the form's height budget
doesn't otherwise know about. Whatever height gets passed to
`WithHeight` must reserve for this hint line (and the box's own
top/bottom border, already accounted for by `tui.Theme()`'s border
padding), or the combined output ends up one line taller than the
terminal.

**Resolved during implementation (2026-07-20; see PLAN.md Phase
20.26):** the reserved-line count is **2**, not 1 as first assumed here
-- confirmed empirically by rendering a real form (the actual
`tui.Theme()`, not a stand-in) at a known `WithHeight` and counting the
output. One line is the hint; the second is `huh.Form`'s own trailing
help/keybindings footer, which renders *below* whatever height
`WithHeight(n)` was given (a form asked for height `n` renders `n+1`
lines total) -- not something this addendum had accounted for.
`tui.Theme()`'s border/padding turned out not to change the count at
all. `runMenuField`'s non-filtering `form.Run()` path was folded into
the same `quitKeyGuard`-wrapped path rather than left as a second
mechanism -- in practice every current call site already builds a
`*huh.Select` (which satisfies `filteringField`), so that path was
already dead code, but unifying it means a future non-Select field
costs nothing extra.

## Launch Templates (Design Addendum, 2026-07-20)

**Status: designed 2026-07-20, targeted for v0.0.2 alongside the IMDSv2
fix below.** v0.0.1 is already piloting in production (unreleased --
no git tag yet, `version.go` still reads 0.0.1 -- but in active use),
so this is additive: new Compute-domain menu entries and a new EC2
client surface, no change to any existing v0.0.1 workflow's behavior.
Directly requested (notes-from-tom.txt) and confirmed as v0.0.2's
headline feature. Folds in the IMDSv2 bug (TODO.md, Bugs) as one design
pass, since both touch the same `MetadataOptions` concept. The
tags-screen fix, backup-bucket-default, and top-level cross-resource
tag management (TODO.md's other open items) are deliberately **out of
scope here** -- v0.0.2 material, but their own design pass.

**The operator's actual flow** (clarified directly, not assumed): a
cloud-init YAML file is authored first; it gets applied to create a
launch template, which "more fully encapsulates" what a running
instance needs (RDM's software requirements, primarily). Over time the
YAML changes (e.g. RDM adds a new dependency) and that change is
applied as a **new version** of the existing template -- never an
in-place edit, matching the operator's own framing of "new template
version" as the upgrade unit. `CreateLaunchTemplate`/
`CreateLaunchTemplateVersion` fit this directly: both take a
`LaunchTemplateData`/`RequestLaunchTemplateData` struct (AMI, instance
type, subnet, security groups, IAM instance profile, tags, `UserData`,
`MetadataOptions`, ...) constructed directly by the caller -- there is
no dependency on an existing EC2 instance (that's a different, unused
API: `GetLaunchTemplateData` *derives* a template from a running
instance's live config, which is not this flow). So "cloud-init YAML →
new template" and "cloud-init YAML → new version of an existing
template" are both just a template-data struct built from the same
AMI/instance-type/subnet/security-group/IAM-profile/tag prompts
Feature 3 already collects, with the YAML's content (base64-encoded)
as `UserData`.

**New EC2 client surface.** `internal/awsclient/ec2.go`'s `EC2API`
interface (currently ~20 methods, one line per SDK call, no
per-feature narrowing) gains: `CreateLaunchTemplate`,
`CreateLaunchTemplateVersion`, `DescribeLaunchTemplates`,
`DescribeLaunchTemplateVersions`, `ModifyLaunchTemplate` (sets which
version number `$Default` points to, via its `DefaultVersion *string`
field), `DeleteLaunchTemplate`, `DeleteLaunchTemplateVersions` --
mirrored into `logging_ec2.go`'s wrapper, matching how every existing
EC2 call is already logged.

**Data model.** A new `internal/inventory.LaunchTemplate` (list-tier,
one row per template, aggregated across regions like `Image`/`Instance`
already are): `TemplateID`, `Name`, `DefaultVersion`, `LatestVersion`,
`Region`, `Project`, `Environment` (from `DescribeLaunchTemplates`,
whose tags come back the same `[]types.Tag` shape `Image`/`Instance`
already decode). A separate per-version detail type (returned by
`DescribeLaunchTemplateVersions` for one template) carries the fields
actually shown/edited: version number, create date, AMI, instance
type, IAM instance profile, security groups, subnet, tags, and
`MetadataOptions` (below). Deliberately **not** modeling the rest of
`RequestLaunchTemplateData`'s much larger surface (block device
mappings, network interfaces, capacity reservations, CPU options, ...)
-- out of scope, matching this project's existing preference for a
curated field set over the full AWS struct (the same restraint
`Image`/`Instance` already apply).

**Two distinct SDK types for "IMDSv2 required," not one.** AWS uses
`types.HttpTokensState`/`HttpTokensStateRequired` for a plain
`RunInstances`/`ModifyInstanceMetadataOptions` call, but a *different*
type, `types.LaunchTemplateHttpTokensState`/
`LaunchTemplateHttpTokensStateRequired`, inside a template's
`LaunchTemplateInstanceMetadataOptionsRequest` -- confirmed by reading
the SDK's `enums.go`, not assumed. Both get set going forward (see
"IMDSv2 enforcement" below); implementation needs to use the right one
in each of the two call sites rather than assuming one type covers
both.

### New Compute-domain menu entries

Peer entries alongside the existing ones (`menu.go`'s `mainMenuItems`),
not nested under a new sub-menu -- matching how Create-from-AMI and
Create-from-Cloud-Init already sit side by side rather than behind a
"Create" sub-picker:

- **Create EC2 Instance from Launch Template** -- a third way to
  create an instance, alongside Feature 2 (from AMI) and Feature 3
  (from cloud-init YAML). Pick a template, then a version: the prompt
  pre-fills `$Default` (matching Backup Archive & Trim's own
  recalled-but-overridable default convention, DECISIONS.md "Recall
  Backup Archive & Trim's instance/directory choices per-instance") but
  stays editable to an explicit version number or `$Latest` before the
  launch executes. Collects nothing else -- the template supplies
  every other launch parameter; this is deliberately not a hybrid
  "template plus override individual fields" wizard, since that was
  the earlier design-conversation confusion (A3) the operator
  explicitly resolved: it's just another way to create an instance,
  parallel in shape to Features 2 and 3, not a variant of either.
- **List Launch Templates** -- folds into the existing "Show resource
  lists" List-tier display as a new resource type, alongside
  instances/AMIs, not a separate top-level action.
- **Show Launch Template** -- pick a template, pick a version (default
  pre-selected), display the curated detail fields above. Modeled on
  the existing AMI summary display (the same modest field set
  `Image`/AMI-related screens already show: ID, name, creation
  date/version, region, tags) plus `MetadataOptions`, shown explicitly
  because it's the field the IMDSv2 work below cares about. If
  `HttpTokens` isn't `required`, the display flags it right there
  (passive -- no separate audit action; the operator asked for
  "recommended for existing templates if missing," not a dedicated
  scan).
- **Create Launch Template from Cloud-Init YAML** -- collects a YAML
  file path (identical prompt to Feature 3, including reading from a
  file only, never inline text) plus the same AMI/instance-type/
  subnet/security-group/IAM-profile/tag prompts Feature 3 already
  asks, then `CreateLaunchTemplate` (which implicitly creates version
  1). `MetadataOptions` is set to required unconditionally -- not a
  prompt, since this is a security default, not an operator choice
  (mirrors IMDSv2 enforcement below).
- **Sync Cloud-Init YAML to a Template** -- pick an existing template,
  pick a YAML file, and:
  1. Fetch the version to compare against (the version the operator
     picks -- normally `$Default`), decode its `UserData`.
  2. Compare the decoded text against the local YAML file's content.
     If identical, report "no changes -- nothing to sync" and stop; no
     version is created. This is the no-op detection the operator
     asked for (A10) -- Tom's own framing of "does this actually
     require a new version" before bumping one.
  3. If different, render a plain-text unified diff (A8: start simple)
     via `github.com/aymanbagabas/go-udiff`'s `Unified(oldLabel,
     newLabel, old, new string) string` -- already present in
     `go.sum` as an *indirect* dependency (pulled in transitively via
     `charmbracelet/x/exp/teatest`, used by `internal/filemanager`'s
     own tests), so this promotes it from indirect to direct rather
     than introducing a new one -- the same move Phase 20.24 already
     made for `x/ansi`.
  4. Show the diff, require explicit confirmation, then
     `CreateLaunchTemplateVersion` with the new `UserData`.
  5. **Never** auto-promotes the new version to default (A9) -- that's
     always the separate action below. The operator specifically
     expects to experiment with in-progress template versions during
     development without accidentally changing what a plain
     "Create from Launch Template" launch picks up by default.
- **Promote Launch Template Version to Default** -- `ModifyLaunchTemplate`
  with `DefaultVersion` set to the chosen version number. Its own
  explicit action, never a side effect of Sync.
- **Delete Launch Template Version(s)** -- prune specific stale
  versions (`DeleteLaunchTemplateVersions`) without touching the whole
  template -- "so no one accidentally chooses them" (A5), e.g. an
  abandoned experimental version from mid-development.
- **Delete Launch Template** -- removes the whole template
  (`DeleteLaunchTemplate`), for when the software system it was built
  for is retired entirely. Both deletion actions go through the same
  safety-first shape Feature 9 (Remove AMI) already established: show
  what would be deleted, an explicit extra warning if the template (or
  the version, for a single-version prune) is tagged
  `Environment=production`, then type-to-confirm.

### Tagging and safety-gate parity (A6)

`types.ResourceTypeLaunchTemplate` exists in the SDK, so
`CreateLaunchTemplate`'s own `TagSpecifications` field can reuse
`launch_execute.go`'s existing `buildTagSpecification(resourceType,
tags)` unchanged -- it already takes a `types.ResourceType` parameter,
not one hardcoded to instances. The Project/Environment convention
(Feature 12) and the `Environment=production` confirmation gate
(Feature 9's pattern) both extend to launch templates exactly as
described above.

### IMDSv2 enforcement (closes the TODO.md bug, extends to templates)

Three call sites, all going to `required` going forward, none of them
a prompt (this is a security default, not an operator choice, per the
operator's own framing: "we want to follow security recommendations by
default"):

1. **Plain `RunInstances`** (Features 2 and 3, `launch_execute.go`) --
   currently sets no `MetadataOptions` at all (confirmed by reading
   `launch_execute.go:51-60` -- the original TODO.md bug). Gains
   `MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens:
   types.HttpTokensStateRequired}`.
2. **Every new launch template** (`CreateLaunchTemplate`) -- its
   `RequestLaunchTemplateData.MetadataOptions` gets
   `types.LaunchTemplateInstanceMetadataOptionsRequest{HttpTokens:
   types.LaunchTemplateHttpTokensStateRequired}` unconditionally.
3. **Existing templates missing it** -- flagged passively in Show
   Launch Template / List Launch Templates (above), not auto-fixed --
   changing an existing template's `MetadataOptions` is a new version,
   same as any other template change, and shouldn't happen silently
   behind an operator's back.

### Not decided yet

Left for the implementation plan: the exact curated field list for the
per-version detail type (beyond the fields named above); whether
`ModifyLaunchTemplateVersion`'s description-setting capability (a
per-version free-text label, separate from the `UserData` itself) is
worth surfacing; and test coverage shape for the diff/no-op-detection
path (`go-udiff`'s own test suite covers the algorithm itself -- this
project's tests need to cover the identical-content-skips-a-version
and different-content-shows-a-diff-then-creates-a-version branches,
via the same accessible-mode pipe-testing convention every other
Menu-tier workflow already uses).

### Real-usage follow-ups (2026-07-20)

The first real-AWS pass over this feature (create a template, launch
from it, sync, promote, list, delete) surfaced one bug and three UX
gaps, all addressed the same day -- see `DECISIONS.md`, "Accept
`v`-prefixed launch template versions" and "Launch Template version
history, scrollable diffs, and split Show resource lists," and
`PLAN.md` Phase 20.28:

- AWS rejects a `"v"`-prefixed version number outright; this project's
  own display convention (`launchTemplateLabel`'s "default v2") made
  typing one a near-certainty. Fixed at the input boundary
  (`normalizeVersionSelector`), not by changing the display format.
- Show Launch Template gained "list all versions" and "diff two
  versions" alongside its original single-version detail view --
  "there's another version" alone wasn't enough to know what changed.
- Sync's confirmation diff (and the new version-diff) render through
  the shared List-tier component instead of a raw, potentially-
  off-screen text dump.
- Compute's "Show resource lists" split into three menu entries (Show
  instances/AMIs/launch templates) -- paging through all three to
  reach the one wanted felt awkward. S3/Key Management, each with a
  single resource type, were left alone.

## Core Features

### Compute Domain (EC2 & AMI)

Features 1 through 12 below are unchanged in behavior from the original
single-menu design — only their position in the navigation changes (see
"Navigation: Domain Picker" above).

### 1. Unified Resource Listing

On startup, the tool fetches and displays:
- All EC2 instances across the configured regions
- For each instance: ID, Name (from tags), State, AMI ID, Region, Project
  and Environment (from tags — see "Project/Environment Tagging" below;
  shown as "unknown" if untagged), and Public/Private IP (shown as
  "none" if the instance has neither, e.g. stopped or launched without a
  public IP — see `DECISIONS.md`, "Show instance IP addresses in the
  main listing"; this is what makes it possible to look up which
  instance to `ssh` into without a separate lookup step)
- All AMIs owned by the current AWS account across the configured regions
- For each AMI: ID, Name, Creation Date, Region, Project and Environment
- Listing can be grouped/filtered by Project and by Environment, so
  "show me everything for caltechauthors" or "show me only production" is
  a quick operation instead of scanning a flat list by name

### 2. Create EC2 Instance from AMI

Interactive workflow:
1. Display pick list of available AMIs — the account's own AMIs (owned-
   by-account, as before) plus a short curated list of official Ubuntu
   LTS releases (currently 24.04 and 22.04, amd64 only), so launching
   from a fresh base image doesn't require first copying a public AMI
   into the account by hand. See `DECISIONS.md`, "Offer official Ubuntu
   LTS AMIs alongside owned AMIs when picking a base AMI" — this is a
   curated addition, not a general public-AMI browser; anything more
   exotic (arm64/Graviton, a different distribution, a specific non-LTS
   release) still means copying that specific public AMI into the
   account first, same as before this addition.
2. User selects an AMI
3. Prompt for required parameters:
   - Instance type: a pick list of a curated shortlist relevant to this
     team's actual usage (t3/m5/c5/r5 family, plus t2.micro/t2.medium —
     the list's only non-Nitro, no-ENA-required entries, included after
     real-world use hit a legacy AMI no ENA-requiring type could ever
     launch; ~11 entries total), each labeled with vCPU/memory, plus
     "Other" to type any value not listed — not a full AWS catalog
     listing (600+ types per region), which would reproduce the "flat
     list is noise, not help" problem already found with key pairs at a
     much larger scale. See `DECISIONS.md`, "Instance type pick list:
     curated shortlist, not the full AWS catalog" and "Add non-ENA-
     required options to the curated instance type list". Once picked,
     checked against the AMI's ENA support and
     (after Subnet ID, below) the subnet's Availability Zone —
     see those two items below and `DECISIONS.md`, "Pre-flight check:
     instance type vs. AMI ENA support" / "... vs. subnet Availability
     Zone"
   - Key pair name: a pick list of key pairs that actually exist in the
     AMI's region (`ec2:DescribeKeyPairs`), plus "Create new key pair"
     (which calls `ec2:CreateKeyPair`, saving the private key to
     `~/.ssh/<name>.pem` with `0600` permissions; see "Debug Logging"
     below for why its response is handled specially in the `-debug`
     log). Unlike Security group IDs/Subnet ID, there's no "Other: type
     a name" escape hatch — key pairs are a complete, small,
     fully-enumerable list per region, so a name outside it is
     guaranteed not to work there. Falls back entirely to the original
     free-text prompt (typing `new`, or a private key filename/path —
     `/`, `~`, or `.pem`/`.ppk`/`.key` — validated as readable and
     resolved to its AWS name, since `ssh -i` muscle memory makes typing
     the file a real, recurring mistake) only if the list itself can't
     be fetched. See `DECISIONS.md`, "Validate key pair name against the
     AMI's region" and "Derive the AWS key pair name from a private key
     filename/path".
   - Security group IDs (list available security groups)
   - Subnet ID (list available subnets, narrowed to Availability Zones
     that actually support the instance type chosen earlier —
     `ec2:DescribeInstanceTypeOfferings` — so an incompatible subnet is
     never offered in the first place; falls back to the full,
     unfiltered list if that lookup fails or would filter to nothing.
     See `DECISIONS.md`, "Filter the subnet picker by instance-type
     Availability Zone support"). As a safety net for whatever this
     filtering can't cover (lookup failure, free-text fallback with an
     unknown AZ), the picked subnet is still checked against the
     instance type after the fact — if AWS still wouldn't accept the
     pairing, a pick list offers to change the instance type, pick a
     different subnet, or abort the launch, instead of either a dead
     end or a doomed `RunInstances` call. See `DECISIONS.md`,
     "Pre-flight check: instance type vs. subnet Availability Zone".
   - IAM instance profile (optional; list available instance profiles via
     `iam:ListInstanceProfiles`, offering "(none)" to skip and "Create new
     instance profile (attach an existing role)" to create one on the
     spot — pick a role via `iam:ListRoles`, then
     `iam:CreateInstanceProfile` + `iam:AddRoleToInstanceProfile`; falls
     back to a free-text prompt only if the list itself can't be fetched.
     See `DECISIONS.md`, "Support picking or creating an IAM instance
     profile from within awsops" — this replaced an earlier free-text-only
     prompt that pointed at "IAM console > Roles," which real-AWS testing
     showed leads operators to type a role name where AWS actually
     expects the (often differently named) instance profile name,
     producing AWS's own "Invalid IAM Instance Profile name" error)
   - User data (optional) — a cloud-init YAML or any other user-data
     script, entered inline **or loaded from a local file path** (e.g.
     pointing at a file from a local clone of `cloud-init-examples`),
     prefixed with `@` (e.g. `@newt-machine.yaml`). A bare filename
     typed without the `@` (a real mistake found in use) is auto-
     detected and loaded anyway if a file actually exists at that path
     — a bare filename is never valid literal user-data, so silently
     using it as inline text would launch the instance with that string
     as its user-data instead of the file's contents. See `DECISIONS.md`,
     "Auto-detect a bare existing-file path in User data / Cloud-init
     YAML input".
   - Tags — `Name` (required), `Project` and `Environment` (suggested; see
     "Project/Environment Tagging Convention" below), plus any additional
     free-form tags
4. Confirm all parameters before launching
5. Launch instance; poll until `running` — tolerates AWS's own brief
   post-launch `InvalidInstanceID.NotFound` window (the new instance ID
   isn't always immediately visible to `DescribeInstances`) rather than
   treating it as a failure; see `DECISIONS.md`, "Tolerate
   DescribeInstances' post-RunInstances eventual-consistency window"
6. **If user-data was provided**: wait for SSM to report `Online` and run
   `cloud-init status --wait` (bounded timeout — see `DECISIONS.md`,
   "Enhance Create Instance from AMI: cloud-init file input + completion
   check"), reporting cloud-init's actual completion status (`done` vs
   `error`), not just that the instance reached `running`. If SSM never
   comes online, skip this check cleanly (not an error) — not every AMI
   has SSM configured. Tolerates AWS's own brief post-`SendCommand`
   `InvocationDoesNotExist` window the same way step 5 tolerates
   `DescribeInstances`' post-`RunInstances` window; see `DECISIONS.md`,
   "Tolerate GetCommandInvocation's post-SendCommand eventual-consistency
   window"
7. Display connection info (public/private IP, SSH command)

**See also:** Feature 3 shares this exact execution path but leads with
the cloud-init file as the primary input (pick a base AMI second) rather
than treating user-data as one optional parameter among several.

### 3. Create EC2 Instance from Cloud-Init YAML

Interactive workflow (see `DECISIONS.md`, "Add Create EC2 Instance from
Cloud-Init YAML as a v1 primitive"). Shares its underlying execution with
Feature 2 entirely — same launch/poll/cloud-init-completion-check logic —
but a different entry point: the cloud-init file is the primary input,
not one optional parameter among several.
1. Prompt for a cloud-init YAML **file path** — unlike Feature 2's
   optional "User data" field, this prompt always reads from disk
   rather than also accepting inline text: real cloud-init YAML is
   realistically always a file (e.g. from a local clone of
   `cloud-init-examples`), never something typed inline at a terminal.
   A leading `@` is tolerated (muscle memory from Feature 2's prompt)
   but not required. Re-prompts on a missing/unreadable file rather
   than silently using the value as literal text — see `DECISIONS.md`,
   "Create EC2 Instance from Cloud-Init YAML always reads from a file"
2. Pick a base AMI to launch it on (same pick list as Feature 2)
3. Collect the remaining launch parameters — instance type, key pair,
   security groups, subnet, IAM profile, tags — identical to Feature 2
4. Confirm all parameters before launching
5. Launch; poll until `running`
6. Wait for SSM `Online` and run `cloud-init status --wait` (bounded
   timeout), reporting completion status (`done` vs `error`) — same
   mechanism as Feature 2's step 6
7. Display connection info

Not to be confused with the deferred "Bake AMI from cloud-init" idea
(which snapshots the result into a new AMI and terminates the instance) —
this primitive leaves a real, running, usable instance.

### 4. Start EC2 Instance

Interactive workflow:
1. Pick a stopped instance
2. Confirm (simple yes/no — starting is safe and reversible, the
   symmetric counterpart to Feature 5)
3. Call `ec2:StartInstances`
4. Poll until `running` (bounded timeout)
5. Display connection info (public/private IP, SSH command) — a restarted
   instance's public IP may have changed unless it uses an Elastic IP

### 5. Stop EC2 Instance

Interactive workflow:
1. Pick a running instance
2. Confirm (simple yes/no — stopping is reversible; data on EBS volumes
   persists and the instance can be started again)
3. Call `ec2:StopInstances`
4. Poll until `stopped` (bounded timeout)

### 6. Terminate EC2 Instance

Safety-first workflow — same tier as Feature 9 (Remove AMI), since
termination is permanent:
1. Pick an instance
2. **Dry-run first**: show what would be destroyed, **including whether
   any attached EBS volume has `DeleteOnTermination=true`** — that
   volume's data (potentially including not-yet-archived backups; see
   Feature 11) is destroyed along with the instance, not just the
   instance itself
3. **Environment check**: if tagged `Environment=production`, show an
   additional, explicit warning before type-to-confirm
4. **Type to confirm**: user must type the instance ID or name exactly
5. Execute via `ec2:TerminateInstances`
6. Confirm successful termination

### 7. Manage Tags

A general-purpose action, distinct from the Project/Environment Tagging
Convention (Feature 12) below — see "Manage Tags vs. the Tagging
Convention" at the end of this feature. Interactive workflow (see
`DECISIONS.md`, "Broaden Rename Instance into a general Manage Tags
primitive"):
1. Pick a resource — an instance or an AMI
2. Display its current tags
3. Choose an action:
   - **Add**: prompt for a new key and value
   - **Update**: pick an existing key from the list, prompt for a new value
   - **Remove**: pick an existing key from the list
4. Confirm (a simple yes/no — this is cheap and reversible, unlike
   Feature 9's destructive dry-run/type-to-confirm tier)
5. Call `ec2:CreateTags` (add/update) or `ec2:DeleteTags` (remove)

Renaming an instance is simply updating its `Name` tag through this same
flow — there is no separate "rename" operation. This does **not** apply to
an AMI's `Name` attribute itself, which cannot be changed via the AWS API
once set (see the note under Feature 8 above) — Manage Tags only ever
touches tags, never that attribute. Editing the `Environment` tag
specifically is worth a brief on-screen note that it's the same tag used
elsewhere in this tool to gate production-safety warnings.

**Manage Tags vs. the Tagging Convention:** Manage Tags is the general
*mechanism* — it edits any tag key, any time, on demand. The
Project/Environment Tagging Convention (Feature 12) is the *policy* that
gives two specific tag keys meaning elsewhere in this tool (defaults
during creation, safety gates during destruction). Manage Tags is how
you'd edit an `Environment` tag after the fact; the Convention is why
`Environment` matters to this tool in the first place.

### 8. Create AMI from EC2 Instance

Interactive workflow, including capabilities ported from `ami_copy.bash`
(see `DECISIONS.md`, 2026-06-30 "AMI-from-instance: fold ami_copy.bash
capabilities into Phase 5") from day one rather than as a later addition:
1. Display pick list of EC2 instances (running or stopped)
2. User selects an instance
3. Gather attached-volume info; show total size and an estimated creation
   time (see table under "Domain Knowledge Carried Forward" below)
4. If the instance is running, offer an SSM `fstrim` pass before
   snapshotting (skip cleanly if SSM is unavailable) and show the
   Postgres/OpenSearch/Redis/Docker crash-consistency guidance
5. Prompt for:
   - AMI name (suggested default: `<instance-name-or-id>-copy-<date>`,
     user may override)
   - AMI description (optional)
   - No-reboot flag (default: false; only offered for running instances)
   - Tags — `Project` and `Environment` default to the source instance's
     tags (if present), plus any additional free-form tags
6. Confirm before creating
7. Create AMI, then poll (unbounded — large Invenio RDM volumes can take
   20–60+ minutes) until `available` or `failed`, displaying elapsed time

**Note:** an AMI's `Name` is immutable via the AWS API once set here —
there is no "rename AMI" operation (see `DECISIONS.md`, "Add Rename
Instance as a v1 primitive; AMI Name is immutable"). The default name
suggestion above is still offered; make sure it's right before confirming,
since only `Description` can be changed after the fact.

### 9. Remove AMI

Safety-first workflow:
1. Display pick list of owned AMIs
2. User selects an AMI
3. **Dry-run first**: Show what would be deleted
4. **Show dependencies**: List any instances currently using this AMI
5. **Environment check**: If the AMI is tagged `Environment=production`,
   show an additional, explicit warning before the type-to-confirm step
6. **Type to confirm**: User must type the AMI ID or name exactly to proceed
7. Execute deletion
8. Confirm successful removal

### 10. Show/Export Cloud-Init

Interactive workflow for detecting drift between a deployed instance/AMI
and the team's canonical templates in `caltechlibrary/cloud-init-examples`
(see `DECISIONS.md`, "Add Show/Export Cloud-Init as a v1 primitive"):
1. Pick an instance or an AMI
2. **Instance path** (free, instant): call `ec2:DescribeInstanceAttribute`
   (attribute `userData`), base64-decode, display. If no user-data was
   set at launch, say so plainly — not an error
3. **AMI path** (costs real time/money — explicit confirmation required
   before proceeding): launch a temporary, disposable instance from the
   AMI; wait for it to reach `running` and for SSM to report `Online`
   (bounded timeout — this is a diagnostic operation, not core creation,
   so it fails cleanly rather than polling unboundedly like Feature 8);
   run an SSM command to read `/var/lib/cloud/instance/user-data.txt` off
   disk; decode and display it; **always terminate the temporary instance
   afterward**, including if SSM never comes online or the command fails
4. **Export**: offer to save the decoded YAML to a local file path,
   for manual comparison against a local clone of `cloud-init-examples`
   (no inline fetch-and-diff against the GitHub repo in v1 — see
   "Deferred to a Later Version" below)

### 11. Backup Archive & Trim

Interactive workflow for turning today's manual "log in and delete old
backups" chore into a supervised, verified operation (see `DECISIONS.md`,
"Add Backup Archive & Trim as a v1 primitive"). This is a genuinely
destructive workflow (it deletes real backup files), so it gets the same
safety tier as Feature 9 (Remove AMI):
1. Pick an instance, immediately followed by a `command -v aws`
   preflight check on that instance (see `DECISIONS.md`, "Preflight
   check: AWS CLI availability before Backup Archive & Trim") — aborts
   fast with a clear, actionable error if the AWS CLI isn't installed,
   before any further prompt or the dry-run list
2. Prompt for the backup directory, then the S3 bucket, then the age
   threshold in days — in that order (see `DECISIONS.md`, "Reorder
   Backup Archive & Trim's prompts": the threshold reads more naturally
   once both the source directory and destination bucket are already
   fixed). The instance picker's cursor and the directory prompt's
   default both recall what was actually used last time *for this
   specific instance* (`~/.clasm_state`, an app-managed file distinct
   from `~/.clasm` — see `DECISIONS.md`, "Recall Backup Archive & Trim's
   instance/directory choices per-instance"), taking priority over
   `~/.clasm`'s `backup_directories` Name-pattern match (see
   "Configuration" above; e.g. RDM instances default to
   `/opt/rdm_sql_backups`, other services to their own directory) when
   both exist. Either way the directory stays editable and is never
   silently accepted; no recalled value and no rule match leaves it
   unset, same as before either mechanism existed. The age threshold
   itself has no default — always an explicit, deliberate choice. The S3
   bucket prompt itself is a filterable pick list of this account's
   buckets (`'/'` to filter by name), plus an "Other" entry to type any
   bucket name directly — e.g. one outside this account's own listing
   (see `DECISIONS.md`, "Bucket picker for Backup Archive & Trim");
   falls back to a plain free-text prompt if the bucket listing itself
   can't be fetched. Immediately followed by `s3:GetBucketLocation` to
   discover which region the bucket actually lives in (any region,
   unrelated to the instance's — see `DECISIONS.md`, "Resolve a bucket's
   actual region before Backup Archive & Trim's access check") and then
   an `s3:HeadBucket` access check, scoped to that region, that aborts
   with a clear reason (bucket doesn't exist, or the operator's own
   credentials can't reach it) before any of the steps below run — see
   `DECISIONS.md`, "Preflight check: S3 bucket access before Backup
   Archive & Trim's dry-run list"
3. **Dry-run list** (SSM, read-only): show candidate files matching the
   age threshold, with size and age, before anything happens
4. **Type to confirm** before proceeding
5. **Upload phase** (SSM): the instance uploads each candidate file to
   `s3://<bucket>/<instance-name>/<filename>` via its own AWS
   CLI/credentials — every key namespaced by the source instance's Name
   tag (falling back to its instance ID if untagged) so backups from
   different systems sharing one bucket can't collide (see
   `DECISIONS.md`, "Namespace backup uploads by instance") — one
   `ssm:SendCommand` per file (SSM Run Command cannot bulk-transfer
   multi-hundred-MB files — see Feature 10's AMI-path constraint),
   printing a live "N/M (bytes of total) — OK/FAIL key" line as each
   file completes rather than a generic heartbeat (see `DECISIONS.md`,
   "Per-file upload progress for Backup Archive & Trim"). The remote
   `aws s3 cp` runs with `--only-show-errors` so its own progress-meter
   output can never fill `ssm:GetCommandInvocation`'s 24,000-character
   output cap and truncate away this script's own OK/FAIL signal on a
   large file (see `DECISIONS.md`, "Suppress aws s3 cp's progress output
   to avoid truncating the OK/FAIL signal"). Nothing is deleted at this
   point
6. **Independent verification**: the tool itself — using its own AWS
   credentials, not the instance's self-report — calls `s3:HeadObject` on
   every uploaded key and confirms it exists with the expected size
7. **Delete phase**: a *second*, separate SSM command tells the instance
   to delete exactly the tool-verified file list (the instance does not
   re-derive its own "what's stale" list, avoiding a
   time-of-check/time-of-use gap)
8. **fstrim** to reclaim the freed blocks, then a report of bytes freed
   and any files that failed verification (left untouched, not deleted)

This primitive requires the target instance's IAM instance profile to
grant `s3:PutObject` (and likely `s3:ListBucket` scoped to its prefix) on
the destination bucket — a cloud-init/AMI-level change, not something this
tool can grant from outside. See "Assumptions" below.

### 12. Project/Environment Tagging Convention

Not a user-facing menu item, but a cross-cutting policy applied by
instance/AMI creation (Features 2, 3, 8) and destructive operations
(Features 6, 9) above (see `DECISIONS.md`, "Introduce a light
Project/Environment tagging convention"). See "Manage Tags vs. the
Tagging Convention" under Feature 7 above for how this differs from the
general-purpose tag-editing primitive.
- `Project` groups resources by RDM application (e.g. `caltechauthors`,
  `caltechdata`, `caltechthesis`) — the tool suggests the source
  instance/AMI's existing value as a default where one exists
- `Environment` is one of `production`, `development`, or `test` — the
  tool does not guess this from the resource name; it is always an
  explicit prompt (with no default) so a "production" tag is a deliberate
  choice
- The tool never rewrites tags on resources it didn't create; existing
  untagged resources simply display as "unknown" until someone tags them

### Key Management Domain

Key pairs are already touched by Compute (Feature 2's inline "type `new`"
shortcut during instance launch), but were never first-class, listable,
deletable resources in their own right. This domain makes them one.

### 13. List Key Pairs

Resource listing shown when the Key Management domain is entered: for
each region, `ec2:DescribeKeyPairs`, aggregated into one table (Name,
Region, Type, Fingerprint or Key ID). This is the listing this domain's
menu sits below, not a separate menu action.

### 14. Create Key Pair

Interactive workflow — the standalone form of what Feature 2's inline
"type `new`" shortcut already calls under the hood, so both share one
underlying primitive:
1. Prompt for a name (must be unique within its region)
2. Prompt for region (pick list)
3. Call `ec2:CreateKeyPair` (ED25519, PEM)
4. Save the private key to `~/.ssh/<name>.pem` at `0600`
5. Confirm success; remind the operator where the private key landed — it
   is never displayed or logged again (see Security Considerations #9)

A name collision re-prompts for a different name; any other AWS error
propagates normally.

### 15. Import Key Pair

Interactive workflow, for operators who already have a personal or team
public key they want registered instead of generating a new one:
1. Prompt for a name (must be unique within its region)
2. Prompt for a local public key file path (`.pub`)
3. Prompt for region
4. Read and validate the file is a well-formed public key before calling
   AWS — fail locally with a clear message rather than surfacing AWS's
   raw `InvalidKeyPair.Format` error
5. Call `ec2:ImportKeyPair`
6. Confirm success

Unlike Create Key Pair, there is no private key material to save —
`ec2:ImportKeyPair` never returns one, since AWS never sees the private
half. This is the reverse direction from Create Key Pair: Create is for
when AWS generates the pair and hands you the private `.pem` half; Import
is for when you already generated a keypair yourself (e.g. via
`ssh-keygen`) and want AWS to trust its public half. A `.pem` file from
this tool's own Create Key Pair is a *private* key and will always be
rejected here — the prompt hints at deriving a `.pub` file from one with
`ssh-keygen -y -f <private-key> > file.pub` if that's the operator's
actual starting point.

### 16. Delete Key Pair

Safety-tier workflow, one notch below Terminate/Remove AMI's dry-run +
type-to-confirm tier (deleting a key pair doesn't destroy running
infrastructure the way terminating an instance does, but it does
permanently remove AWS's copy, so a plain yes/no is too casual):
1. Pick a key pair from the list
2. **Show dependent instances**: list any running/stopped instances
   launched with this key pair (`ec2:DescribeInstances` filtered by
   `key-name`) — deleting the AWS-side key pair doesn't affect those
   instances' ability to keep running, but it does mean this key pair can
   no longer be used to launch *new* ones, worth surfacing first
3. **Type to confirm**: operator types the key pair name exactly
4. Call `ec2:DeleteKeyPair`
5. Confirm deletion; remind the operator the local `~/.ssh/<name>.pem`
   file (if one exists, from Feature 14) is untouched — this tool never
   deletes local files, only the AWS-side registration

### S3 Domain (Buckets & Static Websites)

Two use cases live in this domain: browsing/managing buckets generally,
and the specific workflow of standing up a static website backed by S3 +
CloudFront. Per `DECISIONS.md`'s "CloudFront + OAC by default for static
websites", the website workflow defaults to CloudFront + Origin Access
Control (bucket stays private; CloudFront is the only reader) rather than
a public-read bucket policy — see Security Considerations below.

### 17. List Buckets

Resource listing shown when the S3 domain is entered: `s3:ListBuckets`,
then for each bucket a lightweight `s3:GetBucketLocation` (region),
best-effort `s3:GetBucketWebsite` (a `NoSuchWebsiteConfiguration` error
just means "not configured," not a failure) to show whether static
website hosting is enabled, and best-effort `s3:GetBucketTagging` (a
`NoSuchTagSet` error, or simply no `Purpose` key present, means
"untagged" — not a failure) to read the bucket's `Purpose` tag (Feature
18) for Feature 21.1's use — in one table (Name, Region, Static Website,
Purpose; untagged shows blank, matching this tool's existing "blank
means untagged" convention for Name).

**Revised by "S3 Resource List Display — Paged, Accessible-Compatible"
below (designed 2026-07-10, not yet implemented):** the data fetch
described above is unchanged, but the table is no longer printed
automatically on every S3 menu redisplay — only when "Show resource
lists" is explicitly chosen — and printing itself becomes paged.

### 18. Create Bucket

Interactive workflow:
1. Prompt for a bucket name (globally unique; validate against S3's
   naming rules locally before calling AWS)
2. Prompt for region
3. Prompt for bucket purpose — a numbered pick list: Website, Backup,
   Internal — see "Bucket Purpose Tagging Convention" below
4. Call `s3:CreateBucket`
5. Block public access by default (`s3:PutPublicAccessBlock`, all four
   settings on) — an operator who genuinely wants a public bucket must
   say so explicitly in Feature 19, not get it by omission here
6. Tag the bucket `Purpose: website|backup|internal`
   (`s3:PutBucketTagging`) so Feature 21.1 (Manage Bucket Lifecycle
   Policies) can recall this choice later without re-asking
7. Confirm creation

#### Bucket Purpose Tagging Convention

Not an AWS-enforced concept — a `Purpose` tag this tool applies at
creation (steps 3/6 above) and reads later (Feature 21.1) to decide
which lifecycle-policy UX to offer: `backup` gets a simplified guided
flow for the two policy shapes this team actually uses repeatedly
(expire after N days; transition to cheaper storage after N days);
`website` and `internal`, or a bucket with no `Purpose` tag at all
(e.g. one created outside awsops), get a fuller generic lifecycle rule
editor. Distinct from Feature 10's unrelated `Purpose=cloud-init-
extraction` tag on temporary EC2 instances — same tag key name, entirely
different resource type and meaning, no relationship between the two.

### 19. Configure Static Website Hosting

Interactive workflow for turning an existing bucket into a website
origin:
1. Pick a bucket
2. Prompt for index document (default `index.html`) and error document
   (default `error.html`)
3. Call `s3:PutBucketWebsite`
4. **Access pattern**: default and recommended path is CloudFront +
   Origin Access Control — this step only configures the website
   document settings on the bucket itself; the bucket's
   public-access-block settings from Feature 18 are left untouched
   (still blocking public access). **Phase 20 implements only this
   default path** — the explicit public-read-bucket-policy opt-out
   mentioned below is deferred until there's an actual need for it (see
   DECISIONS.md, "Defer the public-read bucket policy opt-out in
   Configure Static Website Hosting"). A future opt-out path would
   require its own explicit confirmation warning that the bucket
   contents become world-readable directly, independent of CloudFront.
5. Confirm. Feature 24 (Create Distribution, CloudFront domain) doesn't
   exist yet as of Phase 20 — until it does, print that CloudFront
   support isn't implemented yet instead of literally offering to hand
   off to it.

### 20. Sync Local Directory to Bucket

Interactive workflow for publishing a built static site:
1. Pick a bucket
2. Prompt for a local directory path
3. **Dry-run diff**: compare local files against the bucket's current
   objects (by key and size, not a full checksum, to keep this fast) and
   show what would be uploaded (new/changed) and, separately, what
   exists in the bucket but not locally (deletion candidates — never
   deleted silently)
4. Confirm before uploading
5. Upload new/changed files (`s3:PutObject`, content-type inferred from
   extension)
6. If step 3 found bucket-only objects, a **separate** confirm-and-delete
   step (`s3:DeleteObject`) — never bundled into the same confirmation as
   the upload, so "yes" to publishing new content can never accidentally
   also mean "yes" to deleting something
7. Report a summary: files uploaded, files deleted, bytes transferred

**Superseded, implemented 2026-07-09 by Feature 21.2's interactive file
manager** (PLAN.md Phase 20.1) — see "S3 Object Management — Interactive
File Manager" below. This directory-to-bucket workflow remains a
first-class, directly reachable capability (not folded away as a
generic case of something else): it's now the file manager's dedicated
Sync action (`S` / `:sync`, 21.6), not the standalone wizard described
above — that wizard (`bucket_sync.go`) is retired; its diff/walk/list
logic moved to `internal/s3diff`, reused rather than reimplemented.

### 21. Browse/Manage Objects

Interactive workflow for ad-hoc bucket inspection outside the sync flow:
1. Pick a bucket
2. Prompt for an optional key prefix filter (blank lists everything) --
   added beyond the original spec once this team's actual bucket usage
   made it clear a single bucket (e.g. `sql-backups.library.caltech.edu`)
   can hold many objects across many per-instance prefixes (see
   DECISIONS.md, "Add an optional key-prefix filter to Browse/Manage
   Objects")
3. List objects (`s3:ListObjectsV2` scoped to that prefix, paginated the
   same way Feature 1's PickList pagination already handles >50 items)
4. Choose an object; offer to show metadata (size, last-modified,
   content-type) or delete it
5. Deletion is a plain yes/no per-object confirm — Feature 20's bulk sync
   deletion gets the stronger "separate confirm" treatment because it can
   affect many files at once; a single ad-hoc delete here is
   lower blast-radius

**Superseded, implemented 2026-07-09 by Feature 21.2's interactive file
manager** (PLAN.md Phase 20.1) — see "S3 Object Management — Interactive
File Manager" below. Single-object browsing, filtering, metadata, and
delete are folded into the new screen's single-pane mode rather than
kept as a second, parallel implementation of "filter, pick, act." The
standalone wizard (`bucket_browse.go`'s `BrowseBucketObjects`) is
retired; only its `listBucketObjectsWithPrefix` helper remains, still
used by Delete Bucket's empty-bucket check.

### 21.1. Manage Bucket Lifecycle Policies

Interactive workflow for reviewing, setting, updating, and removing S3
Lifecycle Configuration rules — covers both "transition to cheaper
storage after N days" and "delete (expire) after N days" policies (see
DECISIONS.md, "Add Manage Bucket Lifecycle Policies"). Numbered 21.1
(inserted after Feature 21 without renumbering CloudFront's Features
22-26 — the same decimal-insertion convention PLAN.md already uses for
Phases, e.g. 15.1-15.26).

The underlying AWS API (`s3:GetBucketLifecycleConfiguration` /
`s3:PutBucketLifecycleConfiguration`) only supports replacing a bucket's
entire rule set atomically — there is no per-rule add/edit/delete call.
This feature presents CRUD over that atomic-replace API: fetch all
existing rules, let the operator pick one to edit or remove, or add a
new one, then write the complete modified rule set back in one call.

1. Pick a bucket (from the already-fetched bucket listing, Feature 17)
2. `s3:GetBucketLifecycleConfiguration` (a `NoSuchLifecycleConfiguration`
   error means "no rules yet," not a failure) and display existing rules
   (ID, prefix scope or "whole bucket", transition(s), expiration)
3. Branch on the bucket's `Purpose` tag (Feature 17/18):

   **`backup` — guided flow:**
   - Add a new policy: prompt "Expire objects after how many days?
     (blank to skip)" and "Transition to cheaper storage after how many
     days? (blank to skip)", plus a storage class pick list when a
     transition day count is given — a curated subset (Standard-IA,
     Intelligent-Tiering, Glacier Flexible Retrieval, Glacier Deep
     Archive; see DECISIONS.md for why this subset), not the full AWS
     storage-class enum
   - Prompt for an optional key prefix (blank = whole bucket), same
     convention as Feature 21's browse filter
   - Existing rules can be edited (re-prompt the same questions,
     defaulted to current values) or removed (plain yes/no confirm)

   **`internal`, `website`, or an untagged bucket — generic editor:**
   - Add a new rule: prompt for a rule ID (must be unique among the
     bucket's existing rules), an optional key prefix, zero or more
     transitions (each: days + storage class from the *full* AWS
     storage-class enum, repeat until the operator stops adding), and an
     optional expiration (days)
   - Edit an existing rule: pick by ID, re-prompt all fields defaulted to
     current values
   - Remove a rule: pick by ID, plain yes/no confirm
4. Whichever path was used, write the complete modified rule set via
   `s3:PutBucketLifecycleConfiguration` and confirm success

### S3 Resource List Display — Paged, Accessible-Compatible (Design Addendum, 2026-07-10)

**Superseded 2026-07-10, same day, by "Terminal UI Architecture: Menus,
Actions, Lists, and Managers" below.** `internal/ui.PagedTable`/
`DisplayBuckets` (implemented per this addendum, then used for less than
a day) are retired, not extended: screen-reader/accessible-mode
compatibility -- this addendum's central design constraint -- turned out
not to be an actual requirement for this tool once discussed directly
(DECISIONS.md, "Deprecate termlib; standardize on huh/bubbletea before
0.0.2"), which removes the reason this had to be plain sequential
termlib printing instead of a real bordered, chrome-consistent
`bubbletea` component like the rest of the app. Left in place below as
the accurate record of what was designed, decided, and briefly shipped,
and why it changed -- not deleted.

**Status (as originally written): designed, not yet implemented.**
Revises Feature 17 (List Buckets) and the "Show resource lists" menu
item's behavior within the S3 domain menu (21.2). Scoped to S3 for now —
Compute and Key Management's own "Show resource lists" listings
(Features 1, 13; `ui.DisplayInstances`/`DisplayImages`/
`DisplayKeyPairs`) keep their current one-shot, unpaginated printing;
ask before extending this to them. The pager itself, however, is
deliberately built as a **generic, reusable `internal/ui` component**,
not a bucket-specific one — S3 buckets are its first consumer, so
Compute/Key Management can adopt the same mechanism later, when/if
their own menus migrate, without a redesign (see "Reusability" below).

**Problem.** Today, `RunS3Menu` calls `actions.Refresh(ctx)` after every
successful action (Create Bucket, Configure Website, etc.), and
`refreshS3` (`cmd/clasm/main.go`) both re-fetches bucket data AND prints
the full bucket table (`ui.DisplayBuckets`) in the same step. Two
problems fall out of that: the full table reprints after every action,
not just when the operator asks to see it, cluttering the menu's
redisplay; and `DisplayBuckets` has no pagination at all (unlike
`ui.PickList`'s existing 50-item paging), so a large bucket count would
print unboundedly.

**Decision.**
1. Split "refresh" into its two separate concerns: re-fetching bucket
   data (still happens after every action, unchanged, so bucket-
   selection prompts elsewhere stay current) and *displaying* the
   bucket table (now happens only when "Show resource lists" is
   explicitly chosen from the S3 menu).
2. "Show resource lists" becomes its own paged display, not an inline
   print: the banner (folding in the page count) and column header
   repeat on every page, and the operator navigates with three
   single-key commands — `n` (next page), `p` (previous page), `q`
   (quit — returns to the S3 menu without printing anything further).
   `n`/`p` are no-ops at the first/last page, matching `PickList`'s
   existing boundary behavior.
3. Mockup (approved 2026-07-10):

   ```
   ===== S3 BUCKETS (page 2 of 3, showing 21-40 of 47) =====

   NAME                                     REGION     STATIC WEBSITE PURPOSE
   caltech-static-assets                    us-west-1  yes            web-hosting
   research-data-archive-2024               us-west-2  no             backup
   thesis-submissions-fall2025              us-west-2  yes            production
   ...                                       ...        ...            ...
   cl-cold-storage-2023                     us-west-1  no             backup

   'n' next page   'p' previous page   'q' quit to S3 menu
   Command: 
   ```

4. **Reusability.** The pager is a new generic function in
   `internal/ui` (working name `PagedTable`), decoupled from any
   specific resource type: it takes a title-format callback (given page/
   totalPages/shown/total, returns the banner line), an already-
   rendered header line, and already-rendered row strings — it owns
   only windowing, chrome, and `n`/`p`/`q` input, not column formatting.
   `DisplayBuckets`'s existing `PadRight`/`Truncate` column rendering is
   reused as-is to build the header/row strings passed in; only the
   printing loop changes. This mirrors `PickList`'s own shape (a
   generic mechanism domain code plugs labels into), so Compute/Key
   Management's `DisplayInstances`/`DisplayImages`/`DisplayKeyPairs`
   could later call the same `PagedTable` with their own header/row
   strings, if and when those menus are revisited — not part of this
   piece of work, but not precluded by it either.
5. Stays fully accessible: this is sequential printing throughout —
   print the banner+header+page of rows, print the command line, read
   one line of input, then either print the next page's block or
   return — no cursor repositioning or redraw at any point, so it
   behaves identically over a real TTY, a `TERM=dumb` session, or (per
   DECISIONS.md, "huh fields are pipe-testable...") a piped
   input/output pair in tests. Note this mechanism doesn't involve
   `huh` at all — it's plain `termlib` printing and `LineEditor.Prompt`
   reading, the same style `PickList` already uses; "migrating to huh"
   elsewhere in this codebase and "paging a resource list" are
   orthogonal, and this design keeps them that way.

**Rejected alternatives.**
- *Print the whole table unpaginated, rely on terminal scrollback* —
  today's actual behavior; rejected per this session's request (buckets
  can exceed a screen's height, and the table shouldn't reprint after
  unrelated actions).
- *Give the paged display a `huh.Select`/`bubbletea`-style bounded
  viewport (like the interactive file manager's panes)* — rejected:
  that's a redraw-in-place rendering model, structurally the thing
  accessible mode has to avoid; would need a parallel accessible-mode
  fallback for no benefit over plain sequential paging, which already
  works today via `PickList`.
- *Reuse `ui.PickList` directly instead of a new `PagedTable`* —
  `PickList` is shaped around choosing ONE item from a single-column
  label list (it returns a selection). This display shows a
  multi-column table and makes no selection (`q` just returns) — close
  in spirit (same page-size/boundary conventions) but a distinct
  function.

**Consequences.** `ui.DisplayBuckets` is replaced by a `PagedTable`
call site; `cmd/clasm/main.go`'s `refreshS3` closure splits into a
silent data-refresh half (still called after every action) and a
separate paged-display call reachable only from the "Show resource
lists" menu item; `s3MenuItems`' "Show resource lists" entry
(`s3_menu.go`) calls the new paged display instead of `a.Refresh(ctx)`
alone.

---

### S3 Object Management — Interactive File Manager (Design Addendum, 2026-07-09)

**Status: implemented 2026-07-09 (`internal/filemanager`; PLAN.md Phase
20.1) — unit-tested, not yet real-AWS verified (PLAN.md Phase 22).**
This addendum was design-only when first written; the section below is
otherwise left as originally drafted (it's the accurate design record),
except where marked. It supersedes Feature 20 (Sync Local Directory to
Bucket) and Feature 21 (Browse/Manage Objects) as S3 menu entry points —
both wizards are now retired. One addition beyond this addendum's
original scope: a dedicated Sync action (21.6) was added during
implementation so Decision 2 below ("Sync's directory-mirroring
workflow is kept as a first-class, directly reachable capability") is
met literally, not just approximated by manual tag-and-act; see
DECISIONS.md, "Add a dedicated Sync action to the file manager."

Builds on the huh-vs-bubbletea technology evaluation already recorded
(`continue_next_time.txt`; `agents/hand-off/
2026-07-09T220000Z-clasm-rename.spmd`): huh was the leading candidate
for replacing termlib's blocking-prompt style generally, evaluated by
pulling real source into a scratch module rather than trusting docs.
This addendum goes one step further for S3 object management
specifically — huh's blocking forms are sufficient for single-pane
browsing and batch selection, but the linked local+bucket workflow
below (21.3, 21.5, 21.6) needs a live, simultaneously-visible two-pane
view that huh's sequential fields structurally can't provide. That one
piece is designed as a scoped `bubbletea` component instead — see 21.8
for why that doesn't reopen the original "don't rewrite everything"
objection to bubbletea.

#### 21.2. Revised S3 Domain Menu

- Show resource lists
- Create Bucket (Feature 18, unchanged)
- Configure Static Website Hosting (Feature 19, unchanged)
- **Browse & Manage Objects** — opens the interactive file manager
  (21.3-21.8) described below, single-pane by default
- Manage Bucket Lifecycle Policies (Feature 21.1, unchanged)
- Delete Bucket (unchanged)
- Back to domain picker

"Sync Local Directory to Bucket" and the bulk delete-by-prefix case are
removed as separate menu entries — both become reachable from inside
the interactive file manager (directory-mirroring via double-pane mode,
21.3; bulk delete via tagging filtered matches in either mode, 21.6).
Feature 21's original single-object browse/metadata/delete is folded in
the same way rather than kept as a second, parallel implementation:
tagging exactly one item and choosing an action in single-pane mode
covers that case without a separate wizard.

#### 21.3. Session Start & Linking

Entering "Browse & Manage Objects":
1. Pick a bucket and region (`huh.Select`, reusing Feature 17's already-
   fetched listing) — this pre-flight step stays on huh; there's no
   reason to rebuild bucket selection inside the interactive screen.
2. Prompt (`huh.Confirm`): link a local directory now? If yes, prompt a
   path (`huh.Input`, reusing `bucket_sync.go`'s existing
   `validateLocalDirectory`) and open in double-pane mode; if no, open
   single-pane (bucket only).
3. Mid-session, the `l` hotkey links or unlinks a local directory
   without restarting. When nothing is linked, it prompts for a path via
   the command line and splits single-pane into double-pane. When a
   directory **is** linked, `l` (or `:unlink`) goes straight to a direct
   Confirm ("Unlink `<path>` and return to single-pane view?") instead
   of the command line — added 2026-07-09 after an operator asked for
   "a way to go from two panels back to displaying only the S3 bucket";
   the original design (clear the pre-filled `:link <path>` field and
   submit it empty) was technically reachable but not discoverable as
   *the* way back. This directly serves "moving between local and
   bucket as one set of activities" without requiring the operator to
   plan ahead at launch.

#### 21.4. Screen Layout & Chrome

```
┌ clasm — S3 File Manager — sql-backups.library.caltech.edu (us-west-2) ─────────────────┐
├───────────────────────────────┬─────────────────────────────────────────────────────────┤
│  LOCAL: /path/on/disk          │  S3: bucket-name/prefix/                                │
│  ...listing...                 │  ...listing...                                          │
├───────────────────────────────┴─────────────────────────────────────────────────────────┤
│ 12 items, 3 tagged (4.3 MB)              filter: db0*                                    │
├───────────────────────────────────────────────────────────────────────────────────────────┤
│ : ____________________________________________________________________________________  │
├───────────────────────────────────────────────────────────────────────────────────────────┤
│ u Upload  d Download  x Delete  f Filter  F Find  S Sync  l Link  Tab Switch  Space Tag  q Quit │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

Top to bottom:
- **Header** — mode indicator, bucket name + region, local root once
  linked.
- **Pane area** — one pane (single-pane mode) or two side by side
  (double-pane): local on the left, S3 on the right, matching the
  WinSCP/SFTP-client convention this team is more likely to already
  know than Midnight Commander's layout. A pane's own header row shows
  an animated spinner + "Loading..." while its listing is being
  (re)fetched, and Find's status row shows the same spinner while a
  search is still running (added 2026-07-09 -- both can take a real,
  noticeable amount of time against a large bucket, and with no
  feedback the screen just looked frozen/broken).
- **Status line** — per pane: item count, tagged count, aggregate
  tagged size (needed to see the blast radius of a bulk action before
  confirming, not just a count), active filter string.
- **Command line** — inert until `:` or `/` takes focus; typed verbs
  (`:upload`, `:delete`, `:find <pattern>`) or filter patterns.
- **Hotkey legend** — single-letter mnemonics, not function keys — F-key
  mappings are unreliable across terminal emulators and multiplexers, a
  real enough problem to design around rather than default to. The
  legend is contextual (e.g. `x Delete` only shown once something is
  tagged). The hotkey bar and the colon command line both drive the
  same underlying action dispatch; neither is a fallback for the
  other — two paths to the same commands, not a primary and a backup.
- **Progress/confirm overlay** — modal, centered over the pane area.
  Confirms reuse the existing `Confirm`/`ConfirmDestructive` split
  unchanged (plain yes/no for Upload/Download, type-the-name-back for
  Delete — Security Consideration #11 still applies without exception).
  Execution reuses the existing per-item OK/FAIL progress convention as
  scrolling lines inside the overlay; completion requires an explicit
  "press any key to continue" rather than an auto-dismiss timer, so a
  FAIL line can never be hidden by a timeout.

#### 21.5. Pane Navigation & Listing

- Panes navigate **independently** — each side browses to any
  folder/prefix on its own; there is no requirement that both point at
  corresponding paths. This matches the tag-in-focused-pane,
  act-on-other-pane convention directly (21.6) and stays more flexible
  (e.g. uploading from one local folder into an unrelated bucket
  prefix) than an always-synced, always-diffing view would allow.
- Rows sort folders-then-files, alphabetical within each group.
- **S3 listing uses `s3:ListObjectsV2` with `Delimiter=/`, one directory
  level per call** (`CommonPrefixes` for folders, `Contents` for files)
  rather than a full-prefix listing grouped client-side. Per-level
  browsing stays cheap regardless of how deep or large the tree is
  below the current level.
- `f` (or `/` on the command line) filters the focused pane's
  **current level** by substring match against already-fetched rows —
  cheap, since delimiter-based listing means "current level" is never
  the whole bucket, and instant/synchronous (no spinner needed the way
  Find's recursive scan needs one, 21.4). A filter starting with `/` is
  matched via the same anchored form Find uses (21.7) instead: an
  exact/glob match of the current level's basenames rather than a
  substring, e.g. `/index.html` matches only a file named exactly that,
  not `myindex.html5` too -- added (2026-07-09) so the `/`-prefix
  convention means the same thing whether typed as a filter or a Find
  pattern.
- The local pane lists one directory level at a time (`os.ReadDir`), not
  the full recursive walk `bucket_sync.go` uses for diffing — that
  recursive traversal is reused specifically by Find (21.7) and by the
  double-pane linked workflow's own diff step, not by ordinary browsing.

#### 21.6. Tagging & Actions

- `Space` tags/untags the row under the cursor; `*` tags every row
  currently visible (post-filter) in the focused pane. Pattern-based
  tag/untag (mc's `+`/`-`) is deliberately left out of this design —
  `*` after filtering already covers "tag everything matching X."
- Action keys operate on the focused pane's tagged set (or the row
  under the cursor if nothing is tagged):
  - `u` **Upload** (Create/Update) — only available when a local
    directory is linked; tagged local files are `s3:PutObject`'d into
    the bucket pane's *current* folder (mc/WinSCP convention: source is
    the focused/tagged pane, destination is the other pane's current
    position).
  - `d` **Download** (Read) — tagged bucket objects (`s3:GetObject`)
    land in the local pane's current directory.
  - `x` **Delete** — tagged bucket objects removed (`s3:DeleteObject`).
    This is how both Feature 21's old single-object delete and the old
    bulk delete-by-prefix case are covered by one path now.
  - `m` **Show metadata** — carried forward from Feature 21's original
    per-object metadata display (`s3:HeadObject`); applies to the row
    under the cursor.
  - `S` **Sync** (added during implementation, not in this addendum's
    original scope — see DECISIONS.md, "Add a dedicated Sync action to
    the file manager") — only available when a local directory is
    linked. Diffs the *entire* linked directory against the *entire*
    bucket by key+size (`internal/s3diff.Compute`, the same logic
    Feature 20's retired wizard used, not reimplemented) — not scoped to
    either pane's current navigated position, matching the original
    wizard's whole-tree semantics. Upload candidates confirm first
    (plain `Confirm`); only once that stage is accepted or skipped (no
    upload candidates) does the delete stage appear
    (`ConfirmDestructive`) — the two are never bundled into one prompt
    (Security Consideration #11), and declining the upload stage aborts
    before the delete stage is ever shown.
- Confirmation: plain `Confirm` before Upload/Download,
  `ConfirmDestructive` (type the bucket name, or the active prefix)
  before Delete — unchanged from Feature 20/21's existing security
  posture, just reachable from one screen instead of three separate
  workflows.
- Open implementation-time question, not resolved by this design pass:
  whether to batch deletes via `s3:DeleteObjects` (up to 1000 keys per
  call) instead of porting the current one-`DeleteObject`-call-per-key
  loop — logged previously as a "nice to have" (`continue_next_time.txt`)
  and worth folding in since the delete path is being rebuilt anyway.

#### 21.7. Find (recursive pattern search)

- Hotkey `F` or command-line `:find <pattern>` — distinct from `f`'s
  current-level-only filter.
- Pattern is a **shell glob matched against each entry's basename**,
  evaluated recursively at every depth below the focused pane's current
  position (Go stdlib `path/filepath.Match` semantics, including
  backslash-escaping) — the same behavior as `find <dir> -name
  '<pattern>'`. `*.go` and `\.git` both work as plain globs; neither
  needs a regex engine. Recursion starts at the focused pane's current
  directory, not always the tree root, matching Unix `find`'s own
  convention and avoiding an unbounded scan when the operator only meant
  to search what they're currently looking at.
- A pattern starting with `/` is **anchored** to the search's starting
  point instead of matched against the basename alone: the leading `/`
  is stripped and the remainder is matched (still via `filepath.Match`,
  so `*` still won't cross a `/`) against each entry's full path
  relative to that starting point. `/index.html` therefore matches only
  a root-level `index.html`, not `sub/index.html` — added (2026-07-09)
  because basename-only matching couldn't express "just the one at the
  root" when the same filename legitimately exists at other depths too
  (a common case: every static site under a bucket has its own
  `index.html`).
- Matches both files and directories/pseudo-folders.
- Results **replace the focused pane's listing** with a flat list, each
  row showing the path relative to the search's starting point (not
  just the basename, since matches can span multiple subdirectories).
  Normal tagging (`Space`, `*`) works directly on results, so matches
  can be acted on immediately without a detour back through normal
  browsing.
- `Enter` on a result jumps back to normal hierarchical browsing at that
  match's parent directory, cursor on it; `Esc` discards the find view
  and returns to normal browsing at the prior location.
- On the S3 side this means an on-demand, full recursive
  `ListObjectsV2` (no `Delimiter`) under the current prefix — the same
  listing cost Feature 20 (Sync) and the old delete-by-prefix case
  already paid when invoked, just user-triggered from inside the
  browser now rather than automatic. Shows a live "Searching… (N
  scanned, M matched)" status; cancellable (`Esc`/`Ctrl-C`) since a deep
  prefix on a large bucket could take a while.
- The local side reuses `bucket_sync.go`'s existing
  `filepath.WalkDir`-based `walkLocalTree` traversal, with the glob
  match layered on top — no new local-filesystem traversal code needed.

#### 21.8. Technology & Architecture Notes

- **`bubbletea`, scoped to this one screen** — not the full-application
  rewrite the original evaluation ruled out. That evaluation's
  objection was the cost of rewriting all ~40 `internal/workflow`
  wizards' blocking, linear control flow into Elm-architecture state
  machines; it doesn't apply to building one bounded, genuinely
  interactive component while every other wizard (Create Bucket,
  Configure Website, Lifecycle Policies, Delete Bucket, and this
  screen's own bucket-selection pre-flight, 21.3) stays on huh's
  blocking synchronous fields.
- No new dependency weight beyond adopting huh at all: huh already
  pulls in `bubbletea`, `bubbles`, and `lipgloss` transitively, so a
  scoped `bubbletea.Program` for this screen doesn't add anything huh
  wasn't already going to bring in.
- This is the one place in the S3 domain design where a live, stateful,
  two-pane view is unavoidable — every other S3 feature (17-19, 21.1)
  stays exactly as designed, huh fields only.
- Prototype this screen's single-pane mode first (the simpler of the
  two) before committing to double-pane/link/Find — same "prototype
  cheaply before porting everything" guidance already recorded from the
  original huh evaluation, now applied one level deeper.

### CloudFront Domain

**Someday/maybe — not on the active roadmap, no committed timeline**
(revised 2026-07-09 from "postponed to a later version"; see
`DECISIONS.md`, "Demote CloudFront to someday/maybe..."). No code
written. The design below stays valid reference for if this is ever
picked back up.

CloudFront's control plane is a single global API (`us-east-1`,
regardless of where origins live) — this domain's listing is not
region-fanned-out the way Compute/Key Management/S3 are (see "Navigation:
Domain Picker" above).

### 22. List Distributions

Resource listing shown when the CloudFront domain is entered:
`cloudfront:ListDistributions`, showing ID, Domain Name, Origin, and
Status (`Deployed`/`InProgress`) in one table.

### 23. Show Distribution Detail

Interactive workflow: pick a distribution, call
`cloudfront:GetDistribution`, display its full origin/behavior/cache
config and current status — read-only, no confirmation needed.

### 24. Create Distribution

Interactive workflow, the CloudFront half of standing up a static website
(paired with Feature 19):
1. Pick (or create, handing off to Feature 18) the S3 bucket to serve
2. Create an Origin Access Control for this distribution
   (`cloudfront:CreateOriginAccessControl`) if one doesn't already exist
   for this bucket
3. Prompt for default root object (default `index.html`, matching
   Feature 19's index document if already configured)
4. Prompt for optional alternate domain name(s) (CNAMEs) — if provided,
   note plainly that an ACM certificate covering that name must already
   exist in `us-east-1` (certificate provisioning itself is out of scope
   — see "Deferred to a Later Version")
5. Confirm before creating (this provisions real, billable
   infrastructure, though CloudFront's free tier makes this low-stakes
   compared to Compute's destructive operations — a plain confirm, not a
   type-to-confirm tier)
6. Call `cloudfront:CreateDistribution`
7. **Update the bucket policy** to allow only this distribution's OAC to
   read it (`s3:PutBucketPolicy`, scoped by `AWS:SourceArn` to this
   distribution) — this is what makes the private-bucket-plus-OAC pattern
   actually work; without it the distribution returns `AccessDenied` for
   every request
8. Poll (unbounded — distribution deployment commonly takes 5–15 minutes)
   until `Deployed`, displaying elapsed time, the same pattern as
   Feature 8's AMI-creation poll
9. Display the distribution's domain name

### 25. Invalidate Cache Paths

Interactive workflow for forcing CloudFront to re-fetch updated content
after a Feature 20 sync (CloudFront otherwise serves cached content per
each object's `Cache-Control`/default TTL):
1. Pick a distribution
2. Prompt for path pattern(s) to invalidate (default `/*` — everything —
   with a note that wildcard invalidations are simple but less precise
   than targeted paths)
3. Confirm (invalidations beyond the first 1,000 paths/month are
   billable — worth a brief on-screen note, not a blocking warning)
4. Call `cloudfront:CreateInvalidation`
5. Poll until `Completed`, displaying elapsed time
6. Confirm completion

### 26. Project/Environment Tagging Convention (extended)

Feature 12's tagging convention (`Project`/`Environment` tags, defaults
suggested at creation, explicit prompt for `Environment` with no default)
extends to the new domains where the underlying AWS resource supports
tags: S3 buckets (Feature 18) and CloudFront distributions (Feature 24).
Key pairs also support tags but carry comparatively little operational
risk on their own, so tagging them is offered but not required the way it
effectively is for Compute's destructive-operation gating. The
`Environment=production` safety-gate behavior itself (the extra warning
before type-to-confirm) is **not** extended to Delete Key Pair or any
S3/CloudFront deletion in this round — see "Deferred to a Later Version".

## Architecture

```
cmd/awsops/
    main.go              ← Entry point; wires config, AWS clients, and the
                            interactive menu loop together

internal/awsclient/       ← Thin, typed wrapper over aws-sdk-go-v2
    ec2.go                 - per-region EC2 client construction (also backs
                              Key Management: DescribeKeyPairs/CreateKeyPair/
                              ImportKeyPair/DeleteKeyPair share this client)
    ssm.go                 - per-region SSM client construction (fstrim,
                              cloud-init AMI extraction, backup archive)
    s3.go                   - S3 client construction; broadened beyond
                              Feature 11's HeadObject-only use to cover the
                              S3 domain (CreateBucket, PutPublicAccessBlock,
                              PutBucketWebsite, PutBucketPolicy, PutObject,
                              ListObjectsV2, DeleteObject)
    cloudfront.go            - CloudFront client construction (single
                              `us-east-1` control-plane endpoint — no
                              per-region fan-out, unlike the other clients)
    iam.go                   - IAM client construction (single client,
                              global service like STS/CloudFront) --
                              ListInstanceProfiles/ListRoles/
                              CreateInstanceProfile/AddRoleToInstanceProfile
                              for Feature 2/3's instance profile pick-or-create
    regions.go              - the configured regions (currently us-west-1, us-west-2)

internal/inventory/       ← Resource listing/aggregation
    instances.go            - ListInstances(ctx) across all regions
    images.go               - ListImages(ctx) (owned AMIs) across all regions
    keypairs.go              - ListKeyPairs(ctx) across all regions
    buckets.go               - ListBuckets(ctx) with per-bucket region +
                              static-website-hosting status
    distributions.go         - ListDistributions(ctx) (global, not
                              region-fanned-out)

internal/ui/               ← Terminal interaction (replaces show_pick_list,
    picklist.go               display_instances/display_amis, prompts) --
    display.go                stays generic/parameterized; PickList[T]
    prompt.go                 needed no changes for the domain picker below

internal/workflow/         ← One file per operation (replaces the Bash
    launch_instance.go        "_workflow" functions; also backs Feature 3
                              (Create from Cloud-Init YAML) via the same
                              launch/poll/cloud-init-check execution path
    domain_menu.go           - the top-level domain picker (RunDomainPicker,
                              DomainActions) + the "Back to domain picker"
                              vs. "genuine exit signal" distinction every
                              domain's own menu loop reports through --
                              lives here, not internal/ui, so it can share
                              menu.go's dispatch-error sentinels
    create_instance_profile.go - IAM instance profile pick-or-create
                              (Feature 2/3): promptIAMInstanceProfileOrCreate,
                              createInstanceProfileFromRole
    power_state.go           - Start/Stop/Terminate EC2 Instance
    manage_tags.go           - Manage Tags: add/update/remove, instance or AMI
    create_ami.go
    remove_ami.go
    cloud_init.go            - Show/Export Cloud-Init (instance + AMI paths)
    backup_archive.go        - Backup Archive & Trim (upload, verify, delete, fstrim)
    keypair_create.go         - Create/Import/Delete Key Pair (Features 14-16)
    keypair_import.go
    keypair_delete.go
    bucket_create.go          - Create Bucket, Configure Static Website
    bucket_website.go           Hosting (Features 18-19)
    bucket_sync.go             - Sync Local Directory to Bucket (Feature 20)
    bucket_browse.go           - Browse/Manage Objects (Feature 21)
    distribution_create.go     - Create Distribution, Invalidate Cache Paths
    distribution_invalidate.go   (Features 24-25)
    menu.go                   - reworked to drive the domain picker and
                              delegate to each domain's menu loop
```

Each `internal/workflow` file depends on `internal/awsclient` and
`internal/inventory` through small interfaces (e.g. an `EC2API` interface
covering just the SDK methods actually used, and an `S3API` interface
covering just `HeadObject` for Feature 11's independent verification), so
tests can supply fakes without hitting real AWS or shelling out to a mock
CLI binary.

## Data Flow

```
User Interaction
     │
     ▼
Menu Selection (1-8)
     │
     ▼
┌─────────────────────────────────────┐
│  For each operation:                │
│  1. Fetch current resource data     │ ← ec2.DescribeInstances/DescribeImages
│                                        (typed SDK calls, one per region)
│  2. Filter/sort for display         │ ← Owned AMIs only, aggregate regions
│  3. Present pick list to user       │ ← Numbered menu with formatting
│  4. Collect additional parameters   │ ← Interactive prompts with validation
│  5. Perform AWS API call            │ ← ec2.RunInstances/CreateImage/
│                                        DeregisterImage (typed SDK calls)
│  6. Display results                 │ ← Success/failure with details
│  7. Refresh displays                │ ← Return to main menu with updated data
└─────────────────────────────────────┘
```

## File Structure

```
awstools/
├── DESIGN.md              ← This document
├── DECISIONS.md           ← Architecture and UX decisions (Bash + Go history)
├── PLAN.md                ← Implementation plan (Go)
├── TEST_PLAN_REAL_AWS.txt ← Manual verification steps against real AWS
├── go.mod / go.sum
├── cmd/awsops/            ← Go entry point (see Architecture above)
├── internal/              ← Go packages (see Architecture above)
├── ec2_ami_manager.bash   ← Reference only; retire once Go reaches parity
│                             and passes TEST_PLAN_REAL_AWS.txt
├── ami_copy.bash          ← Reference only; superseded by ported
│                             capabilities (see DECISIONS.md, 2026-06-30)
└── tests/                 ← Bash/BATS tests for ec2_ami_manager.bash
                              (kept until the Bash tool is retired; Go tests
                              live alongside their packages as *_test.go)
```

`check_ami.bash` and `check_ec2_instances.bash` were already retired — see
`DECISIONS.md` (2026-06-30 "Retire check_ami.bash and
check_ec2_instances.bash").

## Dependencies

- **Go**: 1.26+ (matches `codemeta.json`'s `softwareRequirements`,
  module-based build)
- **github.com/aws/aws-sdk-go-v2** and its `ec2`, `ssm`, `s3`, `sts`,
  `iam`, and `cloudfront` service packages — see `DECISIONS.md` ("Use
  official AWS SDK for Go v2"; `iam` added for Feature 2/3's instance
  profile pick-or-create, `cloudfront` for the CloudFront domain)
- **github.com/rsdoiel/termlib** for terminal output and interactive
  input (`internal/ui`) — see `DECISIONS.md` ("Use github.com/rsdoiel/
  termlib for the Terminal UI")
- **gopkg.in/yaml.v3** for `~/.awsops` config file parsing
  (`internal/config`) — see `DECISIONS.md`, "Add a `~/.awsops` YAML
  config file for awsops' own operational settings"
- **Go standard library `testing`** for unit tests; no external test
  framework needed (replaces BATS)

No `jq`, no AWS CLI, and no `bash`/`grep`/`tr` version- or locale-dependent
behavior at runtime — the compiled binary only needs the Go standard
library, the AWS SDK, and `termlib` (both pre-approved dependencies per
`CLAUDE.md`).

## Assumptions

1. AWS credentials are already configured and resolvable by the SDK's
   default credential chain (`~/.aws/credentials`, environment variables,
   or SSO)
2. **The tool's own identity** (the operator running it) needs:
   `ec2:DescribeInstances`, `ec2:DescribeImages`, `ec2:DescribeKeyPairs`,
   `ec2:DescribeSecurityGroups`, `ec2:DescribeSubnets`, `ec2:DescribeVpcs`,
   `ec2:DescribeIamInstanceProfileAssociations`, `ec2:RunInstances`,
   `ec2:StartInstances`, `ec2:StopInstances`,
   `ec2:TerminateInstances` (also used for cloud-init AMI extraction
   cleanup),
   `ec2:CreateImage`, `ec2:DeregisterImage`, `ec2:CreateTags`,
   `ec2:DeleteTags`, `ec2:DescribeTags` (for the Project/Environment
   tagging convention and Manage Tags),
   `ec2:DescribeInstanceAttribute` (for Show/Export Cloud-Init),
   `ec2:DescribeVolumes` (for Create AMI from Instance's volume-size time
   estimate and prior-snapshot detection -- missing from this list until
   Phase 10 surfaced it),
   `ec2:DescribeInstanceTypeOfferings` (Feature 2/3's instance-type-vs-
   subnet-Availability-Zone pre-flight check),
   `ec2:DescribeInstanceTypes` (Feature 2/3's instance-type-vs-AMI-ENA-
   support pre-flight check),
   `ssm:SendCommand`, `ssm:GetCommandInvocation`,
   `ssm:DescribeInstanceInformation` (fstrim, Show/Export Cloud-Init's AMI
   path, and Backup Archive & Trim), `s3:HeadObject` (for Backup Archive &
   Trim's independent verification step — a read-only check against
   whatever bucket the operator specifies),
   `iam:ListInstanceProfiles`, `iam:ListRoles`, `iam:CreateInstanceProfile`,
   `iam:AddRoleToInstanceProfile` (Feature 2/3's IAM instance profile
   pick-or-create; see `DECISIONS.md`, "Support picking or creating an
   IAM instance profile from within awsops").
   For Key Management (Features 13-16): `ec2:ImportKeyPair`,
   `ec2:DeleteKeyPair` (`ec2:DescribeKeyPairs` and `ec2:CreateKeyPair` are
   already listed above).
   For the S3 domain (Features 17-21): `s3:ListAllMyBuckets`,
   `s3:GetBucketLocation`, `s3:GetBucketWebsite`, `s3:CreateBucket`,
   `s3:PutPublicAccessBlock`, `s3:PutBucketWebsite`, `s3:PutBucketPolicy`,
   `s3:PutObject`, `s3:ListBucket`, `s3:GetObject`, `s3:DeleteObject`.
   For the CloudFront domain (Features 22-25): `cloudfront:ListDistributions`,
   `cloudfront:GetDistribution`, `cloudfront:CreateDistribution`,
   `cloudfront:CreateOriginAccessControl`, `cloudfront:CreateInvalidation`,
   `cloudfront:GetInvalidation`.
3. **Separately, each target instance's own IAM instance profile** needs
   `s3:PutObject` (and likely `s3:ListBucket` scoped to its own prefix) on
   the backup destination bucket, for Backup Archive & Trim's upload phase
   to work — this is a different AWS principal from #2 above, provisioned
   via the instance's own profile/cloud-init, not by this tool
4. The S3 bucket for backup archival does not exist yet as of this
   writing — real-AWS verification of Backup Archive & Trim is blocked on
   it being created (tracked outside this project)
5. Default VPC and subnet exist in each region, or user will provide
   specific values
6. Key pairs exist in each region, or user will create them separately

## Error Handling Strategy

1. **AWS API errors**: the SDK returns typed errors; unwrap and display the
   AWS error code/message clearly (no more parsing free-text CLI stderr)
2. **Validation errors**: prompt user to re-enter invalid inputs
3. **Network/timeouts**: retry with exponential backoff (max 3 attempts),
   using the SDK's built-in retry support where possible
4. **Missing dependencies**: clear error message if AWS credentials cannot
   be resolved at startup
5. **Permission errors**: display the required IAM permission and exit

## Debug Logging

`-debug` writes a line-delimited JSON (JSONL) record of every AWS SDK
call awsops makes during the session, to a timestamped file in the
current directory (`awsops-debug-<timestamp>.jsonl`), for diagnosing
unexpected behavior without re-running under a debugger. Modeled on the
same pattern used for `~/Laboratory/harvey`'s own `--debug` JSONL log.

- Every EC2/SSM/S3/STS call is wrapped by a logging decorator
  (`internal/awsclient`'s `Wrap*` functions) that records the method
  name, region, request params, duration, and either the response or
  the error — one JSON object per line, so the file can be tailed or
  processed with `jq` while awsops is still running
- The decorator is built on `internal/debuglog`'s nil-safe `*DebugLog`:
  every logging method is a no-op on a nil receiver, so `-debug=false`
  (the default) costs nothing beyond one nil check per client at
  startup — no `if debug` conditionals scattered through workflow code
- awsops prints the log's path to stderr once, at startup, when `-debug`
  is set, so the operator knows where to `tail -f` it
- The log records AWS resource identifiers and request/response
  parameters — not credentials, and no customer data ever passes
  through awsops — but treat a debug log file as containing this
  team's infrastructure details (instance IDs, AMI names, security
  group/subnet IDs) and handle it accordingly (don't attach it to a
  public issue, etc.)
- Exception: `ec2:CreateKeyPair`'s response carries the new key pair's
  unencrypted private key material, which must never reach the debug
  log even though everything else does — its logging wrapper redacts
  `KeyMaterial` to a fixed marker before writing the record, rather
  than skipping the whole call's output

## Security Considerations

1. Never store AWS credentials in the binary or repo; rely on the SDK's
   standard credential chain
2. Always confirm destructive operations (AMI removal)
3. Display instance costs/estimates when creating (if possible)
4. Warn about public AMIs vs private AMIs
5. For AMI creation from instances: warn about any sensitive data on the
   instance, and carry forward the Invenio RDM crash-consistency guidance
   for running-instance snapshots
6. Show/Export Cloud-Init's AMI path launches a real, billable instance —
   it must warn the user this costs time/money before proceeding (unlike
   every other read-only operation in this tool), and it must guarantee
   the temporary instance is terminated even when SSM never comes online
   or the extraction command fails, so a failed extraction never leaves a
   forgotten running instance behind
7. Backup Archive & Trim deletes real backup files — it must never delete
   a file based solely on the instance's own self-reported upload success;
   the tool's independent `s3:HeadObject` verification is the actual
   authorization for the delete step, not a redundant nice-to-have
8. `-debug`'s JSONL log (see "Debug Logging" above) is written unencrypted
   to the current directory and is not automatically cleaned up — it's
   the operator's responsibility to remove old debug logs, same as any
   other local diagnostic file
9. A newly created key pair's private key material never touches AWS
   again after `ec2:CreateKeyPair` returns it — awsops writes it to
   `~/.ssh/<name>.pem` with `0600` permissions immediately and never
   logs the raw material anywhere (including `-debug`'s log; see
   "Debug Logging" above)
10. S3 buckets default to `s3:PutPublicAccessBlock` fully enabled at
    creation (Feature 18); a public-read bucket policy is never the
    default path to a static website — CloudFront + Origin Access
    Control is (Feature 19, Feature 24), so a bucket stays private and
    only a specific CloudFront distribution can read it
    (`s3:PutBucketPolicy` scoped by `AWS:SourceArn`, Feature 24 step 7).
    An operator can still opt into a public-read bucket policy
    explicitly, but that path requires its own separate confirmation
    that plainly states the bucket becomes world-readable directly
11. Feature 20's bucket-only-object deletion (during a sync) and
    Feature 16's Delete Key Pair both require a **separate** explicit
    confirmation step from whatever triggered them — never folded into
    a broader "yes" (e.g. "yes, sync" must never also silently mean
    "yes, delete"), the same principle already applied to Feature 11's
    upload/verify/delete separation
12. Feature 24 (Create Distribution) provisions real, billable
    infrastructure and must say so before creating; it is not gated at
    Compute's destructive-operation tier (dry-run + type-to-confirm)
    since creating a distribution isn't itself destructive, but the
    on-screen confirmation should be explicit that this isn't a free,
    instantaneous operation
13. Feature 21.1 (Manage Bucket Lifecycle Policies) doesn't delete
    anything itself — it edits rules AWS evaluates later (typically
    within 24-48 hours per AWS's own lifecycle evaluation cadence, not
    instantly). Adding or editing an expiration rule is still a plain
    yes/no confirm (not the stronger dry-run + type-to-confirm tier),
    but the on-screen confirmation must say plainly that this schedules
    future automated deletion, not an immediate one — an operator should
    never be surprised days later by objects that quietly vanished

## Domain Knowledge Carried Forward from the Bash Version

These are operational facts specific to this team's infrastructure
(primarily Invenio RDM instances) that must not be lost in the rewrite —
ported from `ami_copy_basic_steps.md` and `DECISIONS.md`:

- **Volume-size time estimates** for AMI creation:

  | Volume size | Typical time  |
  |-------------|---------------|
  | < 20 GB     | 5–15 minutes  |
  | 20–100 GB   | 15–45 minutes |
  | 100–200 GB  | 45–90 minutes |
  | 200+ GB     | 1.5–3+ hours  |

  An Invenio RDM instance (Docker images, PostgreSQL data, OpenSearch
  indices) is typically 20–60 minutes.
- **Crash-consistency on running-instance snapshots** (`--no-reboot`):
  PostgreSQL and OpenSearch replay their logs on first boot and recover
  cleanly; Redis session/cache data may be lost (ephemeral by design);
  Docker container images on disk are unaffected.
- **SSM fstrim pre-snapshot step**: if SSM is available on the instance,
  offer to run `fstrim` before snapshotting to reduce copy time by skipping
  already-freed blocks; skip cleanly (not an error) if SSM is unavailable.
- **Prior-snapshot detection**: if a volume already has a prior snapshot,
  note that only changed blocks will be copied and actual time may be
  shorter than the estimate.
- **Correction vs. `ami_copy_basic_steps.md`**: that document states
  additional attached EBS volumes "must be snapshotted separately" from
  the root volume. That is not accurate for EBS-backed volumes —
  `create-image` snapshots every currently-attached EBS volume (per the
  instance's block device mapping) into the new AMI by default, and a new
  instance launched from that AMI gets all of them back automatically.
  The caveat only applies to ephemeral instance-store volumes, which are
  never captured in an AMI regardless. Verify this against the actual
  Invenio RDM instances' volume layout (root vs. any separately-attached
  EBS volumes for DB/search data) before implementing Phase 5, since the
  volume-size time estimate should already be summing across all attached
  volumes (see `gather_volume_info()` in `ec2_ami_manager.bash`), but the
  "must be snapshotted separately" framing should not carry into the Go
  version's user-facing guidance.
- **Postgres backup accumulation** (grounds Feature 11, Backup Archive &
  Trim): backups write to `/opt/rdm_sql_backups` as
  `<project>-db-<n>-<project>-<date>.sql.gz`, roughly one per day. A live
  check on `newauthors` found 87GB accumulated (no rotation at all), at
  ~980MB/day — this is the concrete, measured cause of the
  over-provisioned-disk/slow-cloning problem that motivated this whole
  design conversation.

## Deferred to a Later Version

These directly serve the stated product goal (speed up upgrades, create
accurate test environments with confidence) but are intentionally out of
v1's scope — see `DECISIONS.md`, "V1 scope: ship the four primitives
first, defer composite workflows" and "Structure workflows for future
record/replay". Recorded here so they aren't lost:

- **Recorded Scripts ("session playbooks")**: capture the sequence of
  actions taken in an interactive session as an editable, replayable YAML
  script — analogous to a "skill" for a language model, but for this
  deterministic tool. Values are templated (Go `text/template` over the
  YAML text before parsing), not just literal captured values, so a saved
  script can be repurposed against a different instance/AMI/environment.
  Safety gates (dry-run, confirmation) are always enforced on replay via
  the same reusable confirmation gate interactive mode uses — never
  bypassed except by a deliberate, explicit opt-in, and never bypassable
  at all for anything touching `Environment=production`. v1 does not build
  the recorder or replay engine, but Phases 4/5/6/7/8 are structured
  (params-struct/confirm-gate seam) so this can be added later without
  reopening that code. See `DECISIONS.md`, "Structure workflows for future
  record/replay".
- **Clone instance for testing**, **Upgrade with rollback point**, and
  **Bake AMI from cloud-init** (given a base AMI + a cloud-init YAML from
  `cloud-init-examples`, launch → wait for `cloud-init status --wait` via
  SSM → `create-image` → terminate, in one guided flow — the same pattern
  tools like Packer's `amazon-ebs` builder or AWS EC2 Image Builder
  automate) are all composite sequences built from v1's primitives.
  Once Recorded Scripts exist, these likely become example scripts users
  save and repurpose rather than bespoke Go workflows — recorded here as
  the motivating use cases, not as separate features to build.
- **Inline diff against `cloud-init-examples`**: after Feature 10 decodes
  an instance/AMI's cloud-init, fetch a comparison file directly from
  `caltechlibrary/cloud-init-examples` (picked from a list, via GitHub's
  API) and show a unified diff in the tool, instead of exporting for a
  manual `git diff`. Deferred because the repo's files don't map 1:1 to
  this account's `Project` tag values yet (e.g. there's no
  `caltechauthors-init.yaml`), and it adds a runtime network dependency on
  GitHub that the rest of this tool doesn't have — see `DECISIONS.md`,
  "Add Show/Export Cloud-Init as a v1 primitive".
- **Edit AMI Description**: an AMI's `Name` is immutable (see Feature 8's
  note and `DECISIONS.md`, "Add Rename Instance as a v1 primitive; AMI
  Name is immutable"), but `Description` can be changed after creation via
  `ec2.ModifyImageAttribute` — the closest thing to a "rename" AWS
  actually allows for an AMI. Not requested for v1; noted so it isn't
  lost.
- **ACM certificate provisioning**: Feature 24 lets an operator attach an
  alternate domain name to a CloudFront distribution, but assumes a
  matching ACM certificate already exists in `us-east-1`; requesting and
  validating (DNS or email) a new certificate is not part of this tool.
- **CloudFront functions / Lambda@Edge / WAF association**: none of
  CloudFront's programmable-edge or firewall features are exposed;
  Feature 24 creates a plain S3-origin distribution only.
- **S3 bucket versioning and lifecycle rules**: Feature 18 creates a
  bucket with default settings (no versioning, no lifecycle policy); if
  this team wants versioned static-website buckets or automatic
  old-version expiry later, that's an addition to Feature 18, not a new
  domain.
- **`Environment=production` safety-gate extension**: Compute's extra
  warning before type-to-confirm on `Environment=production` resources
  (Features 6, 9) is not extended to Delete Key Pair, bucket/object
  deletion, or distribution changes in this round (see Feature 26) — a
  candidate for a later pass once it's clear which of these new
  resources actually accumulate a "production" tag in practice.
- **Recorded Scripts for the new domains**: the deferred "session
  playbooks" idea above was scoped against Compute's primitives; whether
  Key Management/S3/CloudFront workflows get the same params-struct/
  confirm-gate seam for future record/replay is an open question for
  when Recorded Scripts itself is actually built, not decided now.
