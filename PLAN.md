# AWS Tools — awsops — Implementation Plan (Go)

## Source Design

See `DESIGN.md` and `DECISIONS.md` for the complete design and decision
rationale, including why this plan retargets from Bash to Go
(`DECISIONS.md`, 2026-07-01 "Retarget implementation from Bash to Go").

## Status (2026-07-01)

Starting from scratch. `ec2_ami_manager.bash` remains in the repo as the
working reference for expected behavior and stays in place, untouched,
until this plan's Phase 14 (main menu) is complete and
`TEST_PLAN_REAL_AWS.txt` passes against the Go binary. Nothing below has
been implemented yet.

Unlike the Bash plan, the AMI-from-instance capabilities that were folded
in later as "Phase 5b" (volume-size time estimate, fstrim/SSM step,
unbounded polling, Invenio RDM crash-consistency guidance) are included in
Phase 5 from the start — we already know they're required.

## Phases

TDD throughout: write the `*_test.go` file (with fakes for the AWS SDK
interfaces) before the implementation it covers.

---

## Phase 0 — Project Setup

**Effort:** ~2 hours
**Priority:** High

### Tasks

- [ ] `go mod init` for the module (name TBD — matches repo import path)
- [ ] Add `github.com/aws/aws-sdk-go-v2` and its `ec2`, `ssm`, `s3`, `sts`
      submodules, plus the config module for credential resolution
- [ ] Create `cmd/awsops/main.go` stub (prints version, exits)
- [ ] Create `internal/{awsclient,inventory,ui,workflow}` package skeletons
- [ ] Confirm `go build ./...` and `go vet ./...` are clean on the empty
      skeleton

---

## Phase 1 — AWS Client Layer

**Effort:** ~3 hours
**Priority:** High
**Files:** `internal/awsclient/`

### Work Items

- Define the four configured regions as a package-level constant/slice
  (`Regions = []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"}`)
- `NewEC2Client(ctx, region) (EC2API, error)` — constructs a region-scoped
  EC2 client from the SDK's default config/credential chain
- `NewSSMClient(ctx, region) (SSMAPI, error)` — same, for the fstrim step
- `NewS3Client(ctx, region) (S3API, error)` — for Backup Archive & Trim's
  independent verification (`HeadObject`) using the operator's own
  credentials, distinct from the target instance's own IAM instance
  profile (see `DESIGN.md` Assumptions)
- Define `EC2API`/`SSMAPI`/`S3API` as narrow interfaces covering only the
  SDK methods actually used (enables fakes in tests without a real client)
