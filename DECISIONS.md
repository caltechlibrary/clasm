# AWS Tools — Architecture & UX Decision Log

This file records significant architectural and UX decisions for the interactive EC2/AMI manager, their rationale, and known trade-offs. New decisions are added at the top.

---

## 2026-07-01 — Key pair stays free text; Name tag moves earlier

**Context.** A prior fix (this same day) added pick lists for key pair
name, security group IDs, and subnet ID, fetched from the AMI's region,
after real-AWS testing surfaced a typo-prone free-text UX (typing a
security group *name* instead of an ID caused a real AWS
`InvalidParameterCombination` error). Further real-AWS testing then
showed the key pair pick list didn't help: unlike opaque `sg-xxxx`/
`subnet-xxxx` IDs, key pair names are already human-readable, and a flat,
unsorted list of every key pair across the account (16+ entries in one
real test run, most irrelevant to the AMI being launched) added noise
without solving a real problem. Separately, the user noted the Name tag
prompt landed too late in the flow — after all of instance type/key
pair/security groups/subnet/IAM profile/user data — when naming the
instance is a natural first step once the AMI is picked.

**Decision.**
- Revert the key pair prompt to free text (`ui.Prompt` with
  `requireNonEmpty`), removing `listKeyPairNames` and the pick-list
  wrapper. Security group IDs and subnet ID keep their pick lists —
  those IDs are the genuinely opaque case the original gap was about.
- Move the Name tag prompt to immediately after the AMI pick (and, for
  the cloud-init-first workflow, immediately after its own AMI pick),
  before Instance type and everything after it.

**Rationale.** Pick lists help when the identifier is opaque and the
account/region has more of them than a person can remember (security
groups, subnets). They don't help when the identifier is already a
name the user chose and knows — showing every key pair in the account
regardless of relevance is worse than just typing the name. Naming the
instance right after picking its AMI matches how a person actually
thinks through the workflow (what is this, then how is it configured).

**Trade-off.** A key pair pick list would still catch a typo'd key pair
name before `RunInstances` runs; free text defers that to AWS's own
error. Accepted, since `ec2:RunInstances` rejects an unknown key pair
name immediately and unambiguously, unlike the security-group-name-vs-ID
confusion this was originally meant to prevent.

---

## 2026-07-01 — Use github.com/rsdoiel/termlib for the Terminal UI

**Context.** PLAN.md's Phase 3 (Terminal UI) originally called for a
stdlib-only implementation (`text/tabwriter` for tables, `bufio.Scanner`
for the numbered pick list and free-text prompts) — no interactive-input
library existed under the pre-approved `github.com/rsdoiel` or
`github.com/caltechlibrary` namespaces (CLAUDE.md) at the time DESIGN.md
was drafted. The user has since pointed at `github.com/rsdoiel/termlib`
("a light weight terminal interface library... ncurses light"), which
already exists and is under the `github.com/rsdoiel` namespace, so no
separate approval discussion is needed per CLAUDE.md's pre-approved
dependency list.

**Decision.** Build `internal/ui` on `termlib` instead of a hand-rolled
stdlib-only implementation:
- `DisplayInstances`/`DisplayImages` use `termlib.Terminal` (buffered
  output, flushed via `Refresh`) plus `termlib.PadRight`/`termlib.Truncate`
  for column formatting, replacing `ec2_ami_manager.bash`'s
  `printf "%-20s ..."` pattern
- `PickList[T]` and `Prompt` are built on `termlib.LineEditor.Prompt`
  (readline-style editing, history, `Ctrl+C`/`Ctrl+D` handling) rather
  than a plain `bufio.Scanner` loop
- Tests drive `LineEditor` through an `os.Pipe()` (not a TTY), which
  `LineEditor.Prompt` detects and falls back to plain line reading for —
  documented and intended by `termlib` itself for exactly this use case,
  so no fake/mock input abstraction was needed

**Rationale.**
- `termlib` is pre-approved (CLAUDE.md: `github.com/rsdoiel` namespace)
  and already provides exactly what Phase 3 needs: buffered flicker-free
  terminal output, readline-style prompts with history and `Ctrl+C`/
  `Ctrl+D` handling, and column-formatting helpers (`PadRight`,
  `Truncate`) that map directly onto the Bash version's `printf`/
  `${var:0:N}` conventions
- Still numbered-menu based, not raw-keystroke arrow navigation —
  consistent with the existing "Numbered menu for pick lists" decision
  (2026-06-30); `termlib`'s raw-mode keystroke reading (`ReadKey`/
  `KeyReader`) is not used here
- Avoids reimplementing readline-style editing/history/multi-line-paste
  handling that a hand-rolled `bufio.Scanner` loop would not have had

**Rejected alternatives.**
- *Stdlib-only (`text/tabwriter` + `bufio.Scanner`)* — the original
  PLAN.md approach; works, but reinvents editing/history niceties
  `termlib` already provides, for no benefit now that an approved library
  covers it
- *A third-party TUI/prompt library outside the `github.com/rsdoiel` /
  `github.com/caltechlibrary` namespaces* — would need discussion per
  CLAUDE.md; moot once `termlib` was pointed out

**Consequences.**
- `go.mod` gains `github.com/rsdoiel/termlib` (direct) and its transitive
  `golang.org/x/term`/`golang.org/x/sys` dependencies
- `DESIGN.md`'s Dependencies section and its "no dependency beyond the Go
  standard library and the AWS SDK" claim are updated to include
  `termlib`
- If a gap surfaces in `termlib` while building later phases (e.g. a
  table-drawing helper `internal/ui` ends up hand-rolling), flag it back
  to the user for `~/Laboratory/termlib/TODO.md` rather than working
  around it silently in `awstools`

---

## 2026-07-01 — Add Create EC2 Instance from Cloud-Init YAML as a v1 primitive

