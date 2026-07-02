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

### Today's only setting: `regions`

```yaml
regions:
  - us-west-1
  - us-west-2
```

Defaults to `[us-west-1, us-west-2]` if unset or the file doesn't exist
(see `DECISIONS.md`, "Narrow configured regions to us-west-1/us-west-2").
These are the regions every region-fanned-out feature (instance/AMI
listing, key pair listing, official Ubuntu AMI lookup, and eventually
Key Management once it ships) iterates over. Built to accommodate, not
yet implementing, future settings this same file would naturally hold:
per-domain defaults once S3/CloudFront ship (e.g. a default backup
bucket), or overrides for the curated instance-type/Ubuntu-release lists
if those ever need site-specific tuning.

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

(Illustrative — the real listing also includes Project and Environment
columns; see Feature 1 and Feature 12 below.) Key Management, S3, and
CloudFront follow the same listing-then-menu pattern; their specific
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
  shown as "unknown" if untagged)
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
1. Pick an instance
2. Prompt for the backup directory (no default — e.g.
   `/opt/rdm_sql_backups`, but instances may differ) and an age threshold
   in days (no default — always an explicit, deliberate choice)
3. **Dry-run list** (SSM, read-only): show candidate files matching the
   age threshold, with size and age, before anything happens
4. **Type to confirm** before proceeding
5. **Upload phase** (SSM): the instance uploads each candidate file to S3
   via its own AWS CLI/credentials (SSM Run Command cannot bulk-transfer
   multi-hundred-MB files — see Feature 10's AMI-path constraint), and
   reports back a small per-file summary (S3 key, size). Nothing is
   deleted at this point
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
half.

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
then for each bucket a lightweight `s3:GetBucketLocation` (region) and
best-effort `s3:GetBucketWebsite` (a `NoSuchWebsiteConfiguration` error
just means "not configured," not a failure) to show whether static
website hosting is enabled, in one table (Name, Region, Static Website).

### 18. Create Bucket

Interactive workflow:
1. Prompt for a bucket name (globally unique; validate against S3's
   naming rules locally before calling AWS)
2. Prompt for region
3. Call `s3:CreateBucket`
4. Block public access by default (`s3:PutPublicAccessBlock`, all four
   settings on) — an operator who genuinely wants a public bucket must
   say so explicitly in Feature 19, not get it by omission here
5. Confirm creation

### 19. Configure Static Website Hosting

Interactive workflow for turning an existing bucket into a website
origin:
1. Pick a bucket
2. Prompt for index document (default `index.html`) and error document
   (default `error.html`)
3. Call `s3:PutBucketWebsite`
4. **Access pattern**: default and recommended path is CloudFront +
   Origin Access Control — this step only configures the website
   document settings on the bucket itself and hands off to Feature 24
   (Create Distribution) to actually front it; the bucket's
   public-access-block settings from Feature 18 are left untouched
   (still blocking public access) unless the operator explicitly opts
   into a public-read bucket policy instead, which requires its own
   explicit confirmation warning that the bucket contents become
   world-readable directly, independent of CloudFront
5. Confirm; if the operator arrived here from Feature 24, offer to return
   there to finish the CloudFront side

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

### 21. Browse/Manage Objects

Interactive workflow for ad-hoc bucket inspection outside the sync flow:
1. Pick a bucket
2. List objects (`s3:ListObjectsV2`, paginated the same way Feature 1's
   PickList pagination already handles >50 items)
3. Choose an object; offer to show metadata (size, last-modified,
   content-type) or delete it
4. Deletion is a plain yes/no per-object confirm — Feature 20's bulk sync
   deletion gets the stronger "separate confirm" treatment because it can
   affect many files at once; a single ad-hoc delete here is
   lower blast-radius

### CloudFront Domain

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