- Startup credential check: fail fast with a clear message if
  `sts:GetCallerIdentity` fails (replaces `check_dependencies`'s AWS CLI/jq
  checks — there's no external binary to check for anymore)

**Tests:** credential-check failure path; region client construction;
retry/backoff behavior on throttling errors (table-driven, using a fake
that returns a throttling error N times then succeeds)

**Dependency:** Phase 0

---

## Phase 2 — Resource Listing

**Effort:** ~4 hours
**Priority:** High
**Files:** `internal/inventory/`

### Work Items

- `ListInstances(ctx, clients map[string]EC2API) ([]Instance, error)` —
  queries `DescribeInstances` per region concurrently (goroutines +
  errgroup or similar), aggregates, excludes terminated instances
- `ListImages(ctx, clients map[string]EC2API) ([]Image, error)` — queries
  `DescribeImages` with `Owners: [self]` per region, aggregates, filters to
  `State == available`
- `Instance`/`Image` structs carry the same fields the Bash version
  displayed: InstanceId/ImageId, Name (from tags), State, ImageId, Region /
  Name, CreationDate, Region — plus `Project` and `Environment` (from
  tags, empty string if untagged — display layer renders empty as
  "unknown", see `DECISIONS.md` "Introduce a light Project/Environment
  tagging convention")
- Group/filter helpers over the aggregated results: by `Project`, by
  `Environment` — used by Phase 3's display and the main menu's listing

**Tests:** aggregation across regions; empty-region handling; terminated-
instance filtering; Name/Project/Environment-tag extraction (present/
absent); group/filter by Project and by Environment

**Dependency:** Phase 1

---

## Phase 3 — Terminal UI (done)

**Effort:** ~4 hours
**Priority:** High
**Files:** `internal/ui/`

Built on `github.com/rsdoiel/termlib` rather than a stdlib-only
implementation — see `DECISIONS.md`, "Use github.com/rsdoiel/termlib for
the Terminal UI".

### Work Items

- `DisplayInstances(t *termlib.Terminal, []inventory.Instance)` /
  `DisplayImages(t *termlib.Terminal, []inventory.Image)` — formatted
  table output via `termlib.PadRight`/`termlib.Truncate` (replaces
  `display_instances`/`display_amis`); untagged Project/Environment
  render as `"unknown"`, untagged Name renders blank, matching the Bash
  version
- `PickList[T any](t *termlib.Terminal, le *termlib.LineEditor, items []T, label func(T) string, prompt string) (T, error)`
  — generic numbered pick list; returns an error (not a panic/crash) on
  invalid input, re-prompts on out-of-range/non-numeric input, entering
  `0` cancels (`ErrCancelled`)
- `Prompt(t *termlib.Terminal, le *termlib.LineEditor, label string, opts ...PromptOption) (string, error)`
  — single free-text prompt with optional default value
  (`WithDefault`) and validator function (`WithValidator`), re-prompting
  on validation failure (replaces the repeated `echo -n ...; read -r ...`
  pattern)

**Tests:** pick list with valid/invalid/cancel input, and prompt with
default/validator, driven through `termlib.LineEditor` via an `os.Pipe()`
(not a TTY) rather than a real terminal -- `LineEditor.Prompt` detects the
non-TTY input and falls back to plain line reading, which is `termlib`'s
own documented/intended way to drive it in tests. This is the Go
equivalent of the Bash version's skipped "interactive testing is hard"
tests, and is not skipped here.

**Dependency:** Phase 2

---

## Phase 4 — Create EC2 Instance from AMI (done)

**Effort:** ~6 hours
**Priority:** High
**Files:** `internal/workflow/{launch_instance,launch_execute,cloud_init_check,confirm,ssm,userdata}.go`

Implemented as several smaller files rather than one, each independently
tested: `confirm.go` (the reusable yes/no gate — see below), `ssm.go`
(`WaitForSSMOnline`/`RunShellCommand`, generic building blocks reused by
Phases 10/12/13), `userdata.go` (`@file` vs. inline text), `cloud_init_check.go`
(the launch-time completion check), `launch_instance.go` (params struct +
interactive collection), `launch_execute.go` (`Launch`/`WaitUntilRunning`),
and `create_instance_from_ami.go` (the orchestrator PLAN.md originally
called out as this phase's one file).

`Confirm(t, le, question) (bool, error)` is the first instance of the
"reusable confirmation/dry-run gate" `DECISIONS.md`'s "Structure
workflows for future record/replay" calls for — a simple yes/no tier for
reversible actions. Phase 8/11/13's heavier dry-run + type-to-confirm
tier is a separate function, added when those phases need it.

### Work Items

- Pick an AMI (Phase 3's `PickList` over `ListImages` results)
- Collect instance type, key pair, security group(s), subnet, IAM instance
  profile (optional), user data (optional — accepts inline text **or a
  local file path**, e.g. a file from a local clone of
  `cloud-init-examples`), tags — mirroring `collect_instance_params()`'s
  parameter set, plus `Project` (default: the source AMI's `Project` tag
  if set) and `Environment` (always an explicit prompt, no default — see
  `DECISIONS.md` "Introduce a light Project/Environment tagging
  convention")
- Confirm and call `ec2.RunInstances`
- Poll `DescribeInstances` until `running` (bounded — 5 minutes, matching
  current Bash behavior) or report a timeout; display connection info
  (public/private IP, SSH command) on success
- **If user-data was provided**: wait for SSM to report `Online`, then run
  `cloud-init status --wait` via SSM (bounded timeout — see
  `DECISIONS.md`, "Enhance Create Instance from AMI: cloud-init file
  input + completion check"), reporting cloud-init's actual completion
  status (`done` vs `error`). Skip cleanly (not an error) if SSM never
  comes online
- Structure as: build a resolved `LaunchInstanceParams` struct (from
  prompts) → reusable confirmation/dry-run gate → execute against
  `ec2.RunInstances`. This is the seam a future replay engine reuses
  rather than reimplementing (see `DECISIONS.md`, "Structure workflows
  for future record/replay")

**Tests:** parameter collection with a fake reader; user-data-from-file
loading (success and file-not-found); launch success/failure;
poll-until-running with a fake that transitions state after N calls; poll
timeout path; cloud-init completion check (done/error/SSM-unavailable)

**Dependency:** Phase 1 (for the SSM client), Phase 3

---

## Phase 5 — Create EC2 Instance from Cloud-Init YAML (done)

**Effort:** ~2 hours
**Priority:** Medium
**Files:** `internal/workflow/launch_from_cloud_init.go`,
`create_instance_from_cloud_init.go`; extracted the shared execution
logic from Phase 4's `create_instance_from_ami.go` into a new
`runLaunch` function in `launch_execute.go`, confirmed behavior-preserving
against Phase 4's existing tests before adding this phase's own.

### Work Items

- Prompt for the cloud-init YAML first — inline text or a local file path
  (reuses Phase 4's file-loading logic)
- Pick a base AMI (Phase 3's `PickList` over `ListImages`)
- Collect the remaining parameters and execute via the *same*
  `LaunchInstanceParams` struct and execution function Phase 4 uses —
  this phase only adds a different front-end prompt sequence, not new
  execution logic (see `DECISIONS.md`, "Add Create EC2 Instance from
  Cloud-Init YAML as a v1 primitive")
- Same post-launch behavior as Phase 4: poll until `running`, then wait
  for SSM + `cloud-init status --wait`, reporting completion status

**Tests:** prompt-ordering test confirming cloud-init is collected before
the AMI pick list; otherwise covered by Phase 4's execution-path tests
(no new execution logic to test independently)

**Dependency:** Phase 4 (shares its execution path directly)

---

## Phase 6 — Start EC2 Instance (done)

**Effort:** ~2 hours
**Priority:** Medium
**Files:** `internal/workflow/power_state.go`

Reuses Phase 4's `WaitUntilRunning` (poll-until-running) and the
`displayConnectionInfo` helper extracted from `launch_execute.go` when
Phase 5 shared its execution path — no new poll/display logic needed
here, so the timeout path is covered by Phase 4's existing tests rather
than re-tested at this orchestrator level (same reuse pattern as Phase
5's own Tests note).

### Work Items

- Pick a stopped instance
- Confirm (simple yes/no, via the same reusable confirmation gate as
  every other workflow)
- Call `ec2.StartInstances`
- Poll `DescribeInstances` until `running` (bounded timeout)
- Display connection info (public/private IP, SSH command) — note the
  public IP may have changed since the instance was last running, unless
  it uses an Elastic IP

**Tests:** start success/failure; poll-until-running with a fake state
transition; poll timeout path; confirmation decline (no API call made)

**Dependency:** Phase 3

---

## Phase 7 — Stop EC2 Instance

**Effort:** ~2 hours
**Priority:** Medium
**Files:** `internal/workflow/power_state.go`

### Work Items

- Pick a running instance
- Confirm (simple yes/no — reversible, unlike Phase 8's terminate)
- Call `ec2.StopInstances`
- Poll `DescribeInstances` until `stopped` (bounded timeout)

**Tests:** stop success/failure; poll-until-stopped with a fake state
transition; poll timeout path; confirmation decline (no API call made)

**Dependency:** Phase 3

---

## Phase 8 — Terminate EC2 Instance

**Effort:** ~6 hours
**Priority:** High
**Files:** `internal/workflow/power_state.go`

### Work Items

- Pick an instance
- Dry-run display: what would be destroyed, **including whether any
  attached EBS volume has `DeleteOnTermination=true`** (query
  `BlockDeviceMappings` on the instance) — that data is destroyed along
  with the instance, potentially including not-yet-archived backups (see
  Phase 13)
- Environment check: if `Environment=production`, show an additional
  explicit warning before type-to-confirm (see `DECISIONS.md`, "Introduce
  a light Project/Environment tagging convention")
- Type-to-confirm: user must type the instance ID or name exactly
- Call `ec2.TerminateInstances`
- Structure as: build a resolved `TerminateInstanceParams` struct →
  reusable confirmation/dry-run gate → execute against
  `ec2.TerminateInstances` (see `DECISIONS.md`, "Structure workflows for
  future record/replay")

**Tests:** dry-run display, including the `DeleteOnTermination` warning
present/absent; production-tag warning shown/not-shown; type-to-confirm
match/mismatch; termination success/failure

**Dependency:** Phase 3

---

## Phase 9 — Manage Tags

**Effort:** ~4 hours
**Priority:** Medium
**Files:** `internal/workflow/manage_tags.go`

### Work Items

- Pick a resource — an instance or an AMI
- Display its current tags
- Choose an action: add (new key/value), update (pick existing key,
  prompt new value), or remove (pick existing key)
- Confirm (simple yes/no — this is cheap and reversible, not the
  dry-run/type-to-confirm tier used for AMI removal or backup deletion)
  via the same reusable confirmation gate used throughout this project's
  workflows, for consistency and future replay support (see
  `DECISIONS.md`, "Structure workflows for future record/replay")
- Call `ec2.CreateTags` (add/update) or `ec2.DeleteTags` (remove)
- Renaming an instance is just updating its `Name` tag through this same
  flow — no separate code path. This never touches an AMI's `Name`
  attribute itself, which is immutable via the AWS API once set at
  `CreateImage` time — see `DECISIONS.md`, "Add Rename Instance as a v1
  primitive; AMI Name is immutable"
- If the edited key is `Environment`, show a brief note that it's the
  same tag used elsewhere in this tool (Phase 11, Remove AMI, and Phase 8,
  Terminate Instance) to gate production warnings

**Tests:** add/update/remove success paths, for both an instance and an
AMI target; confirmation decline (no API call made); `CreateTags`/
`DeleteTags` failure handling

**Dependency:** Phase 3

---

## Phase 10 — Create AMI from EC2 Instance

**Effort:** ~8 hours
**Priority:** High
**Files:** `internal/workflow/create_ami.go`

### Work Items

- Pick an instance (running or stopped)
- Gather attached volume info (sizes, prior-snapshot detection) and show
  the volume-size time estimate table (see `DESIGN.md`, "Domain Knowledge
  Carried Forward")
- If running: show Invenio RDM (Postgres/OpenSearch/Redis/Docker)
  crash-consistency guidance; offer an SSM fstrim pass (skip cleanly, not
  an error, if SSM is unavailable on the instance)
- Collect AMI name (default suggestion:
  `<instance-name-or-id>-copy-<date>`, user may override), description,
  no-reboot flag (running instances only), tags — `Project` defaults to
  the source instance's `Project` tag if set; `Environment` is always an
  explicit prompt, no default
- Call `ec2.CreateImage`
- Poll `DescribeImages` unboundedly until `available` or `failed`
  (large Invenio RDM volumes: 20–60+ minutes) — no fixed timeout, matching
  the fix already made in the Bash version for this same reason
- Build the `TagSpecifications` request field as a typed SDK struct, not a
  hand-built string — this is the exact bug class (malformed AWS CLI
  shorthand for tags) that broke the Bash version in real use
- Structure as: build a resolved `CreateAMIParams` struct → reusable
  confirmation/dry-run gate → execute against `ec2.CreateImage` (see
  `DECISIONS.md`, "Structure workflows for future record/replay")

**Tests:** volume-info gathering; SSM-unavailable path; fstrim
success/decline; AMI-name default generation and validation; create
success/failure; unbounded-poll transitions (available/failed) with a
fake clock or call-count-based fake

**Dependency:** Phase 3

---

## Phase 11 — Remove AMI

**Effort:** ~6 hours
**Priority:** High
**Files:** `internal/workflow/remove_ami.go`

### Work Items

- Pick an owned AMI
- Dry-run display: what would be deleted
- Dependency check: list instances currently referencing this AMI's
  `ImageId`
- Environment check: if the AMI's `Environment` tag is `production`, show
  an additional explicit warning before type-to-confirm (see
  `DECISIONS.md` "Introduce a light Project/Environment tagging
  convention")
- Type-to-confirm: user must type the AMI ID or name exactly
- Call `ec2.DeregisterImage`
- Structure as: build a resolved `RemoveAMIParams` struct → reusable
  confirmation/dry-run gate → execute against `ec2.DeregisterImage` (see
  `DECISIONS.md`, "Structure workflows for future record/replay")

**Tests:** dry-run display; dependency detection (AMI in use / not in use);
production-tag warning shown/not-shown; type-to-confirm match/mismatch;
removal success/failure

**Dependency:** Phase 3

---

## Phase 12 — Show/Export Cloud-Init

**Effort:** ~8 hours
**Priority:** High
**Files:** `internal/workflow/cloud_init.go`

### Work Items

- Pick an instance or an AMI (either is a valid target)
- **Instance path**: call `ec2.DescribeInstanceAttribute` (attribute
  `userData`), base64-decode, display; report plainly (not as an error) if
  no user-data was set
- **AMI path** — costs real time/money, so requires its own explicit
  confirmation before proceeding (see `DESIGN.md` Security
  Considerations):
  1. Launch the smallest available instance type from the AMI, tagged to
     mark it disposable (e.g. `Purpose=cloud-init-extraction`)
  2. Poll until `running` and SSM reports `Online`, with a **bounded**
     timeout (this is a diagnostic side-operation, not core creation —
     unlike Phase 10's unbounded poll, it should fail cleanly rather than
     wait indefinitely)
  3. `ssm.SendCommand` to read `/var/lib/cloud/instance/user-data.txt`,
     then `ssm.GetCommandInvocation` for the output
  4. **Always** call `ec2.TerminateInstances` on the temporary instance
     afterward — including when SSM never comes online or the command
     fails. This must be structured so cleanup cannot be skipped by an
     early return (Go's `defer`, or an equivalent explicit cleanup path
     covered by tests)
- The AMI path's launch-confirmation step uses the same reusable
  confirmation gate used throughout this project's workflows, not a
  one-off implementation (see `DECISIONS.md`, "Structure workflows for
  future record/replay")
- **Export**: prompt for a local file path and write the decoded YAML
  there, for manual comparison against a local clone of
  `caltechlibrary/cloud-init-examples`. No inline fetch-and-diff against
  the GitHub repo in v1 (see `DECISIONS.md`, "Add Show/Export Cloud-Init
  as a v1 primitive", and the Deferred section below)

**Tests:** instance-path success; instance-path no-user-data-set;
AMI-path full happy path with a fake SSM client; AMI-path
cleanup-on-SSM-timeout (assert `TerminateInstances` is still called);
AMI-path cleanup-on-command-failure; export-to-file success and
path-error handling

**Dependency:** Phase 1, Phase 2, Phase 3

---

## Phase 13 — Backup Archive & Trim

**Effort:** ~10 hours
**Priority:** High
**Files:** `internal/workflow/backup_archive.go`

### Work Items

- Pick an instance
- Prompt for the backup directory and an age threshold in days — both
  explicit, no baked-in default (same reasoning as the `Environment` tag
  having no default: force a deliberate choice)
- **Dry-run list** (SSM, read-only): candidate files matching the age
  threshold, with size and age, shown before anything else happens
- **Type-to-confirm** before proceeding — same safety tier as Phase 11's
  (Remove AMI) destructive-action pattern
- **Upload phase** (SSM): instance uploads each candidate file to S3 via
  its own AWS CLI/credentials, reports back a small per-file JSON summary
  (S3 key, size). Nothing deleted yet
- **Independent verification**: the tool's own `S3API.HeadObject` (Phase
  1), using the operator's credentials, confirms each uploaded key exists
  with the expected size — this, not the instance's self-report, is what
  authorizes deletion
- **Delete phase** (a *second*, separate SSM command): instance deletes
  exactly the tool-verified file list, not a re-derived one
- **fstrim** (reuse Phase 10's SSM fstrim mechanism), then report bytes
  freed and any files that failed verification (left untouched)
- Structure as: build a resolved `BackupArchiveParams` struct (directory,
  age threshold, bucket) → reusable confirmation/dry-run gate → execute
  the upload/verify/delete/fstrim sequence (see `DECISIONS.md`, "Structure
  workflows for future record/replay")

**Tests:** dry-run empty result; dry-run with matches; type-to-confirm
mismatch; full happy path (all files verified, deleted, fstrim runs) with
fake `EC2API`/`SSMAPI`/`S3API`; partial-verification-failure path (only
verified files deleted, failures reported, fstrim still runs on whatever
was freed); SSM-unavailable path

**Dependency:** Phase 1 (including the `S3API`), Phase 2, Phase 3. Real-AWS
verification (Phase 16) additionally depends on Phase 4, since the live
test target is a throwaway instance launched from an existing AMI that
already has these backups baked in — never tested directly against a
production instance.

---

## Phase 14 — Main Menu and Integration

**Effort:** ~4 hours
**Priority:** High
**Files:** `cmd/awsops/main.go`, `internal/workflow/menu.go`

### Work Items

- `ShowMainMenu` — header, live instance/AMI listings (Phase 2 + 3),
  12-option menu, input validation loop
- Main loop: dispatch to Phase 4/5/6/7/8/9/10/11/12/13 workflows, refresh
  listings after each state-changing operation, handle `Ctrl+C`
  (`os/signal`) for a clean exit
- Wire real AWS clients (Phase 1) into the workflows at startup

**Tests:** menu navigation and dispatch (fake workflows); refresh-after-
operation; clean exit; signal handling

**Dependency:** Phase 4, 5, 6, 7, 8, 9, 10, 11, 12, 13

---

## Phase 15 — Polish and Error Handling

**Effort:** ~4 hours
**Priority:** Medium

### Work Items

- [ ] Loading indicators for long operations (AMI creation polling,
      cloud-init AMI extraction, backup archive upload/verify)
- [ ] Color output for state (running=green, stopped/failed=red, etc.),
      with a `NO_COLOR`/non-TTY fallback
- [ ] Actionable error messages (unwrap AWS SDK errors to their API error
      code, not just the raw Go error string)
- [ ] Input validation for all prompts
- [ ] Context-based timeouts for AWS calls (`context.WithTimeout`)
- [ ] Pagination for large lists (>50 items)

**Dependency:** Phase 14

---

## Phase 16 — Testing

**Effort:** ~6 hours
**Priority:** High

### Work Items

- [ ] `go test ./...` covers all packages; target meaningful coverage on
      `internal/workflow` (the highest-risk, most-interactive code)
- [ ] Fakes for `EC2API`/`SSMAPI`/`S3API` covering error scenarios
      (throttling, access denied, not-found) — no real AWS calls in unit
      tests
- [ ] `TEST_PLAN_REAL_AWS.txt` run manually against a real AWS account,
      all four regions, covering create-instance, create-instance-from-
      cloud-init-YAML, create-AMI, tag management (add/update/remove,
      instance and AMI), start/stop/terminate (including the
      `DeleteOnTermination` warning), both Show/Export Cloud-Init paths
      (including verifying the temporary instance from the AMI path is
      actually terminated), and Backup Archive & Trim (against a
      throwaway instance launched from an existing AMI with real backups
      baked in — not production; requires the S3 bucket and target
      instance profile to exist first)
- [ ] Update `TEST_PLAN_REAL_AWS.txt` if the Go CLI's exact prompts/flow
      differ from the Bash version's

**Dependency:** Phase 14

---

## Phase 17 — Documentation and Bash Retirement

**Effort:** ~2 hours
**Priority:** Medium

### Work Items

- [ ] `README.md`: overview, prerequisites (Go toolchain removed for end
      users — ship a built binary), installation, usage, examples
- [ ] Update `DESIGN.md`/`DECISIONS.md`/`PLAN.md` with any changes made
      during implementation
- [ ] Once Phase 16's real-AWS verification passes: retire
      `ec2_ami_manager.bash`, `ami_copy.bash`, `ami_copy_basic_steps.md`,
      and the `tests/*.bats` suite (record the retirement as a new
      `DECISIONS.md` entry, per this project's existing retire-after-verify
      pattern)

**Dependency:** Phase 16

---

## Deferred to a Later Version (Phase 18+, not scheduled)

Not part of v1 — see `DECISIONS.md`, "V1 scope: ship the four primitives
first, defer composite workflows", "Add Show/Export Cloud-Init as a v1
primitive", "Add Backup Archive & Trim as a v1 primitive", "Add Rename
Instance as a v1 primitive; AMI Name is immutable", "Add Create EC2
Instance from Cloud-Init YAML as a v1 primitive", "Add Start/Stop/
Terminate EC2 Instance as v1 primitives", "Structure workflows for future
record/replay", and `DESIGN.md`, "Deferred to a Later Version". Recorded
here so they're scheduled deliberately once Phase 16 passes, not lost:

- **Recorded Scripts ("session playbooks")** — capture an interactive
  session's actions as an editable, templated YAML script and replay it
  later, with the same confirmation gates as interactive mode always
  enforced (never bypassable for `Environment=production`). Phases
  4-13 are already structured (params-struct/confirm-gate seam) to
  support this without rework once it's built
- **Clone instance for testing**, **Upgrade with rollback point**, and
  **Bake AMI from cloud-init** — composite sequences built from v1's
  primitives (Phase 4 + Phase 10, plus Phase 12's SSM/poll/cleanup pattern
  for the cloud-init case). Once Recorded Scripts exist, these likely
  become example saved scripts rather than bespoke Go workflows
- **Inline diff against `cloud-init-examples`** — fetch a comparison file
  from the GitHub repo and show a unified diff in the tool, instead of
  Phase 12's export-then-manual-diff. Deferred until the repo's files have
  a clear mapping to this account's `Project` tag values
- **Edit AMI Description** — an AMI's `Name` is immutable, but
  `Description` can be changed after creation via `ec2.ModifyImageAttribute`.
  The closest thing to a "rename" AWS actually allows for an AMI. Not
  requested for v1; noted here so it isn't lost (see `DECISIONS.md`, "Add
  Rename Instance as a v1 primitive; AMI Name is immutable")

---

## Priority Order for Implementation

| Phase | Priority | Effort | Dependencies |
|-------|----------|--------|---------------|
| Phase 0 | High | 2h | None |
| Phase 1 | High | 3h | Phase 0 |
| Phase 2 | High | 4h | Phase 1 |
| Phase 3 | High | 4h | Phase 2 |
| Phase 4 | High | 6h | Phase 1, 3 |
| Phase 5 | Medium | 2h | Phase 4 |
| Phase 6 | Medium | 2h | Phase 3 |
| Phase 7 | Medium | 2h | Phase 3 |
| Phase 8 | High | 6h | Phase 3 |
| Phase 9 | Medium | 4h | Phase 3 |
| Phase 10 | High | 8h | Phase 3 |
| Phase 11 | High | 6h | Phase 3 |
| Phase 12 | High | 8h | Phase 1, 2, 3 |
| Phase 13 | High | 10h | Phase 1, 2, 3 |
| Phase 14 | High | 4h | Phase 4, 5, 6, 7, 8, 9, 10, 11, 12, 13 |
| Phase 15 | Medium | 4h | Phase 14 |
| Phase 16 | High | 6h | Phase 14 |
| Phase 17 | Medium | 2h | Phase 16 |
| Phase 18+ | Deferred | — | Phase 16 (see above) |