**Context.** Feature 2 (Create Instance from AMI) already got a cloud-init
file-input and completion-check enhancement (see "Enhance Create Instance
from AMI" below), but the user pointed out that burying it as the 6th of
7 parameters inside an AMI-first workflow doesn't serve the actual use
case: someone who starts from "I have a cloud-init recipe, give me a
machine" has a different mental model than someone who starts from "give
me another copy of this AMI." That deserves to be its own visible
primitive, not an option nested inside Feature 2's parameter list.

**Decision.** Add "Create EC2 Instance from Cloud-Init YAML" as its own
v1 primitive (Feature 8): the cloud-init file is the *first* thing
collected, then a base AMI is picked, then the same remaining launch
parameters Feature 2 already collects. It shares Feature 2's underlying
execution path entirely (the same `LaunchInstanceParams` struct, the same
launch/poll/cloud-init-completion-check logic) — the only difference is
the order and framing of the interactive prompts. This is distinct from
the deferred "Bake AMI from cloud-init" idea: that one snapshots the
result into a new AMI and terminates the instance; this one leaves a real,
running, usable instance.

**Rationale.**
- Matches the user's explicit ask: this needed to be visible in the
  primitive list, not folded into another feature's parameter collection
- Sharing execution logic with Feature 2 avoids duplicating the
  launch/poll/cloud-init-check code — only the front-end prompt sequence
  differs, consistent with the params-struct/confirm-gate seam already
  required of every workflow (see "Structure workflows for future
  record/replay")
- Placed as Feature 8 (after Backup Archive & Trim, before Stop/Terminate
  Instance and the cross-cutting Project/Environment Tagging convention —
  see the companion decision below) rather than immediately after
  Feature 2, to avoid a costly renumbering cascade through Features 3-7
  and their many existing cross-references, for a placement decision that
  doesn't carry strong semantic weight either way

**Rejected alternatives.**
- *A sub-mode within Feature 2 ("how do you want to start: AMI or
  cloud-init?")* — was the initial implementation; rejected because the
  user wants it directly visible as its own primitive, not a branch
  hidden inside another feature's flow
- *Insert immediately after Feature 2, renumbering Features 3-7* —
  arguably better thematic grouping, but not worth the renumbering risk
  across this document's many existing Feature N cross-references for a
  placement question without a strong correctness argument either way

**Consequences.**
- `DESIGN.md` gets a new Feature 8
- `PLAN.md` gets a new Phase 10 ("Create Instance from Cloud-Init YAML"),
  inserted before Main Menu and Integration
- No new IAM permissions — reuses exactly what Feature 2/Phase 4 already
  needs (`ec2:RunInstances`, SSM for the completion check)

---

## 2026-07-01 — Add Start/Stop/Terminate EC2 Instance as v1 primitives

**Context.** Immediately after adding "Create Instance from Cloud-Init
YAML," the user pointed out more gaps: there's no way to stop a running
instance or to terminate/remove one, and then — while this very decision
was being written up — no way to start a stopped one back up either. The
v1 list only covered AMI lifecycle and instance *creation*, not the rest
of an instance's power-state lifecycle.

**Decision.** Add three more v1 primitives:
- **Start EC2 Instance** (Feature 9): pick a stopped instance, simple
  yes/no confirm (safe and reversible, the symmetric counterpart to Stop),
  `ec2:StartInstances`, poll until `running`, display connection info
  (public IP may have changed unless an Elastic IP is in use)
- **Stop EC2 Instance** (Feature 10): pick a running instance, simple
  yes/no confirm (stopping is reversible — data on EBS volumes persists,
  the instance can be started again), `ec2:StopInstances`, poll until
  `stopped` (bounded timeout)
- **Terminate EC2 Instance** (Feature 11): pick an instance, dry-run
  showing what would be destroyed — **including whether any attached EBS
  volume has `DeleteOnTermination=true`**, since that volume's data
  (including any not-yet-archived backups — see Backup Archive & Trim) is
  destroyed along with the instance — an `Environment=production` warning
  if tagged, type-to-confirm, then `ec2:TerminateInstances`. Same safety
  tier as Remove AMI (Feature 4)

**Rationale.**
- Stopping/starting and terminating are fundamentally different risk
  levels (reversible vs. permanent), so they get different confirmation
  tiers — matching this project's existing principle of scaling friction
  to actual risk rather than applying one blanket confirmation style
  everywhere
- Surfacing `DeleteOnTermination` in the dry-run closes a real gap this
  project already cares about: an instance can be terminated with its
  root volume set to delete-on-termination, destroying exactly the kind
  of not-yet-archived backup data Backup Archive & Trim exists to protect
- `ec2:TerminateInstances` was already a planned permission (for Show/
  Export Cloud-Init's AMI-path cleanup); only `ec2:StartInstances`/
  `ec2:StopInstances` are new

**Rejected alternatives.**
- *One combined "manage instance power state" primitive covering start/
  stop/terminate* — considered, but start/stop and terminate have
  different confirmation tiers and different risk profiles; combining
  them risks the lighter-weight start/stop confirmation habituating users
  to clicking through what should be a heavier gate for terminate

**Consequences.**
- `DESIGN.md` gets three new Features (9, 10, 11); Project/Environment
  Tagging moves from Feature 8 to Feature 12 (four new features inserted
  ahead of it: Feature 8 Create-from-Cloud-Init-YAML, Feature 9 Start,
  Feature 10 Stop, Feature 11 Terminate)
- `PLAN.md` gets three new Phases after the Create-from-Cloud-Init-YAML
  phase, before Main Menu and Integration; later phases renumbered
  accordingly
- `DESIGN.md`'s IAM permission list gains `ec2:StartInstances` and
  `ec2:StopInstances`

---

## 2026-07-01 — Name the CLI binary `awsops`

**Context.** The Go CLI had been referred to as `ec2-ami-manager`
throughout the Architecture sections, a name inherited from the original
Bash script's narrow scope. That name undersells what v1 now covers
(instance/AMI lifecycle, cloud-init inspection, backup hygiene, tag
management) and, worse, ties the tool's identity to a single operation.
The candidate `rdmctl` was also considered, but the user wants the name
to make two things clear: it's about AWS resource operational hygiene in
general, not explicitly tied to the Invenio RDM project specifically —
even though RDM instances are its primary use case today.

**Decision.** Name the CLI binary `awsops` (`cmd/awsops/`). The repository
itself stays named `awstools` (already fixed by the CMTools scaffold —
`codemeta.json`, `Makefile`'s `PROJECT = awstools`); the user may revisit
the repository name separately later, but that's not part of this
decision.

**Rationale.**
- Communicates general AWS operational hygiene rather than a single
  narrow operation (`ec2-ami-manager`) or a project-specific name
  (`rdmctl`) that would tie the tool's identity to Invenio RDM even though
  its mechanisms (tagging, backup archival, cloud-init inspection) aren't
  actually RDM-specific
- Leaves room for the tool to be useful for non-RDM AWS resources later
  without a confusing name

**Rejected alternatives.**
- *`rdmctl`* — clearer about the current primary use case, but locks the
  name to a project the tool isn't actually coupled to at the
  implementation level
- *`awstools` (reuse the repo name for the binary too)* — simplest, but
  the user prefers a distinct binary name in case the repo hosts more
  than one tool later

**Consequences.**
- `DESIGN.md`/`PLAN.md` Architecture sections and doc titles updated from
  `ec2-ami-manager` to `awsops` throughout
- `ec2_ami_manager.bash` (the Bash file itself) is unaffected — it's a
  filename, not the Go binary name, and stays as-is until retirement per
  the existing retire-after-verify plan

---

## 2026-07-01 — Enhance Create Instance from AMI: cloud-init file input + completion check

**Context.** Feature 2 (Create EC2 Instance from AMI) already had a
generic, optional "user data" text prompt — a cloud-init YAML could
technically go there today via freehand typing/pasting. Two real gaps
remained: no way to load it from a file instead of typing/pasting
multi-line YAML into a terminal, and no verification that cloud-init
actually *finished successfully* after launch — the existing poll only
waits for the EC2-level `running` state, not for cloud-init's own
completion. An instance can be "running" while its user-data provisioning
silently failed partway through, which directly undermines the "test new
versions/changes with confidence" goal this whole project is for.

**Decision.** Enhance Feature 2 with:
1. The user-data prompt accepts a local file path as an alternative to
   inline text (e.g. pointing at a file from a local clone of
   `cloud-init-examples`) — a plain local file read, no new AWS API
   surface
2. After the instance reaches `running`, if user-data was provided, wait
   for SSM to report `Online` and run `cloud-init status --wait` via SSM
   (bounded timeout — unlike Phase 5's unbounded AMI-creation poll, a
   cloud-init run on launch should finish in a bounded, predictable time,
   so an unbounded wait would just mask a real hang), reporting the
   actual completion status (`done` vs `error`) rather than only EC2's
   `running` state. If SSM never comes online, skip this check cleanly
   (not an error) — not every AMI has SSM configured

**Rationale.**
- File-path loading avoids re-typing/pasting multi-line YAML in a
  terminal prompt, at essentially zero implementation cost
- Verifying cloud-init's actual completion status closes a real
  "looks fine but isn't" gap — exactly the kind of silent failure that
  erodes confidence in a test environment
- Reuses Phase 6's SSM client/poll/bounded-timeout pattern rather than
  inventing a new mechanism

**Rejected alternatives.**
- *Fetch templates directly from `cloud-init-examples` via the GitHub
  API* — deferred for the same reason "Inline diff against
  cloud-init-examples" is deferred (see the Show/Export Cloud-Init
  decision): no clean mapping from this account's `Project` tags to the
  repo's filenames yet. Pointing at a local file (from your own clone)
  gets the practical benefit without that dependency
- *Unbounded wait for cloud-init completion* — rejected; unlike AMI
  creation, which can legitimately run for hours on large volumes, a
  launch-time cloud-init run should complete in a bounded, predictable
  window

**Consequences.**
- `DESIGN.md` Feature 2 is updated with both changes (still Feature 2, no
  renumbering)
- `PLAN.md` Phase 4 gains an explicit dependency on Phase 1 (for the SSM
  client, alongside the EC2 client it already needed) and new work items
  for file-path loading and the completion check

---

## 2026-07-01 — Broaden Rename Instance into a general Manage Tags primitive

**Context.** Immediately after adding "Rename Instance" (below), the user
pointed out the obvious generalization: renaming is just "update the
`Name` tag," and if the tool can create tags, it should be able to
manage tags generally — add, update, and remove, on any resource, not
just set `Name` on an instance.

**Decision.** Replace Feature 5 ("Rename Instance") with a general
"Manage Tags" primitive: pick a resource (instance or AMI), see its
current tags, then add a new tag, update an existing one, or remove one.
Renaming is simply the common case of updating `Name` through this same
flow — no separate operation. Confirmation stays at the same lightweight
tier Rename Instance had (simple yes/no — tag edits are cheap and
reversible, not the dry-run/type-to-confirm tier reserved for actually
destructive operations), routed through the same reusable confirmation
gate as every other workflow.

**Rationale.**
- Avoids two overlapping menu items backed by the same underlying API
  (`ec2:CreateTags`) doing the same thing at different scopes
- AMIs get tags too (Project/Environment are set at creation per Feature
  3) but had no way to edit them after the fact — this closes that gap
  symmetrically for both resource types
- One general primitive is simpler to reason about, test, and eventually
  expose to Recorded Scripts than two narrow ones

**Rejected alternatives.**
- *Keep both Rename Instance and a separate Manage Tags primitive* — lets
  renaming stay a one-step action, but two menu items for the same
  underlying operation is redundant and was explicitly rejected in favor
  of consolidation
- *Tag management as an instance-only feature* — considered, but AMIs
  need the same capability (they carry Project/Environment tags too), so
  scoping it to instances only would just recreate the same gap for AMIs

**Consequences.**
- `DESIGN.md` Feature 5 is retitled "Manage Tags" and rewritten; no
  renumbering needed elsewhere since it's a same-slot replacement
- `DESIGN.md`'s IAM permission list gains `ec2:DeleteTags` (removal) —
  `CreateTags`/`DescribeTags` were already planned
- `PLAN.md` Phase 9 is retitled "Manage Tags" and its work items rewritten
  to cover add/update/remove on both instances and AMIs; still Phase 9,
  no renumbering elsewhere

---

## 2026-07-01 — Add Rename Instance as a v1 primitive; AMI Name is immutable

**Context.** The user noticed renaming was missing from the v1 primitive
list and asked whether the AWS SDK supports it. The two resources behave
completely differently:
- An EC2 instance's "Name" is not a real API attribute at all — it's just
  the `Name` tag by convention (the same tag this project's Project/
  Environment tagging convention already reads/writes). Changing it is a
  plain `ec2:CreateTags` call, already a planned permission
- An AMI's `Name` is a real attribute, but it is set once at `CreateImage`
  time and **cannot be changed afterward via the AWS API** — this is an
  AWS EC2 limitation, not a gap in the SDK or this tool. `ModifyImageAttribute`
  allows changing `Description` and launch permissions, but not `Name`.
  The only way to get an AMI with a different name is `CopyImage`
  (produces a brand-new AMI with a new ID and duplicated snapshots) plus
  deregistering the original — a materially heavier operation than a
  rename, closer in cost/risk to Feature 3 (create-AMI) than a quick edit

**Decision.** Add "Rename Instance" as a v1 primitive (pick an instance,
prompt for a new `Name` tag value, confirm, `ec2.CreateTags`). Do not add
an "AMI rename" primitive of any kind. Feature 3 (Create AMI from EC2
Instance) keeps its existing default-name-suggestion behavior unchanged,
but gains an explicit note that the name is permanent once created, so
the user isn't surprised later. "Edit AMI Description" (the one AMI
attribute that *is* mutable) is recorded as a deferred, not-yet-requested
idea rather than built now.

**Rationale.**
- Renaming an instance is cheap, reversible, and was a real gap — no
  reason to defer it
- Silently supporting "rename" for AMIs by only updating a tag while the
  real `Name` attribute stays stale would be actively misleading, since
  AWS's own console/CLI would keep showing the original name
- Building a "rename via copy + deregister" primitive for AMIs was
  considered and rejected: it's a different operation with different
  risk (storage duplication, a new AMI ID, anything referencing the old
  ID breaks) than what a user asking to "rename" would expect

**Rejected alternatives.**
- *AMI rename via CopyImage + DeregisterImage* — technically the only way
  to get a differently-named AMI, but it's a heavyweight operation
  disguised as a rename; not built for v1
- *Only update the AMI's tags, leave Name attribute alone* — would create
  a confusing mismatch between this tool's tag-based "name" and the AMI's
  actual `Name` shown everywhere else in AWS

**Consequences.**
- `DESIGN.md` gets a new Feature 5 ("Rename Instance"), renumbering
  Show/Export Cloud-Init, Backup Archive & Trim, and Project/Environment
  Tagging by one
- `PLAN.md` gets a new Phase 9 ("Rename Instance"); Phases 9-12 in the
  prior draft are renumbered to 10-13 accordingly
- No new IAM permission needed — `ec2:CreateTags` was already planned for
  the tagging convention

---

## 2026-07-01 — Structure workflows for future record/replay ("Recorded Scripts")

**Context.** Discussing "bake an AMI from cloud-init" led to a bigger idea:
capture the sequence of actions taken in an interactive session as an
editable, replayable script — analogous to how a "skill" packages a
procedure for a language model, but for this deterministic tool. The user
wants this to use YAML (prior success with YAML-driven configuration in
other projects, e.g. `dataset`'s web service config) and templated values
(not just literal captured values), with safety gates enforced on replay,
not bypassed. This is a substantial feature — a new execution mode
(record / replay) that cuts across the whole menu/dispatch loop, not a
single primitive — so it is not built in v1 (see "Recorded Scripts" under
"Deferred to a Later Version" in `DESIGN.md`/`PLAN.md`). It also
potentially subsumes the earlier-deferred "Clone instance for testing",
"Upgrade with rollback point", and "Bake AMI from cloud-init" composite
workflows: if a user can record a sequence once and replay it with
different values, those don't need to be bespoke Go features.

**Decision.** v1 does not build the recorder, the YAML schema, or the
replay engine. It does structure every confirmation-gated workflow
(Phases 4, 5, 6's AMI path, 7, 8) around a specific seam so that adding
record/replay later does not require reopening already-finished code:
1. Each workflow separates **building a resolved parameters struct**
   (interactive prompts fill it in v1) from **executing it against AWS**.
   The execution code takes a plain, typed struct (e.g.
   `CreateAMIParams{InstanceID, Name, Description, NoReboot, Tags}`) and
   never knows whether prompts or a future YAML file produced it
2. The **confirmation/dry-run gate is its own reusable step**, not inlined
   into each workflow's prompt loop, so a future replay engine can route
   through the identical gate rather than a second, parallel
   implementation of "is this safe to do"
3. When templating is eventually built, it applies via Go's standard
   library `text/template` to the YAML text before parsing — no new
   dependency, consistent with this project's stdlib-first preference

**Rationale.**
- The params-struct/execute split and the reusable confirmation gate are
  good structure on their own merits (testability, single source of truth
  for "is this safe"), so requiring them now costs nothing extra — it's a
  constraint on code already being written, not additional code
- Building the actual recorder/replay engine before v1's primitives have
  proven themselves against real AWS would be scope creep on an already
  large rewrite (see "V1 scope" below)
- Safety gates must not be bypassable by replay without deliberate,
  explicit opt-in — reusing the exact same gate function is the simplest
  way to guarantee that, rather than trusting a second implementation to
  stay in sync

**Rejected alternatives.**
- *Build record/replay now, as part of v1* — most directly useful, but
  conflicts with "V1 scope: ship the four primitives first" and adds a new
  execution mode on top of an already-large Bash→Go rewrite
- *Defer without any structural constraint on v1's workflows* — cheaper
  short-term, but risks having to rewrite Phase 4-8's internals later to
  retrofit the params-struct/confirm-gate seam once record/replay is
  actually built
- *Literal-only captured values (no templating)* — simpler, but the user
  specifically wants templating for repurposing a saved sequence across
  different targets (different instance, different environment), which a
  literal-only capture can't do without hand-editing every value each time

**Consequences.**
- `PLAN.md` Phases 4, 5, 6, 7, 8 each get a work item noting this
  structural requirement
- The "Deferred to a Later Version" entries for "Clone instance for
  testing", "Upgrade with rollback point", and "Bake AMI from cloud-init"
  are annotated as likely to become example Recorded Scripts rather than
  bespoke Go workflows, once the mechanism exists
- No new third-party dependency is introduced by this decision — YAML
  parsing (`gopkg.in/yaml.v3` or similar) would be needed when the feature
  is actually built, and would need the same approval process as
  `aws-sdk-go-v2` did, at that time

---

## 2026-07-01 — Add Backup Archive & Trim as a v1 primitive

**Context.** Today, cleaning up stale Postgres backups on an RDM instance
is a fully manual chore: log in, look at `/opt/rdm_sql_backups`, decide
what's old enough to remove, delete it by hand. A read-only SSM check of
`newauthors` found the real scale of the problem: 87GB in
`/opt/rdm_sql_backups`, one `<project>-db-<n>-<project>-<date>.sql.gz`
file per day at ~980MB, with no rotation at all (root volume: 484GB total,
157GB used, 328GB free — the backups are over half of what's used). This
is the concrete cause of the "over-provisioned disk makes cloning slow"
problem that motivated this whole design conversation. No S3 destination
exists yet for these backups — that's a separate infrastructure task the
user still needs to do, not something this project creates.

This conversation also surfaced a broader framing for the tool: it should
help with ongoing *administration* of these instances (not just
create/remove EC2/AMI lifecycle events), and with improving development,
test, and deployment workflows more generally.

**Decision.** Add "Backup Archive & Trim" as a v1 primitive: given an
instance, a backup directory, and an age threshold (both explicit prompts,
no baked-in default — same reasoning as the `Environment` tag having no
default), it uploads files older than the threshold to S3, independently
verifies each upload, deletes only the verified files, then runs `fstrim`.
The sequence is deliberately split into two separate remote steps with the
tool itself as the arbiter in between, rather than one script that
uploads-then-immediately-deletes based on its own success report:

1. **Dry-run list** (SSM, read-only): candidate files matching the age
   threshold, with size/age, shown before anything happens
2. **Type-to-confirm** (matches the AMI-removal safety tier, since step 5
   is irreversible)
3. **Upload phase** (SSM): the instance uploads each candidate file to S3
   (`aws s3 cp`, run from the instance — see rejected alternatives) and
   reports back a small per-file JSON summary (S3 key, size). Nothing is
   deleted yet
4. **Independent verification**: the tool itself, using its own AWS
   credentials (not the instance's self-report), calls `s3:HeadObject` on
   every uploaded key and confirms it exists with the expected size
5. **Delete phase** (a *second*, separate SSM command): the instance
   deletes exactly the tool-verified file list — it does not re-derive its
   own "what's stale" list, avoiding a time-of-check/time-of-use gap
6. **fstrim**, then a report of bytes freed and any files that failed
   verification (left untouched, flagged, not deleted)

**Rationale.**
- Matches this project's existing safety pattern for destructive
  operations (dry-run, then explicit confirm — see "Multi-layer
  confirmation for AMI removal")
- SSM Run Command is not a bulk file-transfer channel (confirmed when
  designing Show/Export Cloud-Init) — ~980MB backup files are far too
  large to round-trip through SSM output, so the upload must happen
  *from* the instance itself via its own AWS CLI/credentials
- Splitting upload and delete into two round-trips with independent
  verification in between means the tool — not the remote script — decides
  what's safe to delete, based on a second, independent read from S3.
  Trusting a single script's self-report to authorize an irreversible
  delete was considered and explicitly rejected (see below)

**Rejected alternatives.**
- *Single script: upload then immediately delete on self-reported success*
  — simpler, one round-trip, but means a script bug or a transient S3
  error that still reports "success" could delete backups with nothing
  actually saved. Rejected in favor of independent verification
- *Tool downloads/re-uploads the files itself instead of the instance
  doing it* — would let the tool verify with certainty, but requires
  streaming multi-GB files through the operator's machine or through SSM
  (impractical for the same reason raw file transfer was rejected for
  Show/Export Cloud-Init's AMI path)
- *Fold into the existing fstrim step* — considered, but the user chose a
  standalone primitive (see "Should v1 add composite workflows" discussion
  in the Show/Export Cloud-Init decision above); backup cleanup is useful
  on its own, independent of AMI creation

**Consequences.**
- Each target instance's IAM instance profile needs `s3:PutObject` (and
  likely `s3:ListBucket` scoped to its own prefix) on the destination
  bucket — this is a cloud-init/AMI change in `caltechlibrary/cloud-init-
  examples`, not something this Go tool can retrofit from outside. This is
  in scope for this project to specify (per the earlier "prereq scope"
  decision) even though the actual bucket creation and IAM/Terraform work
  happens separately
- The tool's own IAM permissions (the operator's identity, distinct from
  the instance's own instance profile) need `s3:HeadObject` (or
  `s3:GetObject`) on the bucket, for independent verification
- The S3 bucket itself does not exist yet — real-AWS verification of this
  primitive is blocked on that being created first (tracked in `TODO.md`,
  not by this project)
- Testing plan (per discussion with the user): unit tests first, with
  fakes for `EC2API`/`SSMAPI`/a new `S3API` (covering `HeadObject`); then,
  once those pass, a live test against a *throwaway instance launched from
  an existing AMI that already has these backups baked in* — never
  directly against the production instance. This reuses Phase 4
  (Create EC2 Instance from AMI) as a testing tool in its own right, and
  the throwaway instance must be terminated after the test, same cleanup
  discipline as Show/Export Cloud-Init's AMI path
- `DESIGN.md`'s Overview is broadened to describe ongoing instance
  administration and dev/test/deployment workflow support, not just
  EC2/AMI lifecycle management
- `PLAN.md` gets a new Phase 7 ("Backup Archive & Trim"); Phases 7-11 in
  the prior draft are renumbered to 8-12 accordingly

---

## 2026-07-01 — Add Show/Export Cloud-Init as a v1 primitive

**Context.** This team maintains a separate repository,
`caltechlibrary/cloud-init-examples`, of hand-authored cloud-init YAML
templates (e.g. `invenio-rdm.yaml`) used both for local Multipass
development VMs and as the source of the `--user-data` passed when
launching real EC2 instances. A live check found that `ec2:
DescribeInstanceAttribute` (attribute `userData`) returns the exact
base64-encoded cloud-init that launched a given instance, and that
`newauthors`'s actual deployed cloud-init has already drifted from
`cloud-init-examples`' `invenio-rdm.yaml` template (missing packages, no
`write_files` onboarding scripts, a different `runcmd` approach). This is
a real, live instance of the "accurate test environments" risk this whole
project is meant to reduce.

**Decision.** Add "Show/export cloud-init" as a fifth v1 primitive
(alongside create-instance, create-AMI, remove-AMI, and the tagging
convention), not deferred:
- **Instance path**: `ec2:DescribeInstanceAttribute` — read-only, free,
  instant, works for any existing instance
- **AMI path**: also supported in v1. Since an AMI has no user-data
  attribute of its own, extraction launches a temporary, disposable
  instance from the AMI, waits for SSM to come online (reusing the same
  SSM pattern already planned for the fstrim step), runs an SSM command to
  read `/var/lib/cloud/instance/user-data.txt` off disk, and *always*
  terminates the temporary instance afterward — including on failure or
  timeout. This path costs real AWS time/money (a running instance for
  several minutes) and requires an explicit confirmation before
  proceeding, unlike every other read in this tool
- **Export**: decoded YAML can be saved to a local file path for manual
  diffing against a local clone of `cloud-init-examples`. No inline
  fetch-and-diff against the GitHub repo in v1 — see rejected alternatives

**Rationale.**
- Directly serves the stated project goal: this is the concrete mechanism
  for detecting drift between what's actually deployed and the team's
  canonical cloud-init templates
- The instance path is essentially free to build (one more typed SDK call,
  already-planned dependencies) — there's no reason to defer it
- The AMI path reuses the SSM client and "poll with bounded timeout,
  always clean up" pattern already needed elsewhere, rather than
  introducing a new mechanism (e.g. mounting the AMI's snapshot on a
  helper instance, which would need an existing helper instance in every
  region and is more moving parts for the same result)

**Rejected alternatives.**
- *Instances only, defer AMI extraction* — was the initial recommendation
  (cost/complexity), but the user explicitly wants AMI coverage in v1
  since some of the AMIs this team manages have already outlived the
  instance they were created from
- *Fetch + diff inline against `cloud-init-examples`* — would directly
  answer "has this drifted?" without leaving the CLI, but requires solving
  a non-trivial file-mapping problem first (the repo's files don't map
  1:1 to this account's `Project` tag values today, e.g. there's no
  `caltechauthors-init.yaml`) and adds a runtime network dependency on
  GitHub. Deferred — see `DESIGN.md`/`PLAN.md` "Deferred to a Later
  Version"
- *Extract via snapshot-mount on a helper instance instead of launch+SSM*
  — avoids booting the AMI's OS at all, but requires a pre-existing helper
  instance per region and more novel mechanics for the same outcome

**Consequences.**
- `DESIGN.md`'s IAM permission list gains `ec2:DescribeInstanceAttribute`,
  `ec2:TerminateInstances`, and `ssm:GetCommandInvocation` (SendCommand and
  DescribeInstanceInformation were already listed for the fstrim step)
- The AMI path needs its own explicit confirmation prompt (cost/time, not
  free like every other v1 read) and a cleanup guarantee — tests must
  verify the temporary instance is terminated even when the SSM
  command fails or times out
- `PLAN.md` gets a new Phase 6 ("Show/Export Cloud-Init"); Phases 6-10 in
  the prior draft are renumbered to 7-11 accordingly

---

## 2026-07-01 — Introduce a light Project/Environment tagging convention

**Context.** The stated goal for this tool is to speed up upgrading RDM
deployments and creating accurate test environments across production and
development instances. A read-only check of the live account
(`aws ec2 describe-instances`) found tagging is inconsistent today: a
`project` tag exists on some instances (`caltechauthors`, `caltechdata`,
`caltechthesis`) but not others (`new-plots`, `thesis`,
`authors-test-recovery`), and there is no dedicated environment tag —
production vs. test is encoded only in the instance *name string* (e.g.
`caltechdata-test` vs. `oldcaltechdata`). `newauthors` is additionally
managed via an EC2 Launch Template, not ad hoc parameters.

**Decision.** The tool suggests/requires `Project` and `Environment`
(`production` | `development` | `test`) tags when creating new instances
and AMIs, uses them to group the resource listing, and adds extra
confirmation friction for destructive actions (AMI removal) on anything
tagged `production`. Existing untagged resources display as "unknown"
until tagged — the tool does not retroactively rewrite tags on resources it
didn't create.

**Rationale.**
- Directly serves the stated goal: distinguishing production from
  development/test at a glance, and grouping by application (`Project`),
  is exactly what "manage our RDM production and development instances"
  requires
- Inferring environment from free-text instance names (today's de facto
  approach) is fragile and inconsistent, as the live-account check showed
- Extra confirmation on production-tagged resources targets friction at
  the resource class where a mistake is most costly, rather than applying
  uniform friction everywhere

**Rejected alternatives.**
- *Work with what exists, no enforcement* — keeps inferring environment
  from the name string with no structured signal; considered, but doesn't
  move the account toward consistency and leaves "is this production?"
  a matter of reading a name carefully rather than checking a tag
- *Retroactively tag all existing resources* — out of scope for this tool;
  a one-time cleanup task, not an ongoing tool responsibility

**Consequences.**
- `PLAN.md` Phase 2 (listing) groups/filters by `Project`/`Environment`;
  Phases 4/5 (creation) prompt for and default these tags; Phase 6
  (removal) adds a heightened warning for `Environment=production`
- `DESIGN.md`'s IAM permission list must include `ec2:CreateTags` and
  `ec2:DescribeTags` (already present in `software_requirements.md`'s
  policy but missing from `DESIGN.md`'s own Assumptions section — fixed in
  this update)
- No migration or backfill of existing untagged resources is planned

---

## 2026-07-01 — V1 scope: ship the four primitives first, defer composite workflows

**Context.** The stated goal for this tool — speed up upgrading
deployments, create accurate test environments with confidence — is
naturally served by *composite* operations (e.g. "clone this instance for
testing, inheriting its network/instance-type config" or "upgrade with a
tracked rollback point"), not by the four raw primitives
(create-instance-from-AMI / create-AMI-from-instance / remove-AMI /
refresh) alone. Those primitives require the user to manually chain
operations and re-enter configuration (instance type, security groups,
subnet, IAM profile) from scratch each time, which is exactly the
slowness/accuracy-drift risk the stated goal wants to eliminate.

**Decision.** V1 of the Go tool ships a faithful port of the four existing
primitives (matching `ec2_ami_manager.bash`'s feature set), verified
against real AWS, before any composite workflow is added. "Clone instance
for testing" and "upgrade with a tracked rollback point" are recorded here
as intended fast-follow work, not dropped.

**Rationale.**
- Keeps the rewrite scoped and verifiable: Bash→Go parity is itself
  nontrivial (see "Retarget implementation from Bash to Go" below) and
  mixing in new composite behavior would make it harder to tell whether a
  bug is a porting regression or new-feature bug
- The primitives are the building blocks the composite workflows will call
  — building them first, correctly, is not wasted work
- Real-AWS verification (`TEST_PLAN_REAL_AWS.txt`) is easier to reason
  about against a known, already-specified feature set

**Rejected alternatives.**
- *Build composite workflows into v1 directly* — more directly serves the
  stated goal sooner, but risks conflating porting bugs with new-feature
  bugs during the highest-risk phase (initial Go implementation)

**Consequences.**
- `PLAN.md` gets a "Deferred / Future Work" section describing "Clone
  instance for testing" and "Upgrade with rollback point" so they aren't
  lost, to be scheduled once Phase 9 (real-AWS verification) passes
- The Project/Environment tagging convention (see companion decision
  above) is still built in v1, since the composite workflows will depend
  on it and it's needed for the listing/removal-friction behavior anyway

---

## 2026-07-01 — Retarget implementation from Bash to Go

**Context.** `ec2_ami_manager.bash` reached feature parity with the design
(Phases 0–7) but real-world use against production AWS resources surfaced a
string of bugs rooted in Bash's lack of static typing and its reliance on
runtime string construction for control flow:
- an `eval`-based array-copy helper (`show_pick_list`) had unbalanced
  quoting that crashed the interactive picker outright
- the AMI-name validation regex broke under BSD `grep` combined with a
  UTF-8 locale (`invalid character range`)
- the AMI-creation tag-specification string builder produced syntactically
  invalid AWS CLI shorthand that silently failed `create-image`, with no
  AMI created in any region and only a scrollback error message as evidence

Each bug class (shell quoting/escaping, locale-dependent tool behavior,
hand-built CLI argument strings) is structural to shelling out from Bash
rather than incidental, and none would be caught by static analysis before
runtime.

**Decision.** Set aside the Bash implementation and retarget the
interactive EC2/AMI manager to Go, in place in this repository, targeting
full feature parity with the existing design (all four operations: create
instance from AMI, create AMI from instance, remove AMI, main menu) before
the Bash version is retired.

**Rationale.**
- Go is this workspace's stated primary backend language (CLAUDE.md)
- A typed AWS SDK (see companion decision below) replaces "aws CLI + jq +
  eval" with compiled, typed API calls — the entire class of quoting/
  escaping bugs hit in this session becomes structurally impossible
- Go's `go test` and table-driven tests replace BATS plus a hand-rolled
  mock-`aws`-binary harness, and can mock the AWS SDK client via interfaces
  without shelling out at all
- The feature set, UX flow, and hard-won domain knowledge (multi-region
  aggregation, owned-AMIs-only scope, three-layer removal confirmation,
  fstrim/SSM pre-snapshot step, Invenio RDM crash-consistency guidance,
  volume-size time estimates) all carry forward unchanged — this is a
  reimplementation, not a redesign

**Rejected alternatives.**
- *Patch the specific bugs and stay in Bash* — would fix these three bugs
  but leaves the same eval/quoting/locale hazard class open for the next
  feature added
- *Python + boto3* — also a strong, typed-enough option with less ceremony
  than Go, but Go is this workspace's designated primary backend language
  (CLAUDE.md); Python/Perl are described there as secondary/legacy
- *Deno + TypeScript* — this workspace reserves Deno/TypeScript for
  middleware/frontend, not backend CLI tools (CLAUDE.md's layered
  architecture pattern)

**Consequences.**
- `ec2_ami_manager.bash`, `ami_copy.bash`, and their supporting docs remain
  in the repo unchanged as a working reference/spec until the Go version
  reaches parity and is verified against real AWS (the same retire-after-
  verify pattern already used for `ami_copy.bash`, see below)
- `DESIGN.md` and `PLAN.md` are retargeted for Go in this same update; the
  new `PLAN.md` phases restart from Phase 0 (Go module setup)
- BATS test debt for Phase 6/7 (`test_remove_ami.bats`, `test_menu.bats`) is
  superseded — those workflows get Go tests instead, not backfilled BATS
  tests
- `TEST_PLAN_REAL_AWS.txt`'s manual verification step now targets the Go
  binary, not `ec2_ami_manager.bash`

---

## 2026-07-01 — Use official AWS SDK for Go v2

**Context.** Retargeting to Go (see companion decision above) could either
keep the Bash version's shell-out pattern (`exec aws ...` from Go) or adopt
AWS's official Go SDK.

**Decision.** Use `github.com/aws/aws-sdk-go-v2` (with its `ec2` and `ssm`
service packages) for all AWS API calls. This is a third-party dependency
outside the pre-approved `github.com/rsdoiel` / `github.com/caltechlibrary`
namespaces (CLAUDE.md); explicitly approved for this project.

**Rationale.**
- Typed request/response structs eliminate the JSON-through-subshell-through-
  jq round trips that made the Bash version fragile
- No runtime dependency on the `aws` CLI binary or `jq` being installed —
  only the Go binary and AWS credentials
- Official, actively maintained by AWS
- Enables interface-based mocking of AWS calls in tests without a
  hand-rolled mock CLI binary (the pattern `tests/lib/test_helper.bash` used)

**Rejected alternatives.**
- *Shell out to the `aws` CLI from Go (`os/exec`)* — keeps the CLI-argument-
  quoting risk that caused this session's tag-specification bug, just moved
  into Go's `exec.Command` argument building; still requires the `aws` CLI
  as a runtime dependency
- *Hand-rolled AWS API client (SigV4 signing, raw REST calls)* —
  reinventing a well-solved, well-maintained problem for no benefit

**Consequences.**
- `go.mod` declares `github.com/aws/aws-sdk-go-v2` and its `ec2`/`ssm`/`sts`
  submodules as dependencies
- Credential resolution (env vars, `~/.aws/credentials`, SSO) is handled by
  the SDK's default credential chain, matching current Bash behavior
- Region iteration (the four configured regions) becomes explicit per-region
  SDK client construction, replacing the `AWS_REGION` env var wrapper
  pattern used in `ec2_ami_manager.bash`

---

## 2026-06-30 — Retire check_ami.bash and check_ec2_instances.bash

**Context.** `check_ami.bash` and `check_ec2_instances.bash` predate
`ec2_ami_manager.bash`. Both are non-interactive, read-only listing scripts
across the same four regions; their functionality is fully covered by
`list_ec2_instances()`/`list_amis()` and `display_instances()`/`display_amis()`
in `ec2_ami_manager.bash`, which additionally aggregate and sort consistently.

**Decision.** **Retire both scripts; the unified manager is the single
listing entry point.**

**Rationale.**
- No functionality in either script is missing from the manager
- Two parallel implementations of the same AWS queries is a maintenance cost
  with no offsetting benefit
- DESIGN.md's file-structure section listed them as "Existing" scripts to
  keep alongside the new manager without deciding their long-term role —
  this resolves that gap

**Rejected alternatives.**
- *Keep as quick non-interactive utilities* — considered for cron/scripting
  use cases, but nothing in this project currently invokes them
  non-interactively, and `ec2_ami_manager.bash` could add a non-interactive
  `--list` flag later if that need arises

**Consequences.**
- DESIGN.md's file-structure section should drop these two scripts
- Deletion is a separate, explicit step — not yet performed as of this entry

---

## 2026-06-30 — AMI-from-instance: fold ami_copy.bash capabilities into Phase 5

**Context.** `ami_copy.bash` (and `ami_copy_basic_steps.md`) was merged in
from a separate repository (`git log`: "merged ami copy from ami_copy
repo") before this project's DESIGN.md/DECISIONS.md/PLAN.md existed. It was
never reconciled with the design: it duplicates the "Create AMI from EC2
Instance" feature (Phase 5 — see "Include both running and stopped
instances for AMI creation" above) but is single-region only, and it
contains capabilities Phase 5's `create_ami_from_instance_workflow` lacks:
volume-size-based time estimates, prior-snapshot detection, an SSM `fstrim`
pre-snapshot optimization step, unbounded elapsed-time polling during
creation, and Postgres/OpenSearch-specific crash-consistency guidance for
running instances (relevant because this team's primary AMI target is
Invenio RDM instances).

Separately, Phase 5's `post_ami_creation_actions()` times out after 600
seconds (10 minutes) of polling. Per `ami_copy_basic_steps.md`'s own timing
table, this is too short for real usage — even small volumes take 5–15
minutes, and an Invenio RDM instance is estimated at 20–60+ minutes.

**Decision.** **Port `ami_copy.bash`'s capabilities into Phase 5's
multi-region workflow (tracked as Phase 5b in PLAN.md), then retire
`ami_copy.bash`.** Keep the multi-region aggregation Phase 5 already has
rather than narrowing to `ami_copy.bash`'s single-region scope.

**Rationale.**
- Multi-region aggregation (an earlier decision, see "All four regions
  aggregated in unified view" above) is more valuable than what
  `ami_copy.bash` offers and shouldn't be lost
- The volume-size estimate, fstrim step, and unbounded polling are real
  operational value specific to this team's large, stateful instances —
  losing them by simply deleting `ami_copy.bash` would be a regression
- The 600-second timeout in Phase 5 is a correctness bug independent of the
  consolidation question and should be fixed regardless

**Rejected alternatives.**
- *Keep both scripts indefinitely* — two divergent implementations of the
  same operation with different region scope is confusing and doubles
  future maintenance
- *Retire ami_copy.bash immediately without porting* — would silently drop
  working functionality (fstrim optimization, realistic time estimates,
  Invenio-specific guidance) that was never documented elsewhere

**Consequences.**
- New PLAN.md Phase 5b enumerates the specific functions/behavior to port
- `ami_copy.bash` and `ami_copy_basic_steps.md` are retired only after
  Phase 5b is implemented and verified, not before
- DESIGN.md's Core Features and File Structure sections should be updated
  once Phase 5b lands

---

## 2026-06-30 — AMI scope limited to account-owned only

**Context.** When listing available AMIs for instance creation, we must choose between showing all AMIs (public + private), only AWS marketplace AMIs, only account-owned AMIs, or a filtered subset.

**Decision.** **Show only AMIs owned by the current AWS account.**

**Rationale.**
- Reduces clutter and confusion for users managing their own infrastructure
- Public AMIs are typically accessed through other workflows (Launch Templates, console)
- Owned AMIs are the primary concern for lifecycle management (creation/removal)
- Simplifies the pick list to relevant, actionable items
- Aligns with the AMI removal feature which only makes sense for owned AMIs

**Rejected alternatives.**
- *All AMIs (public + private)* — would overwhelm users with thousands of irrelevant AMIs
- *Owned + AWS marketplace* — AWS marketplace AMIs cannot be deleted, creating inconsistency in the removal feature
- *Custom filter by tags* — adds complexity for initial implementation; can be added later as a filter option

**Consequences.**
- Users cannot launch instances from public AMIs through this script
- If needed, a future enhancement could add a "Show public AMIs" toggle
- The script focuses on managing custom/private infrastructure

---

## 2026-06-30 — All four regions aggregated in unified view

**Context.** The script must operate across four AWS regions. We must decide whether to show each region separately, aggregate all regions, or let the user select a region per operation.

**Decision.** **Aggregate all four regions in a single unified view.**

**Rationale.**
- Provides a complete picture of infrastructure across all regions at a glance
- Matches the user's stated requirement to "list the EC2 instances from one of the four regions we use"
- Simplifies the user experience by eliminating region selection from the critical path
- Instance and AMI IDs are globally unique, so aggregation doesn't cause collisions

**Rejected alternatives.**
- *Single region (user picks first)* — requires extra step and doesn't show full infrastructure state
- *Region per operation* — inconsistent UX, requires region selection for every action

**Consequences.**
- All API calls must be made against each of the four regions
- Aggregation logic needed to combine and deduplicate results
- Display must include region column for all resources
- Performance: slightly slower due to 4x API calls, but acceptable for interactive use

---

## 2026-06-30 — Prompt user for all instance creation parameters

**Context.** When creating an EC2 instance from an AMI, we need values for instance-type, key-name, security-group-ids, subnet-id, and other optional parameters. We must decide on the default behavior.

**Decision.** **Prompt the user for all required parameters interactively, with no hardcoded defaults.**

**Rationale.**
- Maximum flexibility for users with different requirements
- Prevents launching instances with inappropriate defaults
- Educational: helps users understand what's required for instance creation
- Reduces risk of launching instances in wrong subnet/security group

**Rejected alternatives.**
- *Hardcoded sensible defaults* — defaults may not be appropriate for all use cases; could lead to instances in wrong network configuration
- *Config file with overrides* — adds complexity; users may not have config files set up; harder to use across different projects

**Consequences.**
- More interactive steps required from user
- Need validation for each parameter
- Should provide lists of available options (key pairs, security groups, subnets) to help user choose
- May add helper to show "common" instance types with descriptions

---

## 2026-06-30 — User-provided AMI names for new AMIs

**Context.** When creating an AMI from an EC2 instance, we need a name for the new AMI. We must decide how this name is generated.

**Decision.** **Require user to provide the AMI name interactively.**

**Rationale.**
- AMI names are user-facing and should be meaningful
- Users have different naming conventions based on their organization
- Auto-generated names might not match organizational standards
- Simple and predictable for users

**Rejected alternatives.**
- *Auto-generate from instance name* — instance may not have a Name tag; generated names might not be descriptive enough
- *Auto-generate with prefix* — requires configuration of prefix; less flexible
- *Name + tags* — adds complexity for initial implementation

**Consequences.**
- User must think of a name each time (minor friction)
- Can add auto-suggestion in future based on instance metadata
- Name validation needed (AMI name constraints: 3-128 characters, specific allowed characters)

---

## 2026-06-30 — Multi-layer confirmation for AMI removal

**Context.** Removing an AMI is destructive and irreversible. We must implement safety mechanisms to prevent accidental deletion.

**Decision.** **Implement three-layer confirmation:**
1. Dry-run first: Show exactly what would be deleted
2. Show dependencies: List any instances currently using this AMI
3. Type to confirm: User must type the AMI ID or name exactly

**Rationale.**
- AMI deletion cannot be undone
- Instances using a deleted AMI cannot be launched or rebooted (if instance-store backed)
- Typo in selection could cause significant data loss
- Multiple confirmation layers provide defense in depth

**Rejected alternatives.**
- *Simple yes/no prompt* — too easy to accidentally confirm
- *Dry-run only* — doesn't prevent accidental confirmation
- *Show dependencies only* — doesn't verify user intentionality

**Consequences.**
- More steps required to delete an AMI (acceptable for safety-critical operation)
- Need to implement dependency checking (query instances by ImageId)
- Must handle case where AMI has no instances using it
- Error handling for typed confirmation mismatch

---

## 2026-06-30 — Use AWS CLI v2 with jq for JSON parsing

**Context.** We need to interact with AWS APIs and parse their JSON output in Bash.

**Decision.** **Use AWS CLI v2 with jq for JSON parsing.**

**Rationale.**
- AWS CLI v2 is the current standard, maintained by AWS
- jq is the de facto standard for JSON parsing in shell scripts
- Both are likely already installed in environments using AWS
- Provides full access to AWS API functionality

**Rejected alternatives.**
- *AWS CLI v1* — deprecated, no longer maintained
- *aws-shell* — not suitable for scripting
- *Boto3 (Python)* — requires Python, not pure Bash
- *Custom JSON parsing with grep/sed* — fragile, error-prone

**Consequences.**
- Script requires AWS CLI v2 and jq as dependencies
- Need to check for these dependencies on startup
- Error messages should guide users to install missing tools

---

## 2026-06-30 — Numbered menu for pick lists

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

## 2026-06-30 — Include both running and stopped instances for AMI creation

**Context.** When creating an AMI from an instance, the instance can be in various states. We must decide which states are allowed.

**Decision.** **Allow both running and stopped instances to be selected for AMI creation.**

**Rationale.**
- AWS supports creating AMIs from both running and stopped instances
- Stopped instances are common targets for AMI creation (clean state)
- Running instances might need AMIs created for backup purposes
- Matches the user's stated requirement

**Rejected alternatives.**
- *Running only* — excludes common use case of creating AMI from stopped instance
- *Stopped only* — excludes emergency backup scenarios
- *All states including terminated* — terminated instances cannot have AMIs created

**Consequences.**
- Need to filter out terminated instances from the pick list
- May want to warn user about creating AMI from running instance (potential inconsistency)
- For running instances, can offer no-reboot option

---

## 2026-06-30 — Refresh data after each operation

**Context.** After performing operations (create instance, create AMI, remove AMI), the displayed resource lists become stale. We must decide when to refresh.

**Decision.** **Refresh all resource data after each operation that modifies state, and provide a manual refresh option.**

**Rationale.**
- Ensures user always sees current state
- Automated refresh after modifications provides immediate feedback
- Manual refresh option allows user to see latest state at any time
- Simple to implement (re-run the listing functions)

**Rejected alternatives.**
- *Refresh only on demand* — user might not realize data is stale
- *Periodic auto-refresh* — adds complexity, unnecessary API calls
- *No refresh* — poor UX, user sees outdated information

**Consequences.**
- Each operation will include the cost of 8 API calls (4 regions x 2 resource types)
- Need to consider performance for slow connections
- Can add progress indicators during refresh
