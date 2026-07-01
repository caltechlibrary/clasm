# AWS Tools — awsops — Design

> **2026-07-01: Retargeted to Go.** See `DECISIONS.md` ("Retarget
> implementation from Bash to Go") for why. `ec2_ami_manager.bash` remains in
> this repo, unchanged, as the working reference for the behavior this
> document describes, until the Go version reaches parity and is verified
> against real AWS.

## Overview

An interactive Go CLI for administering AWS EC2 instances and AMIs for
this team's infrastructure, across four regions (us-east-1, us-east-2,
us-west-1, us-west-2). The tool is general-purpose — nothing in its
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

## User Experience Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  awsops — AWS Operations CLI                                    │
│  Regions: us-east-1, us-east-2, us-west-1, us-west-2            │
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
│  ===== MAIN MENU =====                                          │
│  1) Create EC2 instance from AMI                                │
│  2) Create EC2 instance from cloud-init YAML                    │
│  3) Start EC2 instance                                          │
│  4) Stop EC2 instance                                           │
│  5) Terminate EC2 instance                                      │
│  6) Manage tags for an instance or AMI                          │
│  7) Create AMI from EC2 instance (running or stopped)           │
│  8) Remove AMI                                                  │
│  9) Show/export cloud-init for an instance or AMI               │
│ 10) Archive stale backups to S3 and trim disk space             │
│ 11) Refresh resource lists                                      │
│ 12) Exit                                                        │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

(Illustrative — the real listing also includes Project and Environment
columns; see Feature 1 and Feature 12 below.)

## Core Features

### 1. Unified Resource Listing

On startup, the tool fetches and displays:
- All EC2 instances across the four configured regions
- For each instance: ID, Name (from tags), State, AMI ID, Region, Project
  and Environment (from tags — see "Project/Environment Tagging" below;
  shown as "unknown" if untagged)
- All AMIs owned by the current AWS account across the four regions
- For each AMI: ID, Name, Creation Date, Region, Project and Environment
- Listing can be grouped/filtered by Project and by Environment, so
  "show me everything for caltechauthors" or "show me only production" is
  a quick operation instead of scanning a flat list by name

### 2. Create EC2 Instance from AMI

Interactive workflow:
1. Display pick list of available AMIs (filtered by owned-by-account)
2. User selects an AMI
3. Prompt for required parameters:
   - Instance type (with sensible default suggestion)
   - Key pair name (free text — key pair names are already human-readable,
     unlike security group/subnet IDs, so a pick list mostly adds noise —
     but typing `new` instead of a name creates a fresh key pair on the
     spot via `ec2:CreateKeyPair`, saving the private key to `~/.ssh/
     <name>.pem` with `0600` permissions, for operators who don't want to
     reuse keys across instances; see "Debug Logging" below for why its
     response is handled specially in the `-debug` log)
   - Security group IDs (list available security groups)
   - Subnet ID (list available subnets)
   - IAM instance profile (optional)
   - User data (optional) — a cloud-init YAML or any other user-data
     script, entered inline **or loaded from a local file path** (e.g.
     pointing at a file from a local clone of `cloud-init-examples`)
   - Tags — `Name` (required), `Project` and `Environment` (suggested; see
     "Project/Environment Tagging Convention" below), plus any additional
     free-form tags
4. Confirm all parameters before launching
5. Launch instance; poll until `running`
6. **If user-data was provided**: wait for SSM to report `Online` and run
   `cloud-init status --wait` (bounded timeout — see `DECISIONS.md`,
   "Enhance Create Instance from AMI: cloud-init file input + completion
   check"), reporting cloud-init's actual completion status (`done` vs
   `error`), not just that the instance reached `running`. If SSM never
   comes online, skip this check cleanly (not an error) — not every AMI
   has SSM configured
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
1. Prompt for the cloud-init YAML — inline text or a local file path
   (e.g. a file from a local clone of `cloud-init-examples`)
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

## Architecture

```
cmd/awsops/
    main.go              ← Entry point; wires config, AWS clients, and the
                            interactive menu loop together

internal/awsclient/       ← Thin, typed wrapper over aws-sdk-go-v2
    ec2.go                 - per-region EC2 client construction
    ssm.go                 - per-region SSM client construction (fstrim,
                              cloud-init AMI extraction, backup archive)
    s3.go                   - S3 client construction (operator-credential
                              independent verification, see Feature 11)
    regions.go              - the four configured regions

internal/inventory/       ← Resource listing/aggregation
    instances.go            - ListInstances(ctx) across all regions
    images.go               - ListImages(ctx) (owned AMIs) across all regions

internal/ui/               ← Terminal interaction (replaces show_pick_list,
    picklist.go               display_instances/display_amis, prompts)
    display.go
    prompt.go

internal/workflow/         ← One file per operation (replaces the Bash
    launch_instance.go        "_workflow" functions; also backs Feature 3
                              (Create from Cloud-Init YAML) via the same
                              launch/poll/cloud-init-check execution path
    power_state.go           - Start/Stop/Terminate EC2 Instance
    manage_tags.go           - Manage Tags: add/update/remove, instance or AMI
    create_ami.go
    remove_ami.go
    cloud_init.go            - Show/Export Cloud-Init (instance + AMI paths)
    backup_archive.go        - Backup Archive & Trim (upload, verify, delete, fstrim)
    menu.go
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
- **github.com/aws/aws-sdk-go-v2** and its `ec2`, `ssm`, `s3`, `sts`
  service packages — see `DECISIONS.md` ("Use official AWS SDK for Go v2")
- **github.com/rsdoiel/termlib** for terminal output and interactive
  input (`internal/ui`) — see `DECISIONS.md` ("Use github.com/rsdoiel/
  termlib for the Terminal UI")
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
   `ssm:SendCommand`, `ssm:GetCommandInvocation`,
   `ssm:DescribeInstanceInformation` (fstrim, Show/Export Cloud-Init's AMI
   path, and Backup Archive & Trim), `s3:HeadObject` (for Backup Archive &
   Trim's independent verification step — a read-only check against
   whatever bucket the operator specifies)
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
