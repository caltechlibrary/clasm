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

- [x] `go mod init` for the module (name TBD — matches repo import path)
- [x] Add `github.com/aws/aws-sdk-go-v2` and its `ec2`, `ssm`, `s3`, `sts`
      submodules, plus the config module for credential resolution
- [x] Create `cmd/awsops/main.go` stub (prints version, exits)
- [x] Create `internal/{awsclient,inventory,ui,workflow}` package skeletons
- [x] Confirm `go build ./...` and `go vet ./...` are clean on the empty
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

## Phase 7 — Stop EC2 Instance (done)

**Effort:** ~2 hours
**Priority:** Medium
**Files:** `internal/workflow/power_state.go`

`WaitUntilRunning` (Phase 4) was refactored into a shared `waitUntilState`
helper in `launch_execute.go` (confirmed behavior-preserving against
Phase 4/6's existing tests first), so `WaitUntilStopped` here is a
one-line wrapper rather than duplicated polling logic.

### Work Items

- Pick a running instance
- Confirm (simple yes/no — reversible, unlike Phase 8's terminate)
- Call `ec2.StopInstances`
- Poll `DescribeInstances` until `stopped` (bounded timeout)

**Tests:** stop success/failure; poll-until-stopped with a fake state
transition; poll timeout path; confirmation decline (no API call made)

**Dependency:** Phase 3

---

## Phase 8 — Terminate EC2 Instance (done)

**Effort:** ~6 hours
**Priority:** High
**Files:** `internal/workflow/terminate_instance.go`; added `ConfirmDestructive`
to `confirm.go` -- the heavier type-to-confirm gate `DECISIONS.md`'s
"Structure workflows for future record/replay" anticipated. Single-shot
(no retry loop on mismatch), matching `ec2_ami_manager.bash`'s
`type_to_confirm`; accepts either the instance ID or Name (DESIGN.md says
"ID or name," though the Bash version's actual `remove_ami` call site
only ever passed the ID -- Go version honors the doc's stated intent).

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

## Phase 9 — Manage Tags (done)

**Effort:** ~4 hours
**Priority:** Medium
**Files:** `internal/workflow/manage_tags.go`

Current tags are fetched fresh via `ec2:DescribeInstances`/`DescribeImages`
(both already return the full `Tags` list) rather than a separate
`ec2:DescribeTags` call. `TagChangeParams` stays minimal (resource ID,
action, key, value), matching Phase 8's `TerminateInstanceParams`
precedent for the params-struct/confirm-gate seam.

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

## Phase 10 — Create AMI from EC2 Instance (done)

**Effort:** ~8 hours
**Priority:** High
**Files:** `internal/workflow/{volume_info,fstrim,create_ami_execute,create_ami_from_instance}.go`

`ec2:DescribeVolumes` was missing from both the `EC2API` interface and
DESIGN.md's Assumptions list -- added to both once this phase's volume-
info gathering needed it. `isSSMOnline` is a single-shot check (not
Phase 4's poll-based `WaitForSSMOnline`), matching
`ec2_ami_manager.bash`'s `check_ssm_availability` for an instance that's
presumably been running a while already. `WaitForAMIAvailable` has no
internal timeout at all (unlike every other poll in this codebase) --
only the caller's `ctx` can end it -- since the Bash version's fixed
600-second timeout for this exact operation was itself a correctness bug
(DECISIONS.md, 2026-06-30).

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

## Phase 11 — Remove AMI (done)

**Effort:** ~6 hours
**Priority:** High
**Files:** `internal/workflow/remove_ami.go`

The dependency check (`instancesUsingAMI`) filters the already-fetched
inventory listing by `ImageID` rather than making a fresh AWS call --
Phase 2's `ListInstances` already carries each instance's `ImageID`.
Reuses `ConfirmDestructive` from Phase 8 unchanged.

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

## Phase 12 — Show/Export Cloud-Init (done)

**Effort:** ~8 hours
**Priority:** High
**Files:** `internal/workflow/{cloud_init_instance,cloud_init_ami,cloud_init_export,show_cloud_init}.go`

The always-terminate guarantee is a `defer` against a cleanup-scoped
`context.WithTimeout(context.Background(), 30s)`, deliberately decoupled
from the caller's `ctx` so cleanup isn't skipped by an early return *or*
by `ctx` itself being cancelled. Verified with dedicated tests asserting
`TerminateInstances` is called exactly once across every failure path
(SSM never online, command fails, instance never reaches running) and
exactly zero times when launch itself fails (nothing to clean up).

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

## Phase 13 — Backup Archive & Trim (done, unit tests only)

**Effort:** ~10 hours
**Priority:** High
**Files:** `internal/workflow/{backup_list,backup_upload,backup_verify,backup_delete,backup_archive}.go`

Age filtering happens in Go (`FilterByAge`), not in the remote `find`
command -- the SSM command only lists every file's size/mtime; all
threshold logic is local and independently testable, avoiding fragile
shell-arithmetic date math. Building the remote upload/delete scripts
reintroduces the shell-quoting risk category this whole rewrite exists
to eliminate for *local* command construction -- SSM's API only accepts
a command string, so it's unavoidable here. Caught a real quoting bug in
review: the upload script's S3 destination URI and echoed key were
unquoted (only the source path was), which would have broken on any
filename containing a single quote -- fixed by routing every dynamic
value through `shellQuote` and passing it as a separate `printf`
argument rather than interpolating into a double-quoted string.

Real-AWS verification remains blocked on the S3 bucket and target
instance IAM policy the user still needs to set up outside this repo
(per `DESIGN.md` Assumptions #3-4) -- flagged rather than guessed at.

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

## Phase 14 — Main Menu and Integration (done)

**Effort:** ~4 hours
**Priority:** High
**Files:** `cmd/awsops/main.go`, `internal/workflow/menu.go`

Closed an architectural gap Phases 4-13 deliberately deferred here: each
of the ten orchestrators originally took a single-region `EC2API`/
`SSMAPI`, but Phase 2 aggregates instances/AMIs across all four regions,
so the right client isn't known until *after* a resource is picked
inside the orchestrator. Fixed by changing each orchestrator to take
per-region client maps and resolve (`internal/workflow/region_clients.go`)
by the picked resource's `Region` field immediately after the pick --
touched all ten orchestrator files and their tests, verified
build/vet/race clean after each one before moving to the next.

`menu.go`'s `MenuActions` struct of `func(ctx) error` closures (bound by
`main.go` to live clients and a mutable instance/AMI snapshot) lets menu
dispatch itself be tested with fakes, without driving any workflow's full
interactive prompt sequence. Ctrl+C between prompts cancels `ctx` via
`signal.NotifyContext` (every poll loop already selects on `ctx.Done()`);
Ctrl+C/Ctrl+D *during* an active prompt surfaces as `termlib.ErrInterrupted`/
`io.EOF` instead, handled the same way in the menu loop.

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

## Phase 15 — Polish and Error Handling (done)

**Effort:** ~4 hours
**Priority:** Medium

### Work Items

- [x] Loading indicators for long operations (AMI creation polling,
      cloud-init AMI extraction, backup archive upload/verify) --
      `progress_ticker.go`'s `startProgressTicker`, a goroutine-backed
      periodic status line whose `stop()` blocks until the goroutine has
      fully exited (no race with the caller's next write)
- [x] Color output for state (running=green, stopped/failed=red, etc.),
      with a `NO_COLOR`/non-TTY fallback -- `ui.ColorEnabled()` (checked
      once at startup in `main.go`) + `DisplayInstances`'s new
      `colorEnabled` parameter; `DisplayImages` needed no change since
      AMIs carry no varying state after Phase 2's `available`-only filter
- [x] Actionable error messages (unwrap AWS SDK errors to their API error
      code, not just the raw Go error string) -- `formatError`, wired into
      `menu.go`'s two error-display sites
- [x] Input validation for all prompts -- audited every prompt; added
      validators for previously-unvalidated required fields (KeyName,
      SecurityGroupIDs-at-least-one, SubnetID, Name tag in both launch
      flows; the AMI name's AWS character/length constraint; a non-empty
      tag key on Manage Tags' Add action)
- [x] Context-based timeouts for AWS calls (`context.WithTimeout`) --
      `call_timeout.go`'s `withCallTimeout` (30s), applied to every
      one-shot (non-polling) AWS call across ~13 call sites; polling
      functions already bound themselves via their own timeout parameter
- [x] Pagination for large lists (>50 items) -- rewrote `ui.PickList` to
      paginate (`'n'`/`'p'` navigation) once `len(items)` exceeds the
      50-item page size; selection numbers stay global across pages so a
      page boundary never changes what number picks a given item

**Dependency:** Phase 14

---

## Phase 15.1 — Debug Logging (-debug) (done)

**Effort:** ~2 hours
**Priority:** Medium

### Work Items

- [x] `internal/debuglog`: nil-receiver-safe `*DebugLog` type
      (`Log`/`Path`/`Close`), JSONL sink, timestamped default path
      (see DESIGN.md, "Debug Logging"; DECISIONS.md, "Add -debug: a
      JSONL log of every AWS SDK call")
- [x] `internal/awsclient`: `WrapEC2`/`WrapSSM`/`WrapS3`/`WrapSTS`
      logging decorators over `EC2API`/`SSMAPI`/`S3API`/`STSAPI`, all
      four built on one shared generic helper (`logAWSCall`); each
      returns the client unwrapped when the debug log is nil
- [x] `cmd/awsops`: `-debug` flag, wraps every client right after
      construction, prints the log's path to stderr once at startup
- [x] `helptext.go`/`awsops.1.md`: document `-debug` in OPTIONS

**Dependency:** Phase 1 (AWS Client Layer)

---

## Phase 15.2 — Create Key Pair inline (done)

**Effort:** ~1.5 hours
**Priority:** Medium

### Work Items

- [x] `internal/awsclient`: `EC2API.CreateKeyPair`; `-debug` logging
      wrapper redacts `KeyMaterial` instead of using the shared
      `logAWSCall` helper (see DESIGN.md, "Debug Logging"; DECISIONS.md,
      "Support creating a new key pair from within awsops")
- [x] `internal/workflow/create_key_pair.go`: `createKeyPair` (calls
      `ec2:CreateKeyPair`, saves the private key to `~/.ssh/<name>.pem`
      at `0600`) and `promptKeyPairNameOrCreate` (typing `new` at the
      Key pair name prompt switches into the create-a-new-one sub-flow;
      a name collision re-prompts, any other error propagates)
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: both launch
      flows' Key pair name prompt goes through
      `promptKeyPairNameOrCreate` instead of a plain `ui.Prompt`

**Dependency:** Phase 4 (Create EC2 Instance from AMI)

---

## Phase 15.3 — IAM Instance Profile pick-or-create (done)

**Effort:** ~2 hours
**Priority:** Medium

Real-AWS testing hit `InvalidParameterValue: ... Invalid IAM Instance
Profile name` at launch -- see DECISIONS.md, "Support picking or
creating an IAM instance profile from within awsops".

### Work Items

- [x] `internal/awsclient`: new `IAMAPI` interface (`ListInstanceProfiles`,
      `ListRoles`, `CreateInstanceProfile`, `AddRoleToInstanceProfile`),
      `NewIAMClient` (single global client, like STS), `WrapIAM` (`-debug`
      logging via the shared `logAWSCall` helper -- no special redaction
      needed, unlike `CreateKeyPair`)
- [x] `internal/workflow/resource_lists.go`: `InstanceProfileInfo`/
      `listInstanceProfiles`, `RoleInfo`/`listRoles`
- [x] `internal/workflow/create_instance_profile.go`:
      `promptIAMInstanceProfileOrCreate` (pick list + "(none)" +
      "Create new instance profile (attach an existing role)", always
      offered even when the list is empty; falls back to free text only
      if the list call itself errors), `createInstanceProfileInteractive`
      (pick a role, prompt a name defaulting to the role's name, retry on
      `EntityAlreadyExists`), `createInstanceProfileFromRole`
      (`iam:CreateInstanceProfile` + `iam:AddRoleToInstanceProfile`)
- [x] `launch_instance.go`/`launch_from_cloud_init.go`/
      `create_instance_from_ami.go`/`create_instance_from_cloud_init.go`:
      threaded a new `awsclient.IAMAPI` parameter through; the IAM
      instance profile prompt goes through
      `promptIAMInstanceProfileOrCreate` instead of a plain `ui.Prompt`
- [x] `cmd/awsops/main.go`: constructs one global IAM client (wrapped for
      `-debug`), passed to both Create Instance workflows

**Dependency:** Phase 4 (Create EC2 Instance from AMI)

---

## Phase 15.4 — Derive key pair name from a private key filename/path (done)

**Effort:** ~1 hour
**Priority:** Medium

Real-AWS testing hit `InvalidKeyPair.NotFound` from typing a private key
file path at "Key pair name" -- see DECISIONS.md, "Derive the AWS key
pair name from a private key filename/path".

### Work Items

- [x] `internal/workflow/create_key_pair.go`: `looksLikeKeyFilename`
      (path separator, `~` prefix, or `.pem`/`.ppk`/`.key` extension),
      `keyPairNameFromFilePath` (expands `~`, falls back to checking
      `keyDir` for a bare filename, derives the name from the basename),
      `isReadableFile`
- [x] `promptKeyPairNameOrCreate`: recognized key-filename input is
      validated as readable and derived into an AWS key pair name
      instead of being sent to AWS as-is; an unreadable path re-prompts
      with a local error. Existing "new" sub-flow extracted unchanged
      into `createNewKeyPairInteractive` so the outer loop could add
      this without duplicating retry logic

**Dependency:** Phase 15.2 (Create Key Pair inline)

---

## Phase 15.5 — Pre-flight check: instance type vs. subnet Availability Zone (done)

**Effort:** ~2 hours
**Priority:** Medium

Real-AWS testing (via the `-debug` log) hit `Unsupported: ... instance
type ... is not supported in ... Availability Zone` -- see DECISIONS.md,
"Pre-flight check: instance type vs. subnet Availability Zone".

### Work Items

- [x] `internal/awsclient`: `EC2API.DescribeInstanceTypeOfferings`;
      `-debug` logging wrapper via the shared `logAWSCall` helper
- [x] `internal/workflow/instance_type_az_check.go`:
      `instanceTypeOfferedInAZ`, `instanceTypeOfferedAZs`,
      `ensureInstanceTypeSupportedInSubnet` (pick list: change instance
      type / pick a different subnet / abort -- "abort" returns
      `ui.ErrCancelled`, reusing the existing cancellation path)
- [x] `promptSubnetID` (`launch_prompts.go`): return type changed from
      `(string, error)` to `(SubnetInfo, error)` so the picked subnet's
      Availability Zone is available without a redundant lookup; the
      free-text fallback path returns an empty `AvailabilityZone`
      ("unknown, skip the check")
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: call
      `ensureInstanceTypeSupportedInSubnet` right after the subnet is
      picked, using its (possibly updated) instance type and subnet for
      the rest of the flow

**Dependency:** Phase 4 (Create EC2 Instance from AMI)

---

## Phase 15.6 — Instance type: curated pick list (done)

**Effort:** ~1 hour
**Priority:** Medium

See DECISIONS.md, "Instance type pick list: curated shortlist, not the
full AWS catalog".

### Work Items

- [x] `internal/workflow/launch_prompts.go`: `curatedInstanceTypes`
      (~11 hand-picked types with vCPU/memory labels, including
      t2.micro/t2.medium as the only non-ENA-required entries -- see
      DECISIONS.md, "Add non-ENA-required options to the curated
      instance type list"), `instanceTypeChoice`/`instanceTypeChoiceLabel`,
      `promptInstanceType` (pick list + "Other" free-text fallback)
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: "Instance type"
      prompt goes through `promptInstanceType` instead of a plain
      `ui.Prompt`
- [x] `instance_type_az_check.go`'s "Change instance type" recovery step
      also goes through `promptInstanceType`, for consistency

**Dependency:** Phase 4 (Create EC2 Instance from AMI)

---

## Phase 15.7 — Pre-flight check: instance type vs. AMI ENA support (done)

**Effort:** ~2 hours
**Priority:** Medium

Real-AWS testing hit `InvalidParameterCombination: Enhanced networking
... is required for the '<type>' instance type` -- see DECISIONS.md,
"Pre-flight check: instance type vs. AMI ENA support". Closes the
ENA item queued in TODO.md since an earlier session.

### Work Items

- [x] `internal/inventory/images.go`: `Image.EnaSupport bool`, populated
      from the existing `DescribeImages` call (no new AWS call)
- [x] `internal/awsclient`: `EC2API.DescribeInstanceTypes`; `-debug`
      logging wrapper via the shared `logAWSCall` helper
- [x] `internal/workflow/instance_type_ena_check.go`:
      `instanceTypeRequiresENA`, `ensureInstanceTypeENACompatible`
      (pick list: change instance type / abort -- no "pick a different
      AMI" option, unlike the AZ check's "pick a different subnet")
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: call
      `ensureInstanceTypeENACompatible` right after the instance type is
      picked, using the already-known AMI's `EnaSupport`
- [x] Incompatibility message names `t2.micro`/`t2.medium` explicitly
      and points at the out-of-scope permanent fix (enable ENA on the
      source instance, re-create the AMI) -- see DECISIONS.md, "Add
      non-ENA-required options to the curated instance type list"
      (real-world use showed every curated type failing this check for
      a legacy AMI, with no way to recover without already knowing an
      answer outside awsops)

**Dependency:** Phase 4 (Create EC2 Instance from AMI), Phase 15.6
(reuses `promptInstanceType`)

---

## Phase 15.8 — Tolerate post-launch/post-create eventual-consistency windows (done)

**Effort:** ~1 hour
**Priority:** High

A real, confirmed launch failed immediately after `RunInstances`
succeeded -- see DECISIONS.md, "Tolerate DescribeInstances' post-
RunInstances eventual-consistency window". Not an edge case: a
near-certain race on every launch that happened to hit AWS's brief
propagation window.

### Work Items

- [x] `internal/workflow/launch_execute.go`: `isInstanceNotYetVisible`;
      `waitUntilState` tolerates `InvalidInstanceID.NotFound` as "not
      visible yet" instead of a hard failure
- [x] `internal/workflow/create_ami_execute.go`: `isImageNotYetVisible`;
      `WaitForAMIAvailable` tolerates the AMI-side analog,
      `InvalidAMIID.NotFound` -- fixed preemptively, same failure class,
      same code shape, not yet reported but certain to recur

**Dependency:** Phase 4 (Create EC2 Instance from AMI), Phase 5 (Create AMI
from EC2 Instance)

---

## Phase 15.9 — Filter the subnet picker by instance-type Availability Zone support (done)

**Effort:** ~1.5 hours
**Priority:** Medium

Real-AWS testing surfaced repeated instance-type/subnet back-and-forth --
see DECISIONS.md, "Filter the subnet picker by instance-type
Availability Zone support".

### Work Items

- [x] `internal/workflow/instance_type_az_check.go`:
      `filterSubnetsByInstanceTypeAZ` (best-effort narrowing; falls back
      to the unfiltered list on lookup error or if filtering would leave
      zero subnets)
- [x] `promptSubnetID` (`launch_prompts.go`): gained an `instanceType`
      parameter, applies the filter before building its pick list
- [x] `launch_instance.go`/`launch_from_cloud_init.go`/
      `instance_type_az_check.go`'s "Pick a different subnet" branch:
      updated to pass the already-known instance type through
- [x] `ensureInstanceTypeSupportedInSubnet` (Phase 15.5) unchanged --
      remains the safety net for cases filtering can't cover

**Dependency:** Phase 15.5 (Pre-flight check: instance type vs. subnet
Availability Zone)

---

## Phase 15.10 — Move "Show resource lists" to the top of the Compute menu (done)

**Effort:** ~30 minutes
**Priority:** Low

See DECISIONS.md, "Move 'Show resource lists' to the top of the Compute
menu; rename from 'Refresh'".

### Work Items

- [x] `internal/workflow/menu.go`: renamed "Refresh resource lists" to
      "Show resource lists", moved to position 1; `MenuActions.Refresh`
      field name unchanged
- [x] `menu_test.go`: updated every menu-item-number reference
- [x] `DESIGN.md`'s Compute Menu ASCII diagram and
      `TEST_PLAN_REAL_AWS.txt`'s menu-order checklist updated to match,
      preserving existing `[ok]` markers against renamed/renumbered items

**Dependency:** Phase 14 (Main Menu and Integration)

---

## Phase 15.11 — Auto-detect a bare existing-file path in User data / Cloud-init YAML input (done)

**Effort:** ~1 hour
**Priority:** Medium

Real-world use: typing `newt-machine.yaml` (no `@` prefix) at the
Cloud-init YAML prompt silently became the instance's literal user-data
instead of loading the file -- see DECISIONS.md, "Auto-detect a bare
existing-file path in User data / Cloud-init YAML input".

### Work Items

- [x] `internal/workflow/userdata.go`: `loadUserData` gained a
      `*termlib.Terminal` parameter; when input has no `@` prefix but a
      file exists at that exact path, load it anyway with an on-screen
      note, instead of silently using the filename as literal text
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: updated call
      sites (both already had `t` in scope)

**Dependency:** Phase 4 (Create EC2 Instance from AMI), Phase 5 (Create
EC2 Instance from Cloud-Init YAML)

---

## Phase 15.12 — Create EC2 Instance from Cloud-Init YAML always reads from a file (done)

**Effort:** ~1.5 hours
**Priority:** Medium

Follow-up to Phase 15.11: for this specific prompt, "inline text or
@file path" was itself the wrong shape -- see DECISIONS.md, "Create EC2
Instance from Cloud-Init YAML always reads from a file".

### Work Items

- [x] `internal/workflow/userdata.go`: `promptCloudInitYAMLFile` --
      always treats input as a file path (optional leading `@`
      tolerated, not required), re-prompts with a clear error on a
      missing/unreadable file instead of falling back to literal text
- [x] `launch_from_cloud_init.go`: cloud-init prompt now calls
      `promptCloudInitYAMLFile` instead of `ui.Prompt` + `loadUserData`;
      no longer shares `loadUserData` with Feature 2's optional "User
      data" field at all
- [x] `launch_from_cloud_init_test.go`/`create_instance_from_cloud_init_test.go`:
      rewrote every test that exercised this prompt with inline
      `"#cloud-config"` text to use a real temp-file fixture
      (`writeCloudInitFixture` helper) instead; added coverage for the
      leading-`@` tolerance and the retry-on-unreadable-file path

**Dependency:** Phase 15.11 (Auto-detect a bare existing-file path in
User data / Cloud-init YAML input)

---

## Phase 15.13 — Offer official Ubuntu LTS AMIs alongside owned AMIs (done)

**Effort:** ~2 hours
**Priority:** Medium

See DECISIONS.md, "Offer official Ubuntu LTS AMIs alongside owned AMIs
when picking a base AMI".

### Work Items

- [x] `internal/workflow/official_ubuntu_amis.go`: `ubuntuAMIOwnerID`
      constant (Canonical's public AWS account ID), `curatedUbuntuReleases`
      (24.04, 22.04, amd64 only), `latestUbuntuAMI` (most recent match
      per release/region via `ec2:DescribeImages`), `listOfficialUbuntuAMIsInRegion`,
      `listOfficialUbuntuAMIs` (sequential per-region aggregation),
      `imagesWithOfficialUbuntu` (best-effort merge with owned AMIs;
      falls back to owned-only on lookup error)
- [x] `launch_instance.go`/`launch_from_cloud_init.go`: both AMI pick
      lists now call `imagesWithOfficialUbuntu` before display
- [x] `EnaSupport` carried through from the real `DescribeImages`
      response for curated Ubuntu entries, so the ENA pre-flight check
      (Phase 15.7) doesn't false-positive on a modern, actually-ENA-
      enabled official AMI
- [x] Real-AWS testing (via `-debug`) caught a wrong `name` filter
      pattern (missing the `ubuntu/images/hvm-ssd*/` prefix Canonical's
      real AMI names carry) that silently matched zero AMIs in every
      region -- see DECISIONS.md, "Fix official Ubuntu AMI name filter
      pattern"; corrected in both curated release patterns

**Dependency:** Phase 4 (Create EC2 Instance from AMI), Phase 15.6
(curated instance-type list), Phase 15.7 (ENA pre-flight check)

---

## Phase 15.14 — Narrow configured regions to us-west-1/us-west-2 (done)

**Effort:** ~1 hour
**Priority:** High

See DECISIONS.md, "Narrow configured regions to us-west-1/us-west-2" --
follow-up to the official-Ubuntu-AMI feature surfacing a region
(`us-west-1`) with no provisioned key pairs.

### Work Items

- [x] `internal/awsclient/regions.go`, `regions_test.go`: `Regions`
      narrowed from four regions to `{us-west-1, us-west-2}`
- [x] `helptext.go`, `DESIGN.md` (Overview, ASCII diagram, generic
      "four regions" references genericized to avoid re-hardcoding a
      count): updated to match. `awsops.1.md` regenerates from
      `helptext.go` via the existing `cmt`/Makefile pipeline, not edited
      by hand
- [x] `TODO.md`, `TEST_PLAN_REAL_AWS.txt`: active (non-historical)
      "four regions" mentions updated; historical `DECISIONS.md` entries
      describing what was true when originally decided left unchanged

**Dependency:** Phase 1 (region configuration)

---

## Phase 15.15 — Validate key pair name against the AMI's region (done)

**Effort:** ~3 hours
**Priority:** High

A real launch failed with `InvalidKeyPair.NotFound` after every prompt
was already answered and confirmed -- see DECISIONS.md, "Validate key
pair name against the AMI's region".

### Work Items

- [x] `internal/workflow/resource_lists.go`: `listKeyPairs`
      (`ec2:DescribeKeyPairs`)
- [x] `internal/workflow/create_key_pair.go`: `promptKeyPairNameOrCreate`
      rewritten to a region-scoped pick list (existing key pairs +
      "Create new key pair", no "Other" escape hatch -- key pairs are a
      complete, enumerable list, unlike AMIs/instance types); original
      free-text logic (the "new" keyword, key-file-path auto-detection)
      preserved verbatim as `promptKeyPairNameFreeText`, now solely the
      fallback for when `ec2:DescribeKeyPairs` itself errors
- [x] Updated every test exercising the full launch flow with a
      zero-key-pairs fake (`launch_instance_test.go`,
      `launch_from_cloud_init_test.go`, `create_instance_from_ami_test.go`,
      `create_instance_from_cloud_init_test.go`) to select "Create new
      key pair" from the now-shown pick list instead of typing a bare
      name directly

**Dependency:** Phase 4 (Create EC2 Instance from AMI), Phase 15.2
(Create Key Pair inline), Phase 15.4 (key filename/path derivation)

---

## Phase 15.16 — Tolerate GetCommandInvocation's post-SendCommand eventual-consistency window (done)

**Effort:** ~1 hour
**Priority:** High

Third instance of the same eventual-consistency bug pattern found this
session -- see DECISIONS.md, "Tolerate GetCommandInvocation's
post-SendCommand eventual-consistency window".

### Work Items

- [x] `internal/workflow/ssm.go`: `isInvocationNotYetVisible`;
      `RunShellCommand`'s poll loop tolerates `InvocationDoesNotExist` as
      "not visible yet" instead of a hard failure, matching
      `isInstanceNotYetVisible`/`isImageNotYetVisible`'s shape exactly

**Dependency:** Phase 4 (Create EC2 Instance from AMI -- introduced
`RunShellCommand`/the cloud-init completion check)

---

## Phase 15.17 — `~/.awsops` YAML config file (done)

**Effort:** ~2.5 hours
**Priority:** Medium

See DECISIONS.md, "Add a `~/.awsops` YAML config file for awsops' own
operational settings".

### Work Items

- [x] `go get gopkg.in/yaml.v3`
- [x] `internal/config/config.go`: `Config` struct (`Regions []string`,
      `yaml:"regions"`), `DefaultRegions` (`[us-west-1, us-west-2]`),
      `DefaultPath()` (`~/.awsops`, falling back to a cwd-relative
      `.awsops` if the home directory can't be resolved, matching
      `sshKeyDir()`'s existing fallback pattern), `Load(path)` (missing
      file -> defaults, not an error; malformed YAML -> a real error;
      valid file with `regions` unset/empty -> `DefaultRegions`)
- [x] `internal/awsclient/regions.go`/`regions_test.go` removed;
      `client_test.go`'s sanity test now uses a small test-local region
      literal instead of the removed shared var
- [x] `cmd/awsops/main.go`: new `-config` flag (default
      `config.DefaultPath()`), loads config early (fails fast on a parse
      error, matching every other startup failure mode), uses
      `cfg.Regions` everywhere `awsclient.Regions` was read
- [x] `helptext.go`: documents `-config`

**Dependency:** Phase 15.14 (Narrow configured regions to us-west-1/us-west-2)

---

## Phase 15.18 — Highlight PickList's prompt header (done)

**Effort:** ~1 hour
**Priority:** Low

See DECISIONS.md, "Highlight PickList's prompt header when color is
enabled".

### Work Items

- [x] `internal/ui/color.go`: package-level `colorEnabled` flag +
      `SetColorEnabled(bool)` setter; `Highlight(s string) string` wraps
      `s` in `termlib.Bold`/`termlib.Reset` when enabled, else returns
      `s` unchanged
- [x] `internal/ui/picklist.go`: `PickList` prints `Highlight(prompt)` as
      its own line before the numbered list, not just as the trailing
      input query -- so a wrong menu selection is visible before reading
      through the list
- [x] `cmd/awsops/main.go`: `ui.SetColorEnabled(colorEnabled)` alongside
      the existing `ui.ColorEnabled()` call

**Dependency:** Phase 15 (Color output for state -- `ui.ColorEnabled()`,
`NO_COLOR`/non-TTY fallback)

---

## Phase 15.19 — Configure per-instance backup directories by Name pattern (done)

**Effort:** ~1.5 hours
**Priority:** Medium

See DECISIONS.md, "Configure per-instance backup directories by Name
pattern".

### Work Items

- [x] `internal/config/config.go`: `BackupDirectoryRule` struct
      (`Pattern`, `Directory`, both `string`), `Config.BackupDirectories
      []BackupDirectoryRule` (`yaml:"backup_directories"`),
      `BackupDirectoryFor(rules, instanceName) string` (first
      `path.Match` hit wins, in list order; "" for no match or an empty
      instanceName)
- [x] `internal/workflow/backup_archive.go`: `BackupArchiveAndTrim` takes
      a new `backupDirRules []config.BackupDirectoryRule` parameter;
      pre-fills the "Backup directory" prompt via `ui.WithDefault` when
      `config.BackupDirectoryFor` matches, otherwise unchanged (required,
      no default)
- [x] `cmd/awsops/main.go`: passes `cfg.BackupDirectories` through

**Dependency:** Phase 15.17 (`~/.awsops` YAML config file)

---

## Phase 15.20 — Per-file upload progress for Backup Archive & Trim (done)

**Effort:** ~1.5 hours
**Priority:** Medium

See DECISIONS.md, "Per-file upload progress for Backup Archive & Trim".

### Work Items

- [x] `internal/workflow/backup_upload.go`: `UploadProgress` struct
      (`Done`, `Total`, `BytesDone`, `BytesTotal`, `Result`);
      `UploadBackupFiles` runs one `ssm:SendCommand` per file (was one
      batched script for the whole list) and takes a new
      `onProgress func(UploadProgress)` parameter (nil-safe), invoked
      after each file completes
- [x] `internal/workflow/backup_archive.go`: `formatBytes` helper
      (human-scaled sizes, e.g. "1.2 GiB"); upload phase now prints
      "... uploading N/M (bytes of total) - OK/FAIL key" per file via
      the new callback, replacing the generic 30s heartbeat ticker for
      this phase only (the verify phase keeps its existing heartbeat)

**Dependency:** Phase 11 (Backup Archive & Trim)

---

## Phase 15.21 — Preflight check: S3 bucket access before Backup Archive & Trim's dry-run list (done)

**Effort:** ~1 hour
**Priority:** Medium

See DECISIONS.md, "Preflight check: S3 bucket access before Backup
Archive & Trim's dry-run list".

### Work Items

- [x] `internal/awsclient/s3.go`: `S3API` gains `HeadBucket`; real client
      (via the SDK), `internal/awsclient/logging_s3.go`'s decorator, and
      the test fake all implement it
- [x] `internal/workflow/backup_verify.go`: `CheckS3BucketAccess(ctx,
      client, bucket) error` -- `s3:HeadBucket`, wraps any error with the
      bucket name and a permissions hint
- [x] `internal/workflow/backup_archive.go`: `BackupArchiveAndTrim` calls
      `CheckS3BucketAccess` immediately after the "S3 bucket" prompt,
      aborting before the dry-run list, type-to-confirm, or upload

**Dependency:** Phase 11 (Backup Archive & Trim)

---

## Phase 15.22 — Resolve a bucket's actual region before Backup Archive & Trim's access check (done)

**Effort:** ~1.5 hours
**Priority:** High

Real-AWS regression found immediately after Phase 15.21 shipped -- see
DECISIONS.md, "Resolve a bucket's actual region before Backup Archive &
Trim's access check".

### Work Items

- [x] `internal/awsclient/s3.go`: `S3API` gains `GetBucketLocation`; real
      client, `internal/awsclient/logging_s3.go`'s decorator, and the
      test fake all implement it
- [x] `internal/workflow/backup_verify.go`: `BucketRegion(ctx, client,
      bucket) (string, error)` -- `s3:GetBucketLocation`, mapping ""
      -> "us-east-1" and the legacy "EU" -> "eu-west-1"
- [x] `internal/workflow/backup_archive.go`: `BackupArchiveAndTrim` gains
      a `newS3Client func(ctx, region) (awsclient.S3API, error)`
      parameter; after the "S3 bucket" prompt, calls `BucketRegion` on
      the original `s3Client`, then `newS3Client` to build a
      region-scoped client used for `CheckS3BucketAccess` and every
      later `s3:HeadObject` verification call
- [x] `cmd/awsops/main.go`: supplies the `newS3Client` factory closure
      alongside the original probe `s3Client`

**Dependency:** Phase 15.21 (Preflight check: S3 bucket access)

---

## Phase 15.23 — Namespace backup uploads by instance (done)

**Effort:** ~1 hour
**Priority:** Medium

See DECISIONS.md, "Namespace backup uploads by instance".

### Work Items

- [x] `internal/workflow/backup_upload.go`: `uploadKey(prefix, filePath)
      string` (`path.Join`, prefix dropped if empty); `buildUploadCommand`
      and `UploadBackupFiles` both gain a `prefix` parameter, used for
      every destination key
- [x] `internal/workflow/backup_archive.go`: `BackupArchiveAndTrim`
      derives the prefix from the picked instance's Name tag (falls back
      to its instance ID if blank), passes it to `UploadBackupFiles`, and
      uses the same `uploadKey` to build `pathByKey` for delete
      resolution

**Dependency:** Phase 11 (Backup Archive & Trim)

---

## Phase 15.24 — Show instance IP addresses in the main listing (done)

**Effort:** ~1 hour
**Priority:** Medium

See DECISIONS.md, "Show instance IP addresses in the main listing".

### Work Items

- [x] `internal/inventory/instances.go`: `Instance` gains
      `PublicIP`/`PrivateIP` (from the same `DescribeInstances` response
      already fetched); `instanceFromSDK` populates both
- [x] `internal/ui/display.go`: `orNone` helper ("none" for a blank IP,
      distinct from `orUnknown`'s "unknown" for untagged Project/
      Environment); `DisplayInstances` adds "PUBLIC IP"/"PRIVATE IP" as
      two new trailing columns

**Dependency:** Phase 1 (Unified Resource Listing)

---

## Phase 15.25 — Suppress aws s3 cp's progress output to avoid truncating the OK/FAIL signal (done)

**Effort:** ~1 hour
**Priority:** High

Real-AWS regression found immediately after the IAM permission fix
above -- see DECISIONS.md, "Suppress aws s3 cp's progress output to
avoid truncating the OK/FAIL signal".

### Work Items

- [x] `internal/workflow/backup_upload.go`: `buildUploadCommand`'s
      `aws s3 cp` invocation gains `--only-show-errors`
- [x] `internal/workflow/backup_archive_test.go`: two `ssmCommandResponse`
      substrings updated to match the new command text
      (`"aws s3 cp --only-show-errors '...'"`)

**Dependency:** Phase 15.20 (Per-file upload progress)

---

## Phase 15.26 — Preflight check: AWS CLI availability before Backup Archive & Trim (done)

**Effort:** ~1 hour
**Priority:** High

Hit twice in a row in real-AWS testing (newauthors, then data-new) --
see DECISIONS.md, "Preflight check: AWS CLI availability before Backup
Archive & Trim".

### Work Items

- [x] `internal/workflow/backup_cli_check.go`: `CheckAWSCLIAvailable(ctx,
      client, instanceID, timeout, pollInterval) error` -- `command -v
      aws` via SSM, non-Success status -> a clear, actionable error
      naming the instance
- [x] `internal/workflow/backup_archive.go`: `BackupArchiveAndTrim` calls
      it immediately after picking the instance, before any other
      prompt or the dry-run list
- [x] `internal/workflow/backup_archive_test.go`: five existing fakes'
      `responses` gained a `"command -v aws"` entry; four
      `sendCommandCalls()` count assertions incremented by one

**Dependency:** Phase 11 (Backup Archive & Trim)

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

## Phase 17 — Documentation and Bash Retirement (done)

**Effort:** ~2 hours
**Priority:** Medium

### Work Items

- [x] `README.md`: overview, prerequisites (Go toolchain removed for end
      users — ship a built binary), installation, usage, examples
- [x] Update `DESIGN.md`/`DECISIONS.md`/`PLAN.md` with any changes made
      during implementation
- [x] Once Phase 16's real-AWS verification passes: retire
      `ec2_ami_manager.bash`, `ami_copy.bash`, `ami_copy_basic_steps.md`,
      and the `tests/*.bats` suite (record the retirement as a new
      `DECISIONS.md` entry, per this project's existing retire-after-verify
      pattern) -- done 2026-07-08, see DECISIONS.md, "Retire
      ec2_ami_manager.bash, ami_copy.bash, and the Bash test suite"

**Dependency:** Phase 16

---

## Phase 18 — Domain Picker Refactor

**Effort:** ~4 hours
**Priority:** High
**Files:** `internal/ui/domainmenu.go`, `cmd/awsops/main.go`,
`internal/workflow/menu.go`

Per `DECISIONS.md`, 2026-07-02 "Redesign navigation as a domain picker;
add Key Management, S3, and CloudFront domains". Pure navigation refactor
— no new AWS calls, and Compute's existing workflows and tests are
untouched. Runs alongside Phase 16/17, not blocking or blocked by them.

### Work Items

- New top-level domain picker: Compute / Key Management / S3 /
  CloudFront / Exit (see `DESIGN.md`, "Navigation: Domain Picker")
- Extract Compute's existing "fetch resources → display → numbered menu
  → dispatch → refresh → loop" pattern into a shared
  `internal/ui/domainmenu.go` loop, parameterized by a resource-fetch
  function, a display function, and a menu-item list
- Wire Compute's existing `menu.go` through the new shared loop as the
  first domain; behavior must be identical to today's — this phase adds
  no new Compute-visible behavior
- "Back to domain picker" in every domain menu; "Exit" from inside any
  domain menu exits the whole program, not just that domain
- Stub Key Management/S3/CloudFront picker entries as "not yet
  implemented" placeholders until Phases 19-21 land, so this phase can be
  merged and tested independently of them

**Tests:** domain picker dispatches to the right domain loop; Compute's
existing workflow tests continue to pass unmodified; "Back"/"Exit"
behavior from within a domain menu

**Dependency:** Phase 14 (Main Menu and Integration)

---

## Phase 19 — Key Management Domain (done)

**Effort:** ~6 hours
**Priority:** Medium
**Files:** `internal/inventory/keypairs.go`,
`internal/workflow/{keypair_create,keypair_import,keypair_delete,keymgmt_common,keymgmt_menu}.go`

Implements `DESIGN.md` Features 13-16.

### Work Items

- [x] `ListKeyPairs(ctx)` across the configured regions (`internal/inventory`)
- [x] List Key Pairs display: Name, Region, Type, Fingerprint/Key ID
- [x] Create Key Pair: prompt region + name, `ec2:CreateKeyPair`, save
      private key to `~/.ssh/<name>.pem` at `0600` — reuses Phase 15.2's
      existing `createNewKeyPairInteractive`/`createKeyPair` primitives
      directly (region picked first via a new `promptRegion` helper, then
      delegates to the unchanged inline-flow code) so both call sites
      (this menu entry and Feature 2's inline "type `new`" shortcut)
      share one implementation
- [x] Import Key Pair: prompt region + name + local `.pub` file path,
      validate the file locally before calling AWS (new
      `validatePublicKeyFile`, no prior precedent in this codebase --
      see DECISIONS.md), `ec2:ImportKeyPair`
- [x] Delete Key Pair: pick a key pair, show dependent instances (filter
      the already-fetched `ListInstances` result by the new
      `Instance.KeyName` field — no fresh AWS call, same pattern as
      Phase 11's AMI-dependency check), type-to-confirm, `ec2:DeleteKeyPair`
- [x] Wire into the domain picker from Phase 18 -- Key Management's own
      `refresh` also independently calls `ListInstances` (see
      DECISIONS.md) so Delete Key Pair's dependency check is correct
      regardless of whether Compute was visited first in this run

**Tests:** `internal/inventory/keypairs_test.go`,
`internal/workflow/{keypair_create,keypair_import,keypair_delete,keymgmt_menu}_test.go`
-- fakes for `DescribeKeyPairs`/`CreateKeyPair`/`ImportKeyPair`/`DeleteKeyPair`
covering success, name-collision re-prompt (Create and Import), malformed
public key file, dependent-instance detection, and the menu loop's
dispatch/refresh/error/exit-signal behavior. `go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l .` all clean.

**Tests:** fakes for `DescribeKeyPairs`/`CreateKeyPair`/`ImportKeyPair`/
`DeleteKeyPair` covering success, name-collision re-prompt, malformed
public key file, dependent-instance detection

**Dependency:** Phase 18; reuses Phase 15.2's `CreateKeyPair` wrapper

---

## Phase 20 — S3 Domain (Buckets & Static Websites) (done)

**Effort:** ~16 hours (raised from ~10h after adding Feature 21.1,
2026-07-08 — see DECISIONS.md)
**Priority:** Medium
**Files:** `internal/awsclient/s3.go` (broadened),
`internal/inventory/buckets.go`,
`internal/workflow/{bucket_create,bucket_website,bucket_sync,bucket_browse,bucket_lifecycle,s3_menu}.go`

Implements `DESIGN.md` Features 17-21, 21.1, and the 2026-07-02
"CloudFront + OAC by default for static websites" decision, with scope
decisions made 2026-07-08 before implementation started (see
DECISIONS.md): public-read bucket policy opt-out deferred, key-prefix
filter added to Browse/Manage Objects, and Feature 21.1 (Manage Bucket
Lifecycle Policies) added as new scope with a Purpose-tag-driven
guided/generic split.

### Work Items

- [x] Broaden the `S3API` interface beyond Feature 11's `HeadObject`-only
      scope: `ListBuckets`, `GetBucketWebsite`, `CreateBucket`,
      `PutPublicAccessBlock`, `PutBucketWebsite`, `PutObject`,
      `ListObjectsV2`, `DeleteObject`, `PutBucketTagging`, `GetBucketTagging`,
      `GetBucketLifecycleConfiguration`, `PutBucketLifecycleConfiguration`
      (`GetBucketLocation` already exists; `PutBucketPolicy`/`GetObject` are
      NOT added — the former is only needed by the deferred public-read
      opt-out, the latter isn't needed since object content is never
      downloaded, only `HeadObject` metadata)
- [x] `ListBuckets(ctx)` (`internal/inventory/buckets.go`) with per-bucket
      region (`GetBucketLocation`), static-website-hosting status
      (`GetBucketWebsite`), and `Purpose` tag (`GetBucketTagging`) — all
      three enrichment calls on a region-scoped client via `newClient`,
      never the global client, per the established `MovedPermanently`
      lesson from Backup Archive & Trim — treat `NoSuchWebsiteConfiguration`
      and a missing/absent `Purpose` key (or `NoSuchTagSet`) as "not
      configured"/"untagged," not failures
- [x] Create Bucket: prompt name (validated locally against S3 naming
      rules) + region + purpose (Website/Backup/Internal pick list),
      `s3:CreateBucket`, `s3:PutPublicAccessBlock` (all four settings on),
      then `s3:PutBucketTagging` with `Purpose: <choice>`
- [x] Configure Static Website Hosting: pick bucket, prompt index/error
      documents, `s3:PutBucketWebsite`. **Public-read bucket policy opt-out
      deferred** (DECISIONS.md) — only the default private-bucket path
      ships in this phase; where CloudFront hand-off would go, print that
      CloudFront isn't implemented yet (Phase 21)
- [x] Sync Local Directory to Bucket: dry-run diff (by key + size) against
      the local directory, confirm (plain y/n), upload new/changed
      (`s3:PutObject`, per-file progress line matching Backup Archive &
      Trim's established convention), then a **separate**, stronger
      `ConfirmDestructive` (type the bucket name) gate for bucket-only
      objects (`s3:DeleteObject`) — never bundled into the upload
      confirmation
- [x] Browse/Manage Objects: **optional key-prefix filter added**
      (DECISIONS.md) before listing; paginated object listing (reuse
      Phase 15's PickList pagination for >50 items, unchanged), metadata
      display, per-object delete with a plain yes/no confirm
- [x] **New: Manage Bucket Lifecycle Policies** (`bucket_lifecycle.go`,
      DESIGN.md Feature 21.1): pick a bucket, `s3:GetBucketLifecycleConfiguration`
      (`NoSuchLifecycleConfiguration` = no rules yet, not an error),
      branch on the bucket's `Purpose` tag —
  - `backup`: guided flow, two yes/no-shaped prompts (expire-after-days,
    transition-after-days + a curated storage-class pick list: Standard-IA,
    Intelligent-Tiering, Glacier Flexible Retrieval, Glacier Deep Archive),
    optional key-prefix scope
  - `internal`/`website`/untagged: generic editor — named rules (unique
    ID), optional prefix, zero-or-more transitions from the *full*
    `types.TransitionStorageClass` enum, optional expiration; add/edit/
    remove by ID
  - both paths write the complete modified rule set via
    `s3:PutBucketLifecycleConfiguration` in one call (the API has no
    per-rule operations); rule edit/remove is a plain yes/no confirm,
    with on-screen text noting this schedules *future* automated
    deletion (AWS's own ~24-48h evaluation cadence), not an immediate one
- [x] Wire into the domain picker from Phase 18, following Phase 19's
      `KeyMgmtActions`/`RunKeyMgmtMenu` shape (`S3Actions`/`RunS3Menu` — six
      menu items: Show resource lists, Create Bucket, Configure Static
      Website Hosting, Sync Local Directory to Bucket, Browse/Manage
      Objects, Manage Bucket Lifecycle Policies, Back to domain picker)

**Tests:** `internal/inventory/buckets_test.go`,
`internal/workflow/{bucket_create,bucket_website,bucket_sync,bucket_browse,bucket_lifecycle,s3_menu}_test.go`
-- fakes for each new S3 call covering success/error paths
(bucket-name-taken, website-not-configured treated as non-error, sync
diff correctness, upload/delete confirmations never bundled, prefix
filter narrows the object listing, lifecycle guided-vs-generic branch
selection by `Purpose` tag, rule add/edit/remove round-trips through the
fetch-modify-PutBack cycle correctly) — TDD throughout. `go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l .` all clean.

**Real-AWS verification (2026-07-08):** created one throwaway bucket per
purpose, configured website hosting, ran Sync twice (upload pass, then a
locally-deleted-file pass exercising the separate delete confirm),
browsed with and without a prefix filter (view metadata, delete an
object), set and round-tripped a guided backup lifecycle policy, and
added/edited/removed a named rule via the generic editor -- all against
account 074441911104. Found and fixed one real bug along the way (empty-
`Rules` `PutBucketLifecycleConfiguration` on last-rule removal; see
DECISIONS.md) and left one open, documented gap (no local validation
that transition-days < expiration-days; see TODO.md). All throwaway
buckets and objects cleaned up afterward -- no production bucket was
touched.

**Dependency:** Phase 18

---

## Phase 20.1 — S3 Object Management: Interactive File Manager (huh + bubbletea)

**Status: implemented and unit-tested 2026-07-09 (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean); not yet verified
against real AWS — see Phase 22.** This is the "next release focuses on
improving UX and the UI" work flagged when 0.0.1 shipped (see
DECISIONS.md, "0.0.1 scope: ship on termlib as-is; postpone CloudFront
and the UI/UX overhaul"). Numbered 20.1 (inserted after Phase 20
without renumbering CloudFront's Phases 21-22 — same decimal-insertion
convention already used for Phase 15.1-15.26 and DESIGN.md's Feature
21.1) because it revises Phase 20's S3 domain rather than adding a new
one.

**Effort:** ~24 hours estimated; actual scope grew somewhat during
implementation (extracting `internal/s3diff` and adding a dedicated
Sync action — see below and DECISIONS.md).
**Priority:** High
**Files (as actually built, package boundary resolved during
implementation):**
`internal/awsclient/s3.go`/`logging_s3.go` (added `GetObject` + wrapper),
`internal/workflow/s3_menu.go` (revised menu),
`internal/workflow/object_browser.go` (huh pre-flight, new),
`internal/filemanager/*.go` (the `bubbletea` `Model` — its own package,
not folded into `internal/workflow`, since `internal/workflow` now
depends on it via `object_browser.go` and a same-package dependency the
other way would cycle),
`internal/s3diff/*.go` (new — `diffSync`/`walkLocalTree`/
`listAllBucketObjects`/`contentTypeFor` extracted here, out of the now-
deleted `bucket_sync.go`, so both `internal/workflow` and
`internal/filemanager` can depend on the same diff logic without a
`workflow`<->`filemanager` import cycle),
`internal/workflow/bucket_browse.go` (stripped to just
`listBucketObjectsWithPrefix`, still needed by Delete Bucket's
empty-bucket check) and `internal/workflow/bucket_delete_objects.go`
(deleted) and `internal/workflow/bucket_sync.go` (deleted, superseded
by `internal/s3diff` + the file manager's Sync action).

Implements `DESIGN.md` Features 21.2-21.8.

### Work Items

- [x] Add `s3:GetObject` to the `S3API` interface (`internal/awsclient/s3.go`)
      — Read/Download has been out of scope since Phase 20 ("object
      content is never downloaded"); this phase completes Create/
      Update/Read/Delete parity (DECISIONS.md)
- [x] Session pre-flight on huh: pick bucket + region (`huh.Select`,
      reusing Feature 17's fetched listing), then confirm/prompt an
      optional local directory to link (`huh.Confirm` + `huh.Input`,
      reusing `internal/s3diff.ValidateLocalDirectory`)
- [x] `bubbletea` `Model` for the screen itself: single-pane and
      double-pane layouts, header, per-pane status line (item count,
      tagged count, aggregate tagged size, active filter), command line,
      hotkey legend bar, progress/confirm modal overlay (DESIGN.md 21.4)
- [x] S3 per-directory-level listing via `ListObjectsV2` with
      `Delimiter=/` (`CommonPrefixes` + `Contents`); local per-level
      listing via `os.ReadDir`; independent navigation per pane
      (DESIGN.md 21.5)
- [x] Tagging (`Space`, `*`) and per-pane current-level substring filter
      (`f` / `/`)
- [x] Actions and their confirm gates: Upload (`u`, `s3:PutObject`,
      requires a linked local directory), Download (`d`, `s3:GetObject`),
      Delete (`x`, `s3:DeleteObject`, `ConfirmDestructive`), Show
      metadata (`m`, `s3:HeadObject`) — plain `Confirm` for
      Upload/Download, per-item OK/FAIL progress in the overlay
      (DESIGN.md 21.6)
- [x] Sync action (`S` / `:sync`, added during implementation — see
      DECISIONS.md "Add a dedicated Sync action to the file manager"):
      diffs the entire linked directory against the entire bucket
      (`internal/s3diff.Compute`/`WalkLocalTree`/`ListAllBucketObjects`),
      gated by the same never-bundled Confirm-then-ConfirmDestructive
      two-stage flow the retired wizard used — fulfills Decision 2
      ("Sync's directory-mirroring workflow is kept as a first-class,
      directly reachable capability") more literally than manual
      tag-and-act alone would have.
- [x] Mid-session link/unlink (`l`): prompt a path, split single-pane
      into double-pane or collapse back, without restarting the screen
- [x] Find (`F` / `:find <pattern>`): recursive glob-on-basename search
      from the focused pane's current position (Go stdlib
      `path/filepath.Match`), reusing the same `filepath.WalkDir`
      traversal locally (`listLocalRecursive`) and an on-demand full
      `ListObjectsV2` (no `Delimiter`) on the S3 side
      (`listS3Recursive`); cancellable, live "Searching… (N scanned, M
      matched)" status; `Enter` to jump to a match's location, `Esc` to
      discard results (DESIGN.md 21.7)
- [x] Colon command line (`:upload`, `:download`, `:delete`, `:metadata`,
      `:find <pattern>`, `:link <path>`, `:sync`, `:quit`) dispatching
      to the same action handlers the hotkeys use — not a second,
      parallel implementation of each action
- [x] Revise `s3_menu.go`: removed "Sync Local Directory to Bucket,"
      "Browse/Manage Objects," and the standalone bulk-delete-by-prefix
      entry; added one "Browse & Manage Objects" entry point (DESIGN.md
      21.2)
- [x] Retired `bucket_browse.go`'s single-object wizard and
      `bucket_delete_objects.go`'s prefix-delete wizard now that the new
      screen covers their cases; `bucket_sync.go`'s wizard is also
      retired (its diff/walk/list helpers moved to `internal/s3diff`,
      reused by the new screen's Sync action rather than duplicated —
      see DECISIONS.md for why a plan-time assumption that these helpers
      would simply stay put in `internal/workflow` turned out not to fit
      Go's import-cycle constraints once `object_browser.go` needed to
      call into `internal/filemanager`)
- [ ] Still open, not resolved by this phase: whether to batch deletes
      via `s3:DeleteObjects` (up to 1000 keys/call) instead of the
      current one-`DeleteObject`-per-key loop, now in
      `internal/filemanager`'s Delete/Sync actions (see TODO.md, "nice
      to have")

**Tests:** resolved — `github.com/charmbracelet/x/exp/teatest` is real
and usable (confirmed against actual source, per this project's
evaluation discipline): `teatest.NewTestModel` runs the `Model` as a
real `bubbletea.Program` against an in-memory terminal, `.Send` injects
key messages, and `teatest.WaitFor` polls rendered output. One
practical caveat learned while writing these tests: bubbletea's
renderer only retransmits screen lines that changed since the last
frame, so asserting on unchanged-but-still-visible text across two
separate `WaitFor` calls can race (the earlier call already drained the
frame that contained it) — check multiple substrings in one `WaitFor`
condition, or assert on the status line's derived text instead of raw
row content, when that matters. `go test -race` caught one genuine bug
this surfaced: a running action's background goroutine (`runDelete`)
was mutating pane state (`clearTags`) directly instead of only sending
progress text over its channel, racing with the render loop's
concurrent read — fixed by moving that mutation to the overlay-dismiss
handler, which runs on Update's single goroutine. Diff/glob/listing
helpers are tested as plain Go functions independent of the `Model`
(`internal/s3diff`, `internal/filemanager/entry_test.go`,
`listing_test.go`, `pane_test.go`).

**Dependency:** Phase 20 (done)

---

## Phase 20.2 — S3 Menu: Convert RunS3Menu to huh.Select (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean). Continues
`continue_next_time.txt`'s next-up item from the Phase 20.1 session:
"replace the S3 management menu and display of buckets with the huh
module" — this phase covers the menu half; bucket-selection call sites
are Phase 20.4 (below).

**Files:** `internal/workflow/s3_menu.go`, `s3_menu_test.go`,
`huh_accessible_test.go` (new — reusable pipe-testable-input helper).

### Work Items

- [x] Resolve whether huh fields are pipe-testable at all before writing
      more untested huh code (see DECISIONS.md, "huh fields are
      pipe-testable via WithAccessible(true).WithInput/WithOutput") —
      caught and fixed a real starvation bug in the first pass
      (`strings.NewReader`-backed input silently drops every field after
      the first); fixed with a one-line-per-`Read` reader
      (`newHuhAccessibleInput`/`lineAtATimeReader`)
- [x] Convert `RunS3Menu`'s picker from `ui.PickList` to `huh.Select`,
      selecting by index into `s3MenuItems` (not by `s3Item` itself —
      `huh.Select[T]` requires `T comparable`, and `s3Item.action` is a
      func)
- [x] Map `huh.ErrUserAborted` to `ErrBackToDomainPicker` (abort now
      backs up one level, matching "Back to domain picker," instead of
      exiting the whole program) — covered by a standalone unit test
      (`mapS3MenuPickerErr`) since accessible mode can't itself produce
      that error to drive an end-to-end test with
- [x] Rewrite `s3_menu_test.go` against the new `runS3Menu` (unexported,
      takes injectable input/output); retired
      `TestRunS3Menu_CleanExitOnCancelledPickList` (tested a
      `PickList`-only "0=Cancel" affordance that no longer exists)

**Tests:** all existing dispatch/refresh/error-handling coverage
carried over unchanged (same input strings — huh's accessible-mode
1-indexed numbering happens to match `s3MenuItems`' order); new
`TestMapS3MenuPickerErr` for the abort mapping.

**Dependency:** Phase 20.1 (done, established the huh call-site
pattern); the pipe-testability resolution above (done, same session).

---

## Phase 20.3 — S3 Domain: Paged, Accessible Resource List Display (superseded)

**Superseded 2026-07-10, same day, by Phase 20.6 below.**
Screen-reader/accessible-mode compatibility -- this phase's central
constraint -- turned out not to be an actual requirement once discussed
directly (DECISIONS.md, "Deprecate termlib; standardize on huh/
bubbletea before 0.0.2"). `internal/ui.PagedTable`/`DisplayBuckets`,
implemented below, are retired in favor of a `bubbletea`-based List-tier
component built on a new shared `internal/tui` chrome package (Phase
20.5), less than a day after landing. Left below as the accurate record
of what was implemented and why it changed, not deleted.

**Status (as originally completed): implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean). Exposed by testing
Phase 20.2: every successful S3 menu action called `actions.Refresh(ctx)`,
which both re-fetched bucket data and printed the *entire* bucket table
(`ui.DisplayBuckets`) unconditionally — cluttering the menu's redisplay
after every action, with no pagination for a large bucket count. Full
design, mockup (approved before implementation started), and rejected
alternatives: DESIGN.md, "S3 Resource List Display — Paged, Accessible-
Compatible"; decision record: DECISIONS.md, "Decouple the S3 menu from
resource-list display; add a generic paged table to internal/ui".

**Priority:** requested directly by the user, ahead of Phase 20.4.
**Files:** `internal/ui/paged_table.go` (new, generic — not
bucket-specific), `paged_table_test.go` (new), `internal/ui/display.go`
(`DisplayBuckets` now takes `le` and returns `error`, delegates to
`PagedTable`), `display_test.go`, `internal/workflow/s3_menu.go`
(`S3Actions.ShowResourceLists`, new field; "Show resource lists" entry
now dispatches to it instead of `Refresh`), `s3_menu_test.go`,
`cmd/clasm/main.go` (`refreshS3` now silent-refetch-only;
`showS3ResourceLists`, new closure, wired to `ShowResourceLists`).

### Work Items

- [x] `internal/ui.PagedTable`: a generic pager (`Title` callback +
      pre-rendered `Header`/`Rows` strings in, `n`/`p`/`q` command loop
      via `le.Prompt`) — deliberately decoupled from any specific
      resource type, so Compute/Key Management's `DisplayInstances`/
      `DisplayImages`/`DisplayKeyPairs` can reuse it later without a
      redesign, per the user's framing ("we'll reuse this UI approach as
      needed migrating to huh for other parts of clasm"); not converting
      those other domains now. Written test-first: `paged_table_test.go`
      landed before `paged_table.go`.
- [x] Built the S3 buckets header/row strings for `PagedTable` reusing
      `DisplayBuckets`'s existing `PadRight`/`Truncate` column
      formatting — only the print-loop changed, not the column layout.
      `bucketsPageSize = 20` (smaller than `PickList`'s 50 -- a wide
      multi-column table affords fewer rows per screen than a
      single-column label list).
- [x] Split `cmd/clasm/main.go`'s `refreshS3`: data re-fetch stays
      unconditional (still called on S3 domain entry and after every S3
      action, via `S3Actions.Refresh`, so bucket-selection prompts
      elsewhere stay current); `showS3ResourceLists` is a separate
      closure, wired only to the new `S3Actions.ShowResourceLists` field,
      which `s3MenuItems`' "Show resource lists" entry calls instead of
      `Refresh`.
- [x] `q` (quit) returns to the S3 menu without printing anything
      further; `n`/`p` no-op at the first/last page, matching
      `PickList`'s existing boundary behavior. Commands are
      case-insensitive; unrecognized input reprints an "invalid command"
      message and redisplays the current page (mirrors `PickList`'s own
      reprompt-on-invalid convention).
- [x] Compute/Key Management's own "Show resource lists" listings are
      explicitly NOT touched by this phase.

**Tests:** `internal/ui/paged_table_test.go` (11 cases: single/multi
page, next/previous navigation and their at-boundary no-ops, page-back,
invalid-command reprompt, case-insensitive commands, empty rows, and
read-error propagation — each via `Title`'s recorded call args, not
string-scraping banners); `internal/ui/display_test.go` (`DisplayBuckets`
empty/populated/paginates-large-lists); `internal/workflow/s3_menu_test.go`
(new `TestRunS3Menu_ShowResourceListsDispatchesToItsOwnAction`, since no
prior test exercised choosing menu item 1 at all). No new testability
question — `PagedTable` is plain `termlib`/`LineEditor.Prompt` sequential
printing (no `huh`), pipe-testable the same way `PickList`'s existing
tests already are, reusing this package's own `newPipeEditor` helper.

**Dependency:** Phase 20.2 (done — this phase was found while testing
it, not a prerequisite of its design).

---

## Phase 20.4 — S3 Bucket Selection: Convert to tui.Picker (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Originally
scoped to convert to `huh.Select` (`continue_next_time.txt`'s remaining
next-up item from Phase 20.1's session); retargeted before any code was
written once the user pointed out `huh.Select`'s rendering doesn't
match the List/Manager tiers' chrome ("this UI should feel the same
whether I select a bucket, an AMI or an EC2 instance") — see
DECISIONS.md, "Add a Picker tier: resource selection gets its own
internal/tui component, not huh.Select," and DESIGN.md's "Picker tier"
section (with the full map of every current resource-selection call
site across the app).

Converted the bucket-selection step inside `ConfigureBucketWebsite`
(`bucket_website.go`), `ManageBucketLifecyclePolicies`
(`bucket_lifecycle.go`), and `DeleteBucket` (`bucket_delete.go`) —
previously `ui.PickList(t, le, buckets, bucketLabel, "Select a
bucket")` — to a shared `pickBucket` helper (`bucket_website.go`, next
to `bucketLabel`) built on `tui.RunPicker`. `CreateBucket` stayed out of
scope (it creates a new bucket, not select an existing one); the rest
of each workflow stays on termlib. `object_browser.go`'s existing
`huh.Select`-based bucket pre-flight was NOT touched by this phase —
whether it should also move to `PickerModel` is a separate question,
not decided here.

**Testable-core split, since `tui.RunPicker` runs a real bubbletea
Program that can't be driven by a test's pipe input** (mirrors the
`RunS3Menu`/`runS3Menu` split from Phase 20.2): each of the three
exported functions now does the picker call, then delegates to an
unexported core taking the already-resolved `bucket` directly
(`configureBucketWebsite`, `manageBucketLifecyclePolicies`,
`deleteBucket`). Every existing test for "pick a bucket, then do X" was
rewritten to call the unexported core with a bucket value instead of
driving a `ui.PickList`-shaped `"1\n"` pipe input; each function's own
"cancel while picking a bucket" test (`TestConfigureBucketWebsite_
CancellationAbortsCleanly`, `TestManageBucketLifecyclePolicies_
CancellationAtBucketPick`, and `DeleteBucket`'s equivalent) was retired
— that tested `ui.PickList`'s "0=Cancel" numbered-option convention,
which no longer exists once selection is `tui.RunPicker`-based. The
picker-selection step itself is covered only by manual/interactive
verification going forward, the same accepted limitation
`object_browser.go`'s huh-based bucket pre-flight already has.
`cancelledIsNil` (`manage_tags.go`) now also recognizes
`tui.ErrCancelled` alongside `ui.ErrCancelled`, so cancelling either
kind of picker behaves identically from the operator's point of view.

**Dependency:** Phase 20.8 (`internal/tui.PickerModel` itself, done).

---

## Phase 20.5 — internal/tui: Shared Chrome Package (extracted from the file manager) (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean). First piece of
DESIGN.md's "Terminal UI Architecture: Menus, Actions, Lists, and
Managers"; decision record: DECISIONS.md, "Terminal UI architecture:
menu → action/list/manager taxonomy; shared internal/tui chrome
package."

**Files:** new `internal/tui` package (`box.go`, `scroll.go`, `style.go`
+ their `_test.go` files, written test-first); `internal/filemanager/view.go`
(box-drawing/scroll/style helpers removed, replaced with calls into
`internal/tui`); `internal/filemanager/box_test.go`/`scroll_test.go`
(moved tests removed, remaining Model-level tests updated to call
`tui.RuneLen` instead of the now-moved `stripANSI`).

### Work Items

- [x] Moved `topBorder`→`TopBorder`, `bottomBorder`→`BottomBorder`,
      `divider`→`Divider`, `splitDivider`→`SplitDivider`,
      `mergeDivider`→`MergeDivider`, `boxLine`→`BoxLine`,
      `boxRow2`→`BoxRow2`, `padOrTruncate`→`PadOrTruncate`,
      `runeLen`→`RuneLen`, `stripANSI`→`StripANSI`, `scrollWindow`→
      `ScrollWindow`, `styleRow`→`StyleRow` from
      `internal/filemanager/view.go` into `internal/tui`, exported
      (capitalized) since they're now a separate package's public API;
      `truncateVisible`/`reverseVideo`/`bold`/the `ansi*` constants stay
      unexported within `internal/tui` (only used internally, by
      `PadOrTruncate`/`StyleRow` respectively) — confirmed no other
      caller needed them exported. `splitWidths` (the double-pane
      column-split math) stayed in `internal/filemanager` — it's
      specific to that package's two-pane layout, not generic chrome.
- [x] `internal/filemanager` imports `internal/tui` for all of the
      above instead of keeping its own copy
- [x] No behavior change: `internal/filemanager`'s existing test suite
      continues to pass unmodified in assertions (only the two
      Model-level width-check tests' direct `stripANSI` calls were
      updated to `tui.RuneLen`, since `stripANSI` itself moved and is
      now unexported in its new package)

**Tests:** `internal/tui/box_test.go`/`scroll_test.go`/`style_test.go`
(20 cases total, several new beyond what `internal/filemanager` already
had indirectly — direct coverage for `TopBorder`/`BottomBorder`/
`Divider`/`SplitDivider`/`MergeDivider`/`BoxLine`, which previously only
had indirect coverage via `filemanager.Model.View()`'s tests), written
before `box.go`/`scroll.go`/`style.go` existed. `internal/filemanager`'s
existing test suite (unchanged assertions, `box_test.go`/`scroll_test.go`
trimmed to their Model-level cases) is the regression check that the
extraction was behavior-preserving.

**Dependency:** none (pure refactor, no new external dependency).

---

## Phase 20.6 — S3 Domain: List Viewer bubbletea Component (replaces PagedTable) (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean). Replaces Phase 20.3
(superseded, above). Full design: DESIGN.md, "Terminal UI
Architecture...," "List tier" section; decision record: DECISIONS.md,
"Terminal UI architecture...".

**Files:** `internal/tui/listview.go` (new: `ListViewConfig`,
`ListViewModel`, `NewListViewModel`, `RunListView`) + `listview_test.go`
(new, written test-first); `internal/ui/display.go` (`DisplayBuckets`
rewritten around `tui.RunListView`; `bucketListViewConfig` extracted as
its testable core); `internal/ui/paged_table.go`/`paged_table_test.go`
(removed); `cmd/clasm/main.go` (`showS3ResourceLists` now calls
`ui.DisplayBuckets(ctx, s3State.buckets)` — no `term`/`le` needed, `huh`/
`termlib` aren't involved at all). The "List S3 Buckets" rename and
dropping "Back to domain picker" are Phase 20.7, not this phase — this
phase only replaces the *rendering mechanism* behind "Show resource
lists," not its label or the surrounding menu.

### Work Items

- [x] A single bordered box (no split panes), frozen header row,
      scrollable body reusing `internal/tui`'s shared `ScrollWindow`
      logic (Phase 20.5)
- [x] Sized to the real terminal via `tea.WindowSizeMsg` (sent once at
      start, again on every resize except Windows/no-SIGWINCH — an
      initial size still arrives there); falls back to
      `defaultListViewWidth`/`Height` before the first one lands
- [x] A real legend bar at the bottom ("↑/↓,k/j scroll  q Quit") — this
      tier fully owns its rendering, unlike the menu tier
- [x] Renders inline, no `tea.WithAltScreen`, matching every other
      screen in this app
- [x] `q`/`ctrl+c` quits `RunListView` with a nil error, which
      `DisplayBuckets`/`ShowResourceLists` simply propagate — `runS3Menu`
      treats that the same as any other successful action, continuing
      its own loop back to the S3 menu. No `ErrBackToDomainPicker`
      special-casing needed; returning to the right screen falls out of
      the existing dispatch structure by construction.
- [x] Reuses `DisplayBuckets`'s existing bucket-row formatting
      (`PadRight`/`Truncate` column layout), now isolated in
      `bucketListViewConfig` — only the rendering mechanism changed, not
      the column layout

**Tests:** `internal/tui/listview_test.go` (9 cases). A real rendering
lesson surfaced while writing them, worth keeping in mind for any future
`internal/tui` component: when rendered content height *exactly* matches
the declared terminal height (this component's own "fill the screen"
design, by construction — `windowHeight = height - chrome`), driving it
through a real `teatest.NewTestModel` Program can lose its own top line
to the emulated terminal's scrolling, a known class of issue with inline
(non-`tea.WithAltScreen`) bubbletea rendering, not a bug in this
component specifically. `internal/filemanager`'s own test suite already
sidesteps this the same way: exact-height/scroll-window assertions
(`TestModel_LargeListing_*`) drive `Model` directly (set
`width`/`height`, call `Update`/`View()` synchronously) rather than
through `teatest`, reserving `teatest` for key-driven behavior with
content comfortably smaller than the terminal. This phase's tests follow
the same split.

**Dependency:** Phase 20.5 (done — the shared chrome it's built on).

**Dependency:** Phase 20.5 (the shared chrome it's built on).

---

## Phase 20.7 — S3 Menu: universal 'q' quit key; remove "Back to domain picker"; rename "Show resource lists" (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean). Applies DECISIONS.md's
"TUI keybinding conventions" to the one menu converted so far.

**Files:** `internal/workflow/s3_menu.go`, `s3_menu_test.go`.

### Work Items

- [x] `RunS3Menu`'s `huh.Select` gains `q` as an additional `Quit`
      trigger (`Form.WithKeyMap`, `KeyMap.Quit` gains `"q"` alongside
      the default `"ctrl+c"` via `key.NewBinding(key.WithKeys("ctrl+c",
      "q"))`) — resolves through the already-existing
      `mapS3MenuPickerErr`/`ErrUserAborted`→`ErrBackToDomainPicker`
      path; no new dispatch logic
- [x] A short static hint line (`"(q to go back)"`) printed via the
      existing `t.Println`/`t.Refresh()` above the menu on every
      redisplay (huh's own footer can't show a custom "q: quit" entry —
      see DECISIONS.md for why)
- [x] Removed "Back to domain picker" from `s3MenuItems` (redundant with
      `q`); removed the now-dead `choice.action == nil` branch in
      `runS3Menu` (`s3Item.action` is never nil anymore)
- [x] Relabeled "Show resource lists" → "List S3 Buckets" (label only;
      `S3Actions.ShowResourceLists` and other Go identifiers are
      unchanged)

**Tests:** `s3_menu_test.go` rewritten around a `context.WithCancel` +
cancel-from-within-the-test-action-closure pattern for every test that
previously chose "Back to domain picker" (item 7) to end the loop after
observing one dispatch — that menu item no longer exists, and accessible
mode has no way to simulate the `q`/ctrl+c abort that replaces it (same
limitation `mapS3MenuPickerErr` already documented). New
`TestS3MenuItems_NoBackToDomainPickerEntry` (exactly 6 items, no nil
action) and `TestS3MenuItems_FirstItemIsListS3Buckets` guard the removal/
rename directly. The `q`-triggers-Quit behavior itself can only be
confirmed by real interactive use — not yet done, same class of gap as
this session's other `huh`/`bubbletea` work.

**Dependency:** Phase 20.2 (the menu this applies to).

---

## Phase 20.8 — internal/tui: PickerModel (selectable, filterable List-tier component) (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Full
design: DESIGN.md, "Terminal UI Architecture...," "Picker tier" section;
decision record: DECISIONS.md, "Add a Picker tier: resource selection
gets its own internal/tui component, not huh.Select."

**Files:** `internal/tui/picker.go` (`PickerConfig`, `PickerModel`,
`NewPickerModel`, `RunPicker`, `ErrCancelled`) + `picker_test.go` (12
cases, written test-first).

### Work Items

- [x] Same chrome as `ListViewModel` (`TopBorder`/`BoxLine`/`Divider`/
      `ScrollWindow`/`StyleRow`/`BottomBorder`), same real
      `tea.WindowSizeMsg` sizing, same inline (no altscreen) rendering,
      same legend-bar convention
- [x] `Enter` selects the row under the cursor and returns its index;
      `q`/`ctrl+c` cancels, reported as the new `tui.ErrCancelled`
      (mirrors `ui.PickList`'s `ErrCancelled` and huh's
      `ErrUserAborted`, so callers use the same `if err != nil { return
      cancelledIsNil(...) }` shape as every other pick-list-shaped call
      site)
- [x] `/` enters filter-typing mode (not always-on type-ahead — `j`/`k`
      stay unambiguous navigation keys outside of filter mode), narrows
      visible rows by case-insensitive substring match against each
      row's rendered text (mirroring `ui.PickList`'s `filterByLabel`
      convention and `internal/filemanager`'s pane filter), `Esc`
      clears the filter. Deliberately does not special-case `q`/ctrl+c
      while filtering (every key is literal text), matching
      `internal/filemanager`'s own `handleCommandLineKey` precedent.
- [x] Returns an index into the caller's original row slice, not a
      typed value — `internal/tui` doesn't need generics; callers map
      the index back into their own typed slice (`buckets[idx]`, ...),
      the same pattern `pickS3MenuItem` already uses for `s3MenuItems`

**A real rendering finding, beyond what Phase 20.6 already documented:**
the content area's rendered height must be pinned to the *unfiltered*
row count (bounded by the window height), not however many rows the
current filter happens to match — otherwise the box's height shrinks
and grows as the operator types a filter, which reproduced the same
class of inline-bubbletea-rendering hiccup Phase 20.6 found with
exact/changing frame heights (confirmed by a failing test before this
fix, not just reasoned about). Fixed by padding the content area with
blank rows up to a stable height determined by the total dataset size —
incidentally also better UX (a fixed-height results viewport while
typing a filter, `fzf`-style, rather than the box visibly resizing).

**Tests:** written before the implementation, following
`listview_test.go`'s established split (`teatest` for key-driven
behavior with content comfortably smaller than the terminal, direct
`Model`-driving for exact scroll-window/height assertions). Two
`teatest`-based filter tests initially failed for a second, distinct
reason: bubbletea only retransmits screen lines that changed since the
*immediately preceding* frame, so checking for the same text across two
separate `WaitFor` calls (one already having drained it from the stream)
can race if a later frame doesn't happen to change that particular line
again — fixed by combining assertions into single `WaitFor` calls,
exactly the workaround `internal/filemanager`'s own tests already
document for this same class of issue.

**Dependency:** Phase 20.5 (the shared chrome it's built on).

---

## Phase 20.9 — Lifecycle Rules Action Menu: Convert to huh.Select (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Requested
directly by the user after manually trying the S3 domain (Phase 20.7's
`q` key took a moment to render the first time, prompting a live check):
convert `ManageBucketLifecyclePolicies`'s "Choose an action" menu (Add
rule/Edit rule/Remove rule/View rule details) from `ui.PickList` to
`huh.Select`, matching `RunS3Menu`'s Phase 20.2/20.7 pattern exactly —
this is a guide-menu-shaped choice (a small, fixed action set), not a
Picker-tier candidate (DESIGN.md's "Picker tier" map already excluded it
for this reason).

**Files:** `internal/workflow/bucket_lifecycle.go`
(`pickLifecycleAction`, new; `lifecycleActionLabel` removed, no longer
needed), `bucket_lifecycle_test.go` (all 15 tests updated).

### Work Items

- [x] `pickLifecycleAction`: `huh.Select[string]` over `lifecycleActions`
      (already comparable strings, no index-based workaround needed,
      unlike Phase 20.2's `s3MenuItems`), `q` bound alongside `ctrl+c` on
      `Quit`, input/output nil in production, supplied by tests for the
      accessible-mode pipe path (same shape as `pickS3MenuItem`)
- [x] "Back" removed from `lifecycleActions`; the loop's `switch`
      statement is exhaustive over the remaining 4 actions, no `default`
      fallback needed
- [x] A `ctx.Err()` check added at the top of
      `manageBucketLifecyclePolicies`'s loop (previously missing,
      unlike `RunS3Menu`/`RunMainMenu`/`RunKeyMgmtMenu`'s loops) — needed
      for test termination via context-cancellation, and closes a
      pre-existing small gap in this loop's own convention
- [x] `"(q to go back)"` hint printed above the action menu, matching
      `RunS3Menu`'s convention (huh's own footer can't show it)
- [x] Abort maps through `huhCancelledIsNil` (clean nil return, no
      "Cancelled." message) — treated as equivalent to the old "Back"
      choice, not to `ui.PickList`'s "0=Cancel" (which does print
      "Cancelled." via `cancelledIsNil`); this action menu is a menu, not
      an in-progress action, per the "quit vs. cancel" wording
      convention

**Tests:** action-menu selections now feed through a separate
`newHuhAccessibleInput` reader, not `le` (which still feeds every other
prompt in this function — rule/storage-class `PickList`s, confirms,
day-count/ID input, unaffected by this phase). Several tests that used
to select "Back" (position 5) to end the loop cleanly were restructured
around real terminating actions instead, since that position no longer
exists and the `q`/ctrl+c abort that replaces it can't be simulated in
accessible mode (same limitation `mapS3MenuPickerErr` already
documents) — e.g. choosing "Edit rule"/"View rule details" with zero
rules present returns immediately by construction, which several tests
already relied on incidentally and now rely on deliberately.
`TestManageBucketLifecyclePolicies_BackActionSkipsPut` (tested the "Back"
choice specifically) was retired, matching the precedent set by
`TestRunS3Menu_BackToDomainPickerDoesNotRefresh` in Phase 20.2.

**Dependency:** Phase 20.2 (established the pattern this reuses).

---

## Phase 20.10 — Menu Tier: Top-Level Navigation Menus (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). First
batch of DESIGN.md's Menu-tier punch list, working through it in the
order the user requested (menu tier, then picker, then list). Converts
the three top-level navigation menus from `ui.PickList` to `huh.Select`,
each an exact copy of `RunS3Menu`'s Phase 20.2/20.7 pattern: select by
index (each item's `action` is a func, not comparable), `q` bound
alongside `ctrl+c` on `Quit`, a printed hint above the menu, the
redundant "Back"/"Exit" menu item dropped.

**Files:** `domain_menu.go`/`domain_menu_test.go`, `menu.go`/
`menu_test.go`, `keymgmt_menu.go`/`keymgmt_menu_test.go`.

### Work Items

- [x] Domain picker (`domain_menu.go`, `pickDomainItem`): drops "Exit"
      (not "Back" -- this is the root menu, so `q` here means exit the
      whole program, matching what "Exit" used to do); hint text is
      `"(q to exit)"`, not `"(q to go back)"`
- [x] Compute main menu (`menu.go`, `pickMainMenuItem`): drops "Back to
      domain picker"
- [x] Key Management menu (`keymgmt_menu.go`, `pickKeyMgmtItem`): drops
      "Back to domain picker"
- [x] `mapS3MenuPickerErr` generalized to `mapMenuPickerErr` and moved to
      `domain_menu.go` (next to `ErrBackToDomainPicker`, its own natural
      home) -- shared across all `huh`-converted domain menus instead of
      duplicated per file

**Tests:** every test that used to select "Back"/"Exit" (by position) to
end a menu loop was rewritten around the `context.WithCancel` +
cancel-from-within-the-test-action-closure pattern established in Phase
20.7 (`cancelingAction`, shared from `s3_menu_test.go` rather than
redefined per file); tests for the removed-item's own specific behavior
(`TestRunMainMenu_BackToDomainPickerDoesNotRefresh`,
`TestRunKeyMgmtMenu_BackToDomainPickerDoesNotRefresh`,
`TestRunMainMenu_CleanExitOnCancelledPickList`,
`TestRunKeyMgmtMenu_CleanExitOnCancelledPickList`,
`TestRunDomainPicker_ExitEndsTheProgram`,
`TestRunDomainPicker_CleanExitOnCancelledPickList`) were retired, same
precedent as Phase 20.2/20.7/20.9. New `TestDomainItems_NoExitEntry`/
`TestMainMenuItems_NoBackToDomainPickerEntry`/
`TestKeyMgmtMenuItems_NoBackToDomainPickerEntry` guard each menu's
item count and non-nil actions directly.

**Dependency:** Phase 20.2 (the pattern this reuses).

---

## Phase 20.11 — Menu Tier: Remaining Punch-List Items (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Completes
DESIGN.md's Menu-tier punch list -- every remaining `ui.PickList`
call site classified as Menu tier is now `huh.Select`. Two shared
helpers added to `domain_menu.go` next to `runMenuField`/
`menuQuitKeyMap`: `pickString` (fixed `[]string` options) and its
generic backer `pickComparable[T comparable]` (fixed `[]T` options with
a caller-supplied label func) -- covers every remaining site without
repeating the index-selection workaround `pickS3MenuItem`/
`pickMainMenuItem`/etc. needed only because their option types embed a
`func` field.

### Work Items

- [x] Instance-vs-AMI kind, `show_cloud_init.go`/`manage_tags.go`: split
      `ShowCloudInit`/`ManageTags` into thin entry points + testable
      `showCloudInit`/`manageTags` cores taking a shared
      `menuInput`/`menuOutput` pair
- [x] Tag Add/Update/Remove action + select-a-tag-to-update/remove,
      `manage_tags.go`: same `menuInput`/`menuOutput` pair as the kind
      picker above -- all four huh.Selects in one call read the shared
      reader in sequence
- [x] Region (S3, `bucket_create.go`) and Region (Key Management,
      `keymgmt_common.go`): `promptRegion`/`promptS3Region` take
      `input`/`output` now; `CreateBucket`, `CreateKeyPairStandalone`,
      `ImportKeyPairStandalone` each split into entry point + testable
      core
- [x] Bucket-purpose enum, `bucket_create.go`: same `createBucket` core
      as the region picker above
- [x] Instance type (curated list + "Other"), `launch_prompts.go`:
      `promptInstanceType` takes `input`/`output`; selects by
      `instanceTypeChoice` value directly via `pickComparable` (no index
      workaround needed -- the struct is `comparable`)
- [x] AZ-incompatibility remediation choice,
      `instance_type_az_check.go`, and ENA-incompatibility remediation
      choice, `instance_type_ena_check.go`: `ensureInstanceType
      SupportedInSubnet`/`ensureInstanceTypeENACompatible` take a shared
      `menuInput`/`menuOutput` pair, threaded into their own nested
      `promptInstanceType` call when the operator picks "Change instance
      type" -- both are loops, so the pair is read across iterations the
      same way a domain menu's own loop reads it
  - Threading this up the call chain required splitting
    `CollectLaunchInstanceParams`/`CollectLaunchInstanceParamsFromCloudInit`
    (`launch_instance.go`/`launch_from_cloud_init.go`) and
    `CreateInstanceFromAMI`/`CreateInstanceFromCloudInit`
    (`create_instance_from_ami.go`/`create_instance_from_cloud_init.go`)
    into entry points + testable cores, all sharing one
    `menuInput`/`menuOutput` pair down to `promptInstanceType`
- [x] Storage class, guided backup flow (curated 4) and generic editor
      (full enum), `bucket_lifecycle.go`: `promptGuidedBackupRule`/
      `promptGenericRule` take `menuInput`/`menuOutput`, reusing
      `manageBucketLifecyclePolicies`'s existing `actionMenuInput`/
      `actionMenuOutput` pair (already threaded through for
      `pickLifecycleAction`) via `addLifecycleRule`/`editLifecycleRule`

**Tests:** every affected test call site now feeds its huh.Select(s) via
a separate `newHuhAccessibleInput` reader instead of the numbered
`le`-pipe selection `ui.PickList` used to read; `le` still feeds every
other prompt unaffected by these conversions. Cancellation tests for
pickers that used to support a `PickList` "0=Cancel"/`le`-driven abort
(`TestShowCloudInit_CancelledKindPickList`,
`TestCreateBucket_RegionCancellationAbortsCleanly`,
`TestCreateKeyPairStandalone_CancelledRegionPick`,
`TestImportKeyPairStandalone_CancelledRegionPick`) were retired -- `q`/
`ctrl+c` abort has no accessible-mode keyboard to simulate it with, same
precedent as every prior phase's menu conversions. `cancelledIsNil`
(`manage_tags.go`) now also matches `huh.ErrUserAborted`, unifying it
with `ui.ErrCancelled`/`tui.ErrCancelled` as the one cancellation-mapping
policy for one-off Menu-tier pickers (as opposed to `mapMenuPickerErr`,
which is specific to domain-loop menus backing out to
`ErrBackToDomainPicker`).

**Files:** `domain_menu.go` (new `pickString`/`pickComparable`
helpers), `show_cloud_init.go`/`_test.go`, `manage_tags.go`/`_test.go`,
`bucket_create.go`/`_test.go`, `keymgmt_common.go`, `keypair_create.go`/
`_test.go`, `keypair_import.go`/`_test.go`, `launch_prompts.go`/
`_test.go`, `instance_type_az_check.go`/`_test.go`,
`instance_type_ena_check.go`/`_test.go`, `launch_instance.go`/`_test.go`,
`launch_from_cloud_init.go`/`_test.go`, `create_instance_from_ami.go`/
`_test.go`, `create_instance_from_cloud_init.go`/`_test.go`,
`bucket_lifecycle.go`/`_test.go`.

**Dependency:** Phase 20.10 (the `runMenuField` helper this builds on).

DESIGN.md's Menu-tier punch list is now fully converted -- next up is
the Picker tier (8 remaining entries), per the user's requested order
(menu, then picker, then list).

---

## Phase 20.12 — Picker Tier: Every Remaining Resource Selector (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Completes
DESIGN.md's Picker-tier punch list -- every remaining `ui.PickList` call
site classified as Picker tier (fetched/variable-length resource
collections) is now `tui.RunPicker`. Six new one-line-per-type picker
helpers added next to each resource's own label function, all following
`pickBucket`'s exact shape (Phase 20.4): build `rows []string` from the
resource's label func, call `tui.RunPicker`, index back into the
original slice -- `pickInstance`/`pickImage` (power_state.go/
launch_instance.go), `pickSubnet` (launch_prompts.go),
`pickInstanceProfileChoice`/`pickRole` (create_instance_profile.go),
`pickKeyPairChoice` (create_key_pair.go), `pickKeyPairForDeletion`
(keypair_delete.go), `pickLifecycleRule` (bucket_lifecycle.go).

### Work Items

- [x] EC2 instance, 6 call sites (`backup_archive.go`,
      `create_ami_from_instance.go`, `show_cloud_init.go`,
      `power_state.go` x2, `terminate_instance.go`, `manage_tags.go`):
      each split into a thin entry point (calls `pickInstance`) + a
      testable core taking the already-resolved instance directly
- [x] AMI, 5 call sites (`launch_from_cloud_init.go`, `launch_instance.go`,
      `show_cloud_init.go`, `manage_tags.go`, `remove_ami.go`): same
      split. `launch_instance.go`/`launch_from_cloud_init.go` required an
      extra cascade -- `CollectLaunchInstanceParams(FromCloudInit)` and
      `CreateInstanceFrom{AMI,CloudInit}` all split into entry points +
      testable cores taking a resolved `image` instead of the full
      `images` list, since AMI selection used to happen *inside* the
      already-testable core built in Phase 20.11
- [x] Subnet, `launch_prompts.go` (`promptSubnetID`): list-path tests
      retired -- `filterSubnetsByInstanceTypeAZ`'s own tests
      (instance_type_az_check_test.go) already cover the pre-picker
      filtering logic; the free-text fallback path (zero subnets) stays
      fully testable
- [x] IAM instance profile (+ none/create-new) and IAM role (to attach),
      `create_instance_profile.go`: `createInstanceProfileInteractive`
      split so `createInstanceProfileForRole` (the create-new sub-flow,
      once a role is resolved) is directly testable; list-path tests for
      `promptIAMInstanceProfileOrCreate` itself retired since it always
      builds a choices list of at least `["(none)", "Create new..."]`,
      reaching the picker on every path except the list-fetch-error
      free-text fallback
- [x] Key pair (fetched, + create-new), `create_key_pair.go`
      (`promptKeyPairNameOrCreate`): list-path tests retired (redundant
      with `listKeyPairs`' own tests); `createNewKeyPairInteractive` (no
      picker of its own) gained its own direct test coverage instead of
      being driven indirectly through the picker
- [x] Key pair (fetched, to delete), `keypair_delete.go`
      (`DeleteKeyPair`): same entry-point/testable-core split as EC2
      instance/AMI
- [x] S3 lifecycle rule (view/edit/remove), `bucket_lifecycle.go`:
      `viewLifecycleRuleDetail`/`editLifecycleRule`/`removeLifecycleRule`
      all gained a `ctx` param and now call `pickLifecycleRule`;
      `editLifecycleRuleForRule`/`removeLifecycleRuleForRule` extracted
      as testable cores; the "view" path's own display logic
      (`printLifecycleRuleDetail`) got direct test coverage instead of
      being driven through the loop

**Tests:** every affected call site's happy-path/error-path tests were
rewritten to call the new testable core with an already-resolved
resource instead of driving `ui.PickList`'s numbered selection through
`le`; a handful of fakes needed a forced-error field
(`fakeIAMClientNoProfiles()`, `errNoKeyPairsConfigured` on
`fakeEC2Client`) so unrelated launch-params tests don't themselves
reach the now-bubbletea IAM-profile/key-pair pickers. "0=Cancel"/
list-selection tests for every converted picker were retired -- a real
bubbletea Program can't be pipe-tested, no keyboard to simulate an abort
or a specific-item selection with -- the same precedent `pickBucket`
(Phase 20.4) already established; each retirement is commented in place
noting what still covers the underlying logic directly (the resource's
own list/filter tests, or a newly-split testable core).

**Files:** `power_state.go`/`_test.go`, `launch_instance.go`/`_test.go`,
`launch_from_cloud_init.go`/`_test.go`, `create_instance_from_ami.go`/
`_test.go`, `create_instance_from_cloud_init.go`/`_test.go`,
`terminate_instance.go`/`_test.go`, `backup_archive.go`/`_test.go`,
`create_ami_from_instance.go`/`_test.go`, `remove_ami.go`/`_test.go`,
`show_cloud_init.go`/`_test.go`, `manage_tags.go`/`_test.go`,
`launch_prompts.go`/`_test.go`, `create_instance_profile.go`/`_test.go`,
`create_key_pair.go`/`_test.go`, `keypair_delete.go`/`_test.go`,
`bucket_lifecycle.go`/`_test.go`, `userdata_test.go` (gained
`promptCloudInitYAMLFile`'s own direct tests, migrated out of
`launch_from_cloud_init_test.go`).

**Dependency:** Phase 20.4 (`pickBucket`, the pattern every helper here
copies) and Phase 20.11 (the Menu-tier sweep that had to finish first).

DESIGN.md's Picker-tier punch list is now fully converted -- next up is
the List tier (3 remaining entries: EC2 instances, AMIs, Key pairs), per
the user's requested order (menu, then picker, then list).

---

## Phase 20.13 — List Tier: EC2 Instances, AMIs, Key Pairs (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Completes
DESIGN.md's List-tier punch list -- the last remaining tier from the
full termlib-to-huh/bubbletea conversion sweep. `DisplayInstances`,
`DisplayImages`, and `DisplayKeyPairs` (`internal/ui/display.go`)
converted to `tui.RunListView`, mirroring `DisplayBuckets`/
`bucketListViewConfig` (Phase 20.6) exactly: each gained an
`instanceListViewConfig`/`imageListViewConfig`/`keyPairListViewConfig`
builder (pure, unit-testable column formatting, reusing the existing
`orUnknown`/`orNone`/`stateColor` helpers unchanged) and became a thin
`func(ctx, ...) error` wrapper.

### Work Items

- [x] `DisplayInstances`/`DisplayImages`/`DisplayKeyPairs`: new
      `*ListViewConfig` builders + `tui.RunListView` wrappers, signature
      changed from `(t *termlib.Terminal, ...)` (no return) to
      `(ctx context.Context, ...) error`, matching `DisplayBuckets`
- [x] `instanceRow` extracted from `instanceListViewConfig` so the
      STATE column's color-embedding logic (running=green,
      stopped/terminated=red, pending/stopping=yellow) stays testable
      via an explicit `colorEnabled bool` parameter, independent of the
      real `ColorEnabled()` TTY/NO_COLOR check a test runs under
      (`go test` never has a real stdout TTY, so `ColorEnabled()` itself
      can't be forced true in-process)
- [x] Fixed a real bug surfaced by preserving that STATE color: `tui.
      reverseVideo` (internal/tui/style.go) now re-asserts reverse-video
      after any reset a row already embeds, so a colorized STATE cell
      landing on the cursor row doesn't cut the row's highlight short at
      the cell's own closing reset
- [x] `MenuActions`/`KeyMgmtActions` (`internal/workflow/menu.go`/
      `keymgmt_menu.go`) each split their existing `Refresh` field into a
      silent fetch-only `Refresh` + a new `ShowResourceLists` display
      field, mirroring `S3Actions`' own split (Phase 20.6) -- required
      because `tui.RunListView` blocks on an interactive bubbletea loop
      until `q`, so calling it unconditionally after every dispatched
      action (the old `Refresh` behavior) would force pressing `q` after
      every single action just to get back to the menu. `mainMenuItems`/
      `keyMgmtMenuItems`'s "Show resource lists" entries now dispatch to
      `ShowResourceLists` instead of `Refresh`
- [x] `cmd/clasm/main.go`: `refresh`/`refreshKeyMgmt` closures now only
      fetch (no display); new `showComputeResourceLists`/
      `showKeyMgmtResourceLists` closures call the converted `Display*`
      functions and are wired to each `Actions` struct's new
      `ShowResourceLists` field

**Tests:** `display_test.go`'s `TestDisplayInstances_*`/
`TestDisplayImages_*`/`TestDisplayKeyPairs_*` (direct calls against a
`bytes.Buffer`-backed `termlib.Terminal`) replaced with
`TestInstanceListViewConfig_*`/`TestImageListViewConfig_*`/
`TestKeyPairListViewConfig_*` (direct calls against the new pure
builders, asserting on `cfg.Header`/`cfg.Rows`/`cfg.Title`), matching
`TestBucketListViewConfig_*`'s existing style exactly; the two
color-specific tests became `TestInstanceRow_ColorEnabled_
AppliesStateColor`/`ColorDisabled_NoANSICodes`, calling `instanceRow`
directly with an explicit bool instead of relying on the ambient
terminal state `DisplayInstances` used to take as a parameter. New
`TestStyleRow_CursorRowReassertsReverseVideoAfterEmbeddedReset`
(`internal/tui/style_test.go`) covers the `reverseVideo` fix directly.
`menu_test.go`/`keymgmt_menu_test.go` gained
`TestRunMainMenu_ShowResourceListsDispatchesToItsOwnAction`/
`TestRunKeyMgmtMenu_ShowResourceListsDispatchesToItsOwnAction`, mirroring
`s3_menu_test.go`'s existing `TestRunS3Menu_ShowResourceListsDispatches
ToItsOwnAction` -- neither menu's "Show resource lists" dispatch had
ever actually been exercised by a test before (both `testMenuActions`/
`testKeyMgmtActions` helpers only wired `Refresh`), a real coverage gap
this phase closed along the way.

## Phase 20.14 — Chrome Consistency: Full-Height Rendering Fix + List-Tier Filtering (done)

**Status: implemented and unit-tested 2026-07-10** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). Follow-up
to Phase 20.13, from user-reported feedback after using the newly
converted List tier: (1) the List/Picker/file-manager boxes weren't
using the full terminal height, and (2) List-tier filtering -- listed
in DESIGN.md's keybinding table (`/` = Filter, "Menus, pickers, lists,
managers") since Phase 20.8 but never built for lists -- was still
missing.

### Work Items

- [x] Root-caused the height bug: these are inline (non-alt-screen)
      bubbletea programs, and a box sized to nearly the full terminal
      height renders wherever the cursor already sits rather than at
      row 0; if that doesn't fit in the remaining rows below the
      cursor, the terminal scrolls and bubbletea's redraw-in-place
      bookkeeping goes stale, pushing the top of the box out of view.
      Fixed by returning `tea.ClearScreen` from `Init()` on
      `ListViewModel`, `PickerModel`, and `filemanager.Model` --
      confirmed by the user ("Scrolling is much improved") -- see
      DECISIONS.md, "Clear the screen on entry for every inline
      bubbletea screen"
- [x] Extracted `internal/tui/filter.go`'s `filterState` (`apply`,
      `moveCursor`, `handleIdleKey`, `handleFilterKey`, `statusLine`)
      out of `PickerModel`'s previously-inline filter fields/methods, so
      `ListViewModel` and `PickerModel` share one filter implementation
      instead of each keeping its own copy -- keeps them consistent by
      construction rather than by convention, per the user's "we want
      to have the chrome more consistent" feedback
- [x] `ListViewModel` gained `/` filter-typing mode, the filter status
      line + divider, and an updated legend
      (`↑/↓,k/j scroll  / filter  q Quit`), reusing `PickerModel`'s
      exact behavior (case-insensitive substring match, `Enter` commits
      and keeps navigating the narrowed list, `Esc` clears it,
      content-height pinned to the *unfiltered* row count so the box
      doesn't jitter while typing)
- [x] Also made `ListViewModel`'s header row conditional on
      `Header != ""` (previously always rendered, even blank), matching
      `PickerModel` exactly -- zero behavior change for existing
      callers, all of which always supply a `Header`
- [x] Unified both models' `windowHeight()` onto one shared
      `filterableWindowHeight(height, hasHeader bool)` helper
      (`baseChromeRows` + `headerChromeRows` if header + always
      `filterChromeRowCount` for the filter line/divider), fixing a
      minor pre-existing off-by-one in `PickerModel`'s own chrome math
      (it subtracted an extra, imprecise `-1` "for the filter line"
      rather than counting the filter line's own divider)

**Tests:** `internal/tui/listview_test.go` gained
`TestListView_SlashEntersFilterModeAndNarrowsRows`,
`_FilterIsCaseInsensitive`, `_EscClearsFilter`,
`_LettersDuringFilterModeAreTextNotCommands` -- direct mirrors of
`picker_test.go`'s existing filter tests, minus selection (List has
nothing to choose). `TestListView_LegendShowsScrollAndQuit` renamed to
`_LegendShowsScrollFilterAndQuit` and now also asserts "filter" appears.
All of `picker_test.go`'s existing filter tests continued to pass
unchanged against the refactored `filterState`-backed `PickerModel`,
confirming the extraction didn't change Picker's own behavior.

**Files:** `internal/ui/display.go`/`display_test.go`,
`internal/tui/style.go`/`style_test.go`, `internal/workflow/menu.go`/
`menu_test.go`, `internal/workflow/keymgmt_menu.go`/
`keymgmt_menu_test.go`, `cmd/clasm/main.go`.

**Dependency:** Phase 20.6 (`bucketListViewConfig`/`DisplayBuckets`, the
pattern every builder here copies) and Phase 20.12 (the Picker-tier
sweep that had to finish first).

DESIGN.md's full termlib-to-huh/bubbletea conversion punch list (Menu,
Picker, and List tiers) is now completely converted. Remaining termlib
call sites outside this punch list (e.g. `ui.PickList`/`ui.Prompt`/
`Confirm` calls not classified into any of the three tiers) stay as-is
per DESIGN.md's own note that `internal/ui` shrinks over the course of
termlib removal rather than being replaced in one step.

## Phase 20.15 — Termlib Removal, Part 1: Foundational Helpers (done)

**Status: implemented and unit-tested 2026-07-13** (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean except the
pre-existing, unrelated `version.go`). Implements DESIGN.md, "Removing
termlib: Action Wizards and Output," and DECISIONS.md, "Remove termlib
entirely: input via huh, output via io.Writer."

Landed differently from the original design in one real way, found
while implementing: `ui.Prompt`/`Confirm`/`ConfirmDestructive` don't
just need a *testable core* (the `RunXxx`/`runXxx` split used
everywhere else) -- they need their accessible-mode I/O **exposed as a
functional option** (`ui.WithIO(input, output)`, `WithConfirmIO(input,
output)`), because these three are called from dozens of call sites
spread across both `internal/ui` and `internal/workflow`, not from one
function with one obvious "core" to split. `ConfirmDestructive` also
changed shape slightly to make room for the options param: `mustMatch
...string` became `mustMatch []string, opts ...ConfirmOption` (Go
allows only one variadic per signature) -- the ~6 call sites now wrap
their arguments in a slice literal instead of passing them bare. This
option-based shape is what let Phase 20.16 thread the same input/output
pair through every intermediate "leaf" prompt function (not just the
handful anticipated below) without a second, parallel mechanism.

### Work Items

- [x] `internal/ui/prompt.go`: rebuilt `Prompt` on `huh.NewInput()`,
      with a new `WithIO(input, output) PromptOption` for accessible-mode
      testability (no separate `promptCore` needed -- see the note
      above). `WithDefault` pre-fills the field's `Value`; `WithValidator`
      becomes `.Validate()`, with a fix found via a real test failure:
      the validator itself must treat a blank submission as automatically
      valid when a default is set (skip validation, don't reject ""),
      since huh validates before default-substitution -- otherwise a
      validator that (correctly) never accepts blank input on its own
      (e.g. `validateAMIName`) blocks the default from ever being used.
- [x] `internal/workflow/confirm.go`: `Confirm` rebuilt on
      `huh.NewConfirm()` (the re-prompt-on-bad-input loop disappears --
      a toggle can't produce unrecognized input); `ConfirmDestructive`
      rebuilt on `huh.NewInput()` with no validator (a validator would
      make huh re-prompt until correct, changing the existing
      single-attempt-then-cancel semantics) -- the exact-match check
      runs after the field returns, same as before. Both take a new
      `WithConfirmIO` option instead of a testable-core split.
- [x] `internal/ui/picklist.go` and `picklist_test.go` deleted outright
      -- confirmed dead code, no production caller remained. `ErrCancelled`
      (still used by the AZ/ENA-incompatibility remediation menus) moved
      to `prompt.go` rather than being deleted with the rest of the file.
- [x] `internal/ui/color.go`: `termlib.Bold`/`Reset` replaced with local
      `ansiBold`/`ansiReset` constants; `Highlight` otherwise unchanged.
- [x] `internal/ui/display.go`: `termlib.PadRight`/`Truncate`/`Green`/
      `Red`/`Yellow` replaced with local rune-aware `padRight`/`truncate`
      helpers and local ANSI constants; `stateColor`/`instanceRow`/the
      `*ListViewConfig` builders otherwise unchanged.
- [x] `internal/workflow/progress_ticker.go`: `*termlib.Terminal` →
      `io.Writer`; `termlib.FormatDuration` → local `formatDuration`
      (same `m:ss`/`h:mm:ss`, rounded to the second). Mechanical parity
      only -- no bubbletea spinner in this pass (deferred; see TODO.md).
- [x] `internal/workflow/domain_menu.go`: `runMenuField`'s `t
      *termlib.Terminal` parameter (used only to print the static
      "(q to go back)" hint) → `io.Writer`, along with every other
      function in the file (`pickString`, `pickComparable`,
      `pickDomainItem`, `RunDomainPicker`/`runDomainPicker`,
      `NotYetImplemented`).
- [x] Every `errors.Is(err, termlib.ErrInterrupted)` check replaced with
      the equivalent `errors.Is(err, huh.ErrUserAborted)` check, per call
      site (`menu.go`'s `isExitSignal`, and the menu test files).

**Tests:** `prompt_test.go`/`confirm_test.go` rewritten to call the
public `Prompt`/`Confirm`/`ConfirmDestructive` directly with
`WithIO`/`WithConfirmIO`, matching the accessible-mode pipe pattern
already established for the Menu tier. `picklist_test.go` deleted.
`display_test.go`/`color_test.go` updated for the local constants, plus
new `TestTruncate`/`TestPadRight` parity tests (ported from termlib's
own test cases) that weren't there before. `progress_ticker_test.go`
updated to construct a `bytes.Buffer` directly instead of
`termlib.New(&buf)`, plus a new `TestFormatDuration` (also ported from
termlib's own test cases).

**Files:** `internal/ui/{prompt,picklist,color,display}.go` (+ tests),
`internal/workflow/{confirm,progress_ticker,domain_menu}.go` (+ tests).

## Phase 20.16 — Termlib Removal, Part 2: Propagate Across Action Wizards (done)

**Status: implemented and unit-tested 2026-07-13** (same clean sweep as
Phase 20.15 -- the two landed together in one sitting, since Go's
whole-module compilation meant Phase 20.15's signature changes and this
phase's propagation had to be internally consistent at every commit
boundary; see DESIGN.md's "Sequencing" note). Propagated `le
*termlib.LineEditor` removal and `t *termlib.Terminal` → `io.Writer`
across every remaining caller, then removed `termlib` from `go.mod`.

The real scope turned out larger than the original work-item list
below: `ui.Prompt`/`Confirm`/`ConfirmDestructive`'s new `WithIO`/
`WithConfirmIO` options (Phase 20.15) meant every intermediate function
between a workflow's testable core and its leaf prompt call also needed
`input io.Reader, output io.Writer` parameters threaded through --
`promptKeyPairNameOrCreate`, `promptSubnetID`, `promptSecurityGroupIDs`,
`promptIAMInstanceProfileOrCreate`, `createInstanceProfileForRole`,
`createInstanceProfileInteractive`, `promptCloudInitYAMLFile`,
`offerFstrimIfAvailable`, `confirmLifecycleChange`,
`promptOptionalDays`, `promptPositiveDays`, `removeLifecycleRule(ForRule)`,
`startEC2Instance`/`stopEC2Instance`, `terminateEC2Instance`/
`confirmTerminate`, `removeAMI`/`confirmRemoveAMI`, `deleteKeyPair`/
`confirmDeleteKeyPair`, `runLaunch`, `createAMIFromInstance`, and
`createBucket`/`configureBucketWebsite` (which previously had no
menu-tier huh.Select at all, so no `menuInput`/`menuOutput` pair to
reuse) -- none of these were anticipated in the original per-file list,
found only by tracing every `Confirm(`/`ConfirmDestructive(`/
`ui.Prompt(` call site after the mechanical sweep below and checking
whether its enclosing function had any way to reach it from a test.

A second real gap, found only by actually running the test suite (not
by static analysis): huh's accessible-mode input (`accessibility.
PromptString`, used by `Input.RunAccessible`) has **no way to surface
EOF as an error** -- it silently falls back to the field's default (or
blank) forever, unlike `termlib.LineEditor.Prompt`, which returned
`io.EOF` once piped input ran out. `TestCreateInstanceProfileForRole_
NameCollisionRetries` relied on that old behavior (a fake that always
errors, expecting the retry loop to eventually surface an error once
input was exhausted) and hung indefinitely under the new behavior
instead. Fixed by giving `fakeIAMClient` the same `createInstanceProfile
ErrOnce` shape `fakeEC2Client` already used for the analogous key-pair
test, and rewriting the test to expect a successful retry rather than
an eventual error -- the more accurate reflection of real interactive
behavior anyway (an operator retrying a genuinely duplicate name would
keep being asked forever, not have the tool give up on their behalf).

`TestImportKeyPairStandalone_PromptLabelStaysShort` (a guard against a
real, now-moot termlib bug -- `LineEditor.Prompt`'s raw-mode redraw math
assumed a prompt fit on one terminal row) was retired outright, same
treatment as `ui.PickList`'s own tests.

### Work Items

- [x] **Compute domain:** `launch_instance.go`, `launch_prompts.go`,
      `launch_execute.go`, `launch_from_cloud_init.go`,
      `create_instance_from_ami.go`, `create_instance_from_cloud_init.go`,
      `create_ami_from_instance.go`, `create_instance_profile.go`,
      `power_state.go`, `terminate_instance.go`, `remove_ami.go`,
      `manage_tags.go`, `show_cloud_init.go`, `cloud_init_export.go`,
      `instance_type_az_check.go`, `instance_type_ena_check.go`,
      `fstrim.go`, `userdata.go`, `menu.go`, `backup_archive.go`
- [x] **Key Management domain:** `keymgmt_menu.go`, `keymgmt_common.go`,
      `create_key_pair.go`, `keypair_create.go`, `keypair_delete.go`,
      `keypair_import.go`
- [x] **S3 domain:** `s3_menu.go`, `bucket_create.go`, `bucket_delete.go`,
      `bucket_website.go`, `bucket_lifecycle.go`
- [x] `cmd/clasm/main.go`: deleted `termlib.New(out)`/
      `termlib.NewLineEditor(...)` construction; `os.Stdout` passed
      directly wherever an `io.Writer` is still needed.
- [x] `go.mod`: removed the `github.com/rsdoiel/termlib` requirement via
      `go mod tidy` -- confirmed zero remaining source references first.
- [x] Full sweep: `go build ./...`, `go vet ./...`,
      `go test ./... -race`, `gofmt -l` clean (except pre-existing
      `version.go`).

**Tests:** every `_test.go` file that constructed `termlib.New(&buf)`
to capture output rewritten to use a `bytes.Buffer` directly.
`newPipeEditor` (previously wrapping a real `os.Pipe` + `termlib.
LineEditor`) rewritten to return a `(io.Writer, io.Reader, *bytes.Buffer)`
trio backed by `newHuhAccessibleInput`'s line-at-a-time reader, with the
writer and buffer deliberately the same value. Every test driving a
menu-tier huh.Select *and* one or more free-text prompts/confirms in the
same call had its two previously-independent input streams (`le` for
free text, a separate `newHuhAccessibleInput` reader for the menu) 
merged into one combined stream, in the exact order the production code
actually reads them -- verified by running the suite, not just by
tracing the code, since a wrong merge order fails loudly (wrong field
gets the wrong text) rather than silently.

**Files:** all files listed above, plus each one's corresponding
`_test.go`, plus `go.mod`/`go.sum`.

## Phase 20.17 — Chrome Standardization: Shared lipgloss Palette (done)

**Status: implemented and verified 2026-07-13.** Implements DESIGN.md,
"Chrome Standardization: A Shared lipgloss Palette," and DECISIONS.md,
"Chrome standardization: one shared indigo accent via lipgloss."

### Work Items

- [x] `internal/tui/theme.go` (new): `accentColor` (the same adaptive
      indigo huh's `ThemeCharm` uses), `borderStyle`/`titleStyle`
      `lipgloss.Style`s for `box.go`, and `Theme() *huh.Theme` — built
      from `huh.ThemeBase()` with the indigo accent applied to focused
      titles/borders/selection indicator+marker/confirm button/text
      input prompt+cursor; `Blurred` mirrors `Focused` with a hidden
      border, `Group.Title` matches `Focused.Title`.
- [x] `internal/tui/box.go`: `TopBorder`/`BottomBorder`/`Divider`/
      `SplitDivider`/`MergeDivider`/`BoxLine`/`BoxRow2` all render their
      border characters (and `TopBorder`'s title) through
      `borderStyle`/`titleStyle`. `BoxLine`/`BoxRow2` render their `│`
      per call (not cached in a package var) so they track the current
      lipgloss color profile the same way every other function in the
      file does — an earlier draft cached a package-level `boxSide`
      string rendered once at init time, which silently froze in
      whatever color profile was active at package load and stopped
      responding to later profile changes; caught via a throwaway
      sanity test that forced `lipgloss.SetColorProfile` and diffed the
      raw escape codes. Width math (`RuneLen`/`PadOrTruncate`) already
      stripped ANSI, so no change needed there.
- [x] `internal/ui/prompt.go` (`Prompt`), `internal/workflow/confirm.go`
      (`Confirm`, `ConfirmDestructive`), `internal/workflow/domain_menu.go`
      (`runMenuField`), `internal/workflow/object_browser.go`
      (`runFieldWithHelp`): each `huh.NewForm(...)` gained
      `.WithTheme(tui.Theme())`.

**Tests:** `internal/tui/box_test.go` — the four tests asserting exact/
literal border strings (`TestBottomBorder_MatchesInnerWidth`,
`TestDivider_MatchesInnerWidth`, `TestTopBorder_TitleFitsWithinWidth`,
`TestSplitAndMergeDividers_JoinAtTheMiddleColumn`) and
`TestBoxLine_PadsToInnerWidthAndAddsBorders` updated to compare against
`StripANSI(got)`. New `internal/tui/theme_test.go` confirms `Theme()`
is non-nil, `Focused.Title`'s foreground matches the shared
`accentColor`, and `Blurred.Base`'s border style is
`lipgloss.HiddenBorder()`. Full `go build ./...`, `go vet ./...`,
`go test ./... -race` sweep green; `gofmt -l .` clean except a
pre-existing, unrelated `version.go` (generated file).

**Files:** `internal/tui/{theme.go (new),theme_test.go (new),box.go,
box_test.go}`, `internal/ui/prompt.go`, `internal/workflow/{confirm,
domain_menu,object_browser}.go`, `go.mod`/`go.sum` (`lipgloss` promoted
from indirect to direct via `go mod tidy`).

**Dependency:** None — additive styling, no signature changes.

## Phase 20.18 — Progress Ticker: Real bubbletea Spinner (done)

**Status: implemented and verified 2026-07-13.** Depended on Phase
20.17 for the shared accent color (`tui.SpinnerStyle()`).

### Work Items

- [x] `internal/workflow/progress_ticker.go`: replaced the periodic
      `fmt.Fprintf` loop with a small `bubbletea` model (`progressModel`)
      pairing a `github.com/charmbracelet/bubbles/spinner.Model`
      (`spinner.MiniDot`, styled via `tui.SpinnerStyle()`) with an
      elapsed-time label recomputed on every render. Driven by its own
      `progressTickMsg` cadence (`tea.Tick(interval, ...)`) rather than
      `spinner.Model`'s built-in FPS-based tick, so the refresh rate
      stays caller/test-controlled instead of hardcoded per spinner
      style. `startProgressTicker`'s signature dropped the `interval
      time.Duration` parameter it used to take (`func(w io.Writer, label
      string) (stop func())`): under the old `fmt.Fprintf`-per-tick
      design that argument meant "how often a new status line prints"
      and all three call sites already passed the identical
      `30*time.Second`; under a real animated spinner it would have
      meant "how often the glyph advances," and 30s is far too slow to
      look like an animation, so keeping it would have left the
      argument either dead or silently wrong at every call site.
      Replaced with a package constant, `DefaultSpinnerInterval =
      120*time.Millisecond`, used internally.
- [x] `progressModel` clears itself on stop: `stop()` now sends a
      `progressStopMsg` (not a bare `p.Quit()`), which the model handles
      by rendering one final blank `View()` before returning `tea.Quit`
      -- confirmed via a throwaway sanity test capturing the raw
      program output, which showed the terminal's final control
      sequences clear the spinner's line (`\x1b[J`/`\x1b[2K`)
      rather than leaving "⠹ waiting (elapsed 0:12)" printed.
      `startProgressTicker`'s returned `stop` still blocks until the
      underlying `tea.Program`'s `Run()` goroutine has fully exited
      (same no-race-with-post-stop-output contract the old
      ticker/goroutine pair had).
- [x] `tui.SpinnerStyle()` added to `internal/tui/theme.go` alongside
      `Theme()`, returning a `lipgloss.Style` in the shared accent for
      the spinner glyph.
- [x] Updated callers (`create_ami_from_instance.go`, `show_cloud_init.go`
      -- which also dropped its now-unused `time` import --,
      `backup_archive.go`) to the new two-argument call shape.

**Tests:** `progress_ticker_test.go`'s two async tests
(`TestStartProgressTicker_PrintsPeriodically`,
`TestStartProgressTicker_StopsCleanly`) updated to drop the removed
`interval` argument and sleep in multiples of `DefaultSpinnerInterval`
instead of a caller-supplied fast interval; `TestFormatDuration`
unchanged. Full `go build ./...`, `go vet ./...`, `go test ./...
-race` sweep green.

**Files:** `internal/workflow/progress_ticker.go` (+ test),
`internal/workflow/{create_ami_from_instance,show_cloud_init,
backup_archive}.go`, `internal/tui/theme.go`.

**Dependency:** Phase 20.17 (soft).

## Phase 20.19 — object_browser.go: Bucket Pre-flight onto pickBucket (done)

**Status: implemented and verified 2026-07-13.**

### Work Items

- [x] `internal/workflow/object_browser.go`: `BrowseAndManageObjects`'s
      `selectBucket` (bare `huh.Select` + `bucketOptions`/`huh.NewOption`
      construction) replaced with `pickBucket(ctx, "Select a bucket",
      buckets)`; cancellation mapped via `cancelledIsNil(w, err)`
      instead of `huhCancelledIsNil` for this call site (`confirmLink`/
      the local-directory `huh.Input` keep `huhCancelledIsNil`,
      unaffected). `cancelledIsNil` needs a `w io.Writer` (it prints
      "Cancelled." on the way out, unlike `huhCancelledIsNil`'s silent
      return), so `BrowseAndManageObjects` gained a `w io.Writer`
      parameter (`func(ctx, w, newS3Client, buckets) error`, matching
      every sibling `S3Actions` workflow function's own shape) threaded
      from `cmd/clasm/main.go`'s existing `out`.

**Tests:** No `object_browser_test.go` existed before this change (bare
`huh.Select` fields aren't unit-tested via the accessible-mode pipe
path in this codebase the way Menu-tier `huh.Select`s are -- there was
nothing to retire or update). Full `go build ./...`, `go vet ./...`,
`go test ./... -race` sweep green.

**Files:** `internal/workflow/object_browser.go`, `cmd/clasm/main.go`.

**Dependency:** None.

---

## Phase 20.20 — Backup Archive & Trim: Reorder Prompts (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "Reorder Backup Archive & Trim's prompts."

### Work Items

- [x] `internal/workflow/backup_archive.go`: `backupArchiveAndTrim`'s
      prompt sequence reordered from instance/directory/age-days/bucket
      to instance/directory/bucket/age-days -- the S3 bucket prompt
      (and its immediately-following `BucketRegion`/`newS3Client`/
      `CheckS3BucketAccess` sequence) now runs directly after the
      directory prompt, with the age-threshold prompt moved to run last,
      immediately before the dry-run listing.

**Tests:** every existing `backup_archive_test.go` test's input string
(four `\n`-joined answers) reordered to match; assertions unchanged.

**Files:** `internal/workflow/{backup_archive.go,backup_archive_test.go}`.

**Dependency:** None.

## Phase 20.21 — Backup Archive & Trim: Recall Instance/Directory (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "Recall Backup Archive & Trim's instance/directory
choices per-instance."

### Work Items

- [x] New `internal/state` package: `State`/`BackupArchiveState`
      (`LastInstanceID`, `LastDirectoryByInstance map[string]string`),
      `DefaultPath()` (`~/.clasm_state`), `Load`/`Save` -- mirrors
      `internal/config`'s own `Load`/`DefaultPath` shape, but as its own
      app-managed file, not folded into `~/.clasm`.
- [x] `internal/tui/picker.go`: `PickerConfig` gained `InitialCursor
      int`; `NewPickerModel` positions `filter.cursor` there when it's
      in range, falling back to 0 (the pre-existing default) otherwise.
- [x] `internal/workflow/power_state.go`: `pickInstance` split into
      `pickInstance` (unchanged callers) and `pickInstanceDefaulted`
      (takes a `defaultInstanceID string`, resolves it to a row index,
      passes it as `InitialCursor`).
- [x] `internal/workflow/backup_archive.go`: new `BackupHistory`
      struct (`LastInstanceID`, `LastDirectoryByInstance`, `Save func
      (instanceID, directory string) error`); `BackupArchiveAndTrim`
      gained a `hist BackupHistory` parameter, passed through to
      `pickInstanceDefaulted` and to the directory prompt's default
      (taking priority over `backupDirRules`' Name-pattern match), and
      `hist.Save` called once both instance and directory are resolved.
      A `Save` error is reported to `w` as a warning, not returned.
- [x] `cmd/clasm/main.go`: loads `~/.clasm_state` at startup (import
      aliased `appstate` -- this file already has a local `state`
      struct variable, which would otherwise shadow the package name),
      builds the `Save` closure, wires `workflow.BackupHistory` into
      the `BackupArchiveAndTrim` action closure.

**Tests:** new `internal/state/state_test.go` (missing-file, malformed-
YAML, save-then-load round-trip, overwrite, `DefaultPath`); new
`internal/tui/picker_test.go` cases (`InitialCursor` positions the
cursor; out-of-range falls back to 0; Enter immediately selects the
pre-positioned row); new `internal/workflow/backup_archive_test.go`
cases (recalled directory takes priority over the Name-pattern rule;
`Save` is called with the right instance/directory; a `Save` error is a
warning, not fatal).

**Files:** `internal/state/{state.go,state_test.go}` (new),
`internal/tui/{picker.go,picker_test.go}`, `internal/workflow/
{power_state.go,backup_archive.go,backup_archive_test.go}`,
`cmd/clasm/main.go`.

**Dependency:** None.

## Phase 20.22 — Contextual Description Text on Menu/Picker-tier Screens (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "Contextual description text on Menu/Picker-tier
screens."

### Work Items

- [x] `internal/tui/picker.go`: `PickerConfig` gained a `Description
      string` field, rendered as its own `BoxLine` + `Divider` directly
      below the top border (mirroring `Header`'s existing shape),
      above any `Header`/rows.
- [x] `internal/tui/filter.go`: `filterableWindowHeight` gained a
      `hasDescription bool` parameter, costing the same two rows a
      header does when present; `ListViewModel`'s call site passes
      `false` (List-tier is out of scope -- tabular resource listings
      aren't "just a pick list").
- [x] `internal/workflow/domain_menu.go`: `pickString`/`pickComparable`
      (the shared Menu-tier helpers) gained a `description string`
      parameter, applied via huh's own `.Description(...)`; all 11
      call sites across the package updated with real contextual text.
      The 4 direct `huh.NewSelect` call sites not funneled through
      these helpers (`pickDomainItem`, `pickMainMenuItem`,
      `pickKeyMgmtItem`, `pickS3MenuItem`) each gained a
      `.Description(...)` call directly.
- [x] Picker-tier functions called from more than one call site with
      meaningfully different context (`pickImage`, `pickBucket`,
      `pickInstance`/`pickInstanceDefaulted`) gained a `description
      string` parameter threaded from their own callers. Picker-tier
      functions with exactly one caller (`pickInstanceProfileChoice`,
      `pickRole`, `pickSubnet`, `pickKeyPairChoice`,
      `pickKeyPairForDeletion`, `pickLifecycleRule`) got a single
      description written directly into the function.

**Tests:** new `internal/tui/filter_test.go` (description costs two
rows like a header; height never drops below the floor); new
`TestPicker_DescriptionRendersBelowTopBorder` in `picker_test.go`. No
new workflow-level tests -- the description strings are static text
threaded through already-tested call paths, not new branching logic.

**Files:** `internal/tui/{picker.go,filter.go,filter_test.go,
picker_test.go,listview.go}`, and every `internal/workflow/*.go` file
containing a Menu-tier `huh.Select` or one of the 9 Picker-tier
`tui.PickerConfig` constructor call sites (15 + 9 call sites across
~20 files).

**Dependency:** None.

## Phase 20.23 — huh Fields: Full Box Border to Match tui's Chrome (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "huh fields get a full box border to match tui's chrome."

### Work Items

- [x] `internal/tui/theme.go`: `Theme()`'s `Focused.Base`/`Focused.Card`
      now call `.Border(lipgloss.NormalBorder())` (matching `box.go`'s
      own box-drawing characters) instead of inheriting `huh.ThemeBase`'s
      left-only `ThickBorder`; `Padding(0, 1)` replaces `ThemeBase`'s
      `PaddingLeft(1)`. `Blurred.Base` still hides its border via
      `lipgloss.HiddenBorder()`, now over the full four-sided footprint.

**Tests:** verified via a throwaway test rendering `Theme().Focused.
Base.Render(...)` directly with a forced true-color profile and
inspecting the raw ANSI output -- confirms a full `┌─┐│ │└─┘` box in
the shared accent, not huh's default left-bar. Not committed as a
permanent test (a pure styling value, not branching logic); full
`go build ./...`, `go vet ./...`, `go test ./... -race` sweep green
throughout.

**Files:** `internal/tui/theme.go`.

**Dependency:** None.

---

## Phase 20.24 — Clear the Screen at Startup (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "Clear the screen at startup." Partially addresses a
combined request ("clear the screen first and take up the full height
of the terminal window"); the "full height" half was deliberately not
implemented here -- see the note below.

### Work Items

- [x] New `internal/ui/clear.go`: `ClearScreen(w io.Writer)`, sending
      `ansi.EraseEntireScreen` + `ansi.CursorHomePosition` (the same
      two sequences `tea.ClearScreen` sends internally), from
      `github.com/charmbracelet/x/ansi` (already an indirect
      dependency via `bubbletea`, promoted to direct via `go mod
      tidy`).
- [x] `cmd/clasm/main.go`: calls `ui.ClearScreen(out)` once, after the
      `-help`/`-license`/`-version` early exits (which stay
      script/pipe-friendly) but before any other output, including
      error paths.

**Not done, deliberately deferred:** making the domain picker (or the
Menu tier generally) visually fill the terminal's full height. The
domain picker is a `huh.Select` (Menu tier), which -- unlike the
Picker/List/Manager tier's bubbletea components -- has no built-in
concept of "occupy the full window height"; every other Menu-tier
screen in the app (S3/EC2/Key Management menus) is the same compact,
content-sized form, so making only the root domain picker full-height
would be visually inconsistent with every menu one level deeper, the
opposite of the consistency this session's other chrome work has been
building toward. Revisit if this turns out to be what "full height"
specifically meant, once clarified.

**Tests:** `internal/ui/clear_test.go` asserts the exact bytes written
match `ansi.EraseEntireScreen + ansi.CursorHomePosition`.

**Files:** `internal/ui/{clear.go,clear_test.go}` (new),
`cmd/clasm/main.go`, `go.mod`/`go.sum`.

**Dependency:** None.

## Phase 20.25 — Bucket Picker for Backup Archive & Trim (done)

**Status: implemented and verified 2026-07-13.** Implements
DECISIONS.md, "Bucket picker for Backup Archive & Trim."

### Work Items

- [x] `internal/workflow/backup_archive.go`: new `bucketChoice` type
      (`label`, `name`, `other bool`) and `promptBackupBucket` function
      -- fetches buckets via `inventory.ListBuckets`, offers them as a
      filterable `huh.Select` (via the existing `pickComparable`
      helper, so `'/'` filtering and accessible-mode pipe-testing come
      for free) plus an "Other (type a bucket name)" entry, falling
      back entirely to the original free-text `ui.Prompt` when the
      listing fails or is empty.
- [x] `backupArchiveAndTrim`'s bucket-resolution line now calls
      `promptBackupBucket(ctx, w, s3Client, newS3Client, input,
      output)` instead of a bare `ui.Prompt("S3 bucket", ...)` --  no
      other signature changes; bucket resolution stays in the same
      place in the same testable core.

**Tests:** three new cases in `backup_archive_test.go`
(`TestBackupArchiveAndTrim_BucketPickerOffersKnownBuckets`,
`..._BucketPickerOtherFallsBackToFreeText`,
`..._BucketPickerFallsBackToFreeTextOnListError`), each verifying the
resulting bucket name via the upload command's `s3://<bucket>/...`
destination. Every pre-existing test in this file continues to pass
unchanged -- none populate `fakeS3Client.buckets`, so `ListBuckets`
returns an empty list and they all naturally exercise the free-text
fallback branch, exactly as before this change.

**Files:** `internal/workflow/{backup_archive.go,backup_archive_test.go}`.

**Dependency:** None.

## Phase 20.26 — Full-height Menu Tier

**Status: designed 2026-07-20, not yet implemented.** Resolves Phase
20.24's deferred "full height" request. Implements `DESIGN.md`,
"Full-height Menu Tier," and `DECISIONS.md`, "Full-height Menu tier via
live `WindowSizeMsg` tracking, applied at every depth."

### Work Items

- [ ] `internal/workflow/domain_menu.go`: extend `quitKeyGuard` (or a
      sibling wrapper covering the same `*huh.Form` interface) to
      intercept `tea.WindowSizeMsg` and call `form.WithHeight(msg.Height
      - reserved)` on every resize, where `reserved` accounts for
      `runMenuField`'s own `fmt.Fprintln(w, hint)` line printed outside
      the form.
- [ ] `runMenuField`: route both the filtering and non-filtering
      `huh.Field` paths through the same wrapper, so every Menu-tier
      `huh.Select` (root domain picker, S3/EC2/Key Management submenus,
      `pickString`/`pickComparable` call sites) gets full-height sizing
      uniformly — no per-call-site change needed since `runMenuField` is
      the shared entry point.
- [ ] Confirm the reserved-line count against `tui.Theme()`'s actual
      border/padding plus huh's own `titleFooterHeight`, so the combined
      output doesn't exceed the terminal height by one line.

**Tests:** exercise `WithHeight` being called with the expected value
for a given `tea.WindowSizeMsg`, and that content shorter than the
window still renders a fixed-height box (`lipgloss.Style.Height`
padding). Accessible-mode (pipe-tested) call sites are unaffected --
they never construct a `tea.Program`, so this whole path doesn't apply
to them.

**Files:** `internal/workflow/domain_menu.go` (+ test).

**Dependency:** None.

## Phase 20.27 — Launch Templates (done)

**Status: implemented and unit-tested 2026-07-20** (`go build ./...`,
`go vet ./...`, `go test ./... -race` all clean; `gofmt -l` clean except
the pre-existing, unrelated `version.go`); not yet verified against real
AWS -- see Phase 22. v0.0.2's headline feature, confirmed directly
2026-07-20 (v0.0.1 is already piloting in production, unreleased -- no
git tag yet). Folds in the IMDSv2 bug fix (TODO.md, Bugs) as one design/
implementation pass, since both touch the same `MetadataOptions`
concept. Implements `DESIGN.md`, "Launch Templates," and `DECISIONS.md`,
"Launch templates: build directly from cloud-init YAML, diff-then-new-
version sync, fold in IMDSv2." Deliberately excludes TODO.md's
tags-screen fix, backup-bucket-default, and top-level cross-resource tag
management -- those get their own design/decision/plan pass after this
one lands.

**Effort:** ~24 hours estimated (comparable scope to Phase 20.1: a new
client surface, seven new interactive workflows, a diff mechanism, plus
three IMDSv2 call sites)
**Priority:** High
**Files:**
`internal/awsclient/ec2.go`/`logging_ec2.go` (7 new `EC2API` methods +
logging wrapper),
`internal/inventory/launch_templates.go` (new -- `LaunchTemplate`
list-tier type, per-version detail type, `ListLaunchTemplates` +
version-detail fetch, aggregated across regions like `Image`/
`Instance`),
`internal/workflow/show_launch_template.go` (new -- List/Show),
`internal/workflow/launch_template_create.go` (new -- Create from
Cloud-Init YAML),
`internal/workflow/launch_from_template.go` (new -- Create EC2 Instance
from Launch Template, naming to match the existing
`launch_from_cloud_init.go` convention),
`internal/workflow/launch_template_sync.go` (new -- Sync/diff/no-op
detection),
`internal/workflow/launch_template_manage.go` (new -- Promote to
Default, Delete Version(s), Delete Template),
`internal/workflow/launch_execute.go` (IMDSv2 on plain `RunInstances`),
`internal/workflow/menu.go` (7 new `mainMenuItems` entries + matching
`MenuActions` fields),
`cmd/clasm/main.go` (wiring),
`go.mod`/`go.sum` (`github.com/aymanbagabas/go-udiff` promoted from
indirect to direct -- already present transitively via
`charmbracelet/x/exp/teatest`, used by `internal/filemanager`'s own
tests, so no new dependency is actually being introduced).

### Work Items

- [x] **EC2 client surface:** add `CreateLaunchTemplate`,
      `CreateLaunchTemplateVersion`, `DescribeLaunchTemplates`,
      `DescribeLaunchTemplateVersions`, `ModifyLaunchTemplate`,
      `DeleteLaunchTemplate`, `DeleteLaunchTemplateVersions` to
      `EC2API`, mirrored into `logging_ec2.go`.
- [x] **Data model:** `inventory.LaunchTemplate` (`TemplateID`, `Name`,
      `DefaultVersion`, `LatestVersion`, `Region`, `Project`,
      `Environment`) via `DescribeLaunchTemplates`, aggregated across
      regions concurrently (same shape as `ListImages`/`ListInstances`);
      a per-version detail type via `DescribeLaunchTemplateVersions`
      (version number, create date, AMI, instance type, IAM instance
      profile, security groups, subnet, tags, `MetadataOptions`) --
      deliberately not the full `RequestLaunchTemplateData` surface.
- [x] **List Launch Templates:** fold into the existing "Show resource
      lists" List-tier display as a new resource type, alongside
      instances/AMIs -- not a separate top-level action.
- [x] **Show Launch Template:** pick a template, then a version
      (`$Default` pre-selected), display the curated detail fields
      above. Flags the template passively (no separate audit action)
      if `MetadataOptions.HttpTokens != required`.
- [x] **Create Launch Template from Cloud-Init YAML:** reuse Feature
      3's file-path prompt (file only, never inline text) plus its
      AMI/instance-type/subnet/security-group/IAM-profile/tag prompts;
      `CreateLaunchTemplate` (implicitly creates version 1) with
      `MetadataOptions` forced to required, unconditionally -- not a
      prompt.
- [x] **Create EC2 Instance from Launch Template:** pick a template,
      then a version -- prompt pre-filled to `$Default` (same
      recalled-but-overridable shape as Backup Archive & Trim's
      instance/directory defaults) but editable to an explicit version
      number or `$Latest`. Collects nothing else; `RunInstances` via
      `LaunchTemplateSpecification`. A third peer entry alongside
      Features 2 and 3, not a hybrid of either.
- [x] **Sync Cloud-Init YAML to a Template:** pick a template + version
      to compare against, pick a YAML file; decode the version's
      `UserData` and compare against the file's content. Identical →
      report "no changes -- nothing to sync," stop, no version created.
      Different → render a plain-text unified diff via
      `go-udiff.Unified(oldLabel, newLabel, old, new string) string`,
      require explicit confirmation, then `CreateLaunchTemplateVersion`.
      Never auto-promotes the new version to default.
- [x] **Promote Launch Template Version to Default:**
      `ModifyLaunchTemplate` with `DefaultVersion` set to the chosen
      version -- its own explicit action, never a side effect of Sync.
- [x] **Delete Launch Template Version(s):** prune specific versions
      (`DeleteLaunchTemplateVersions`) without touching the whole
      template. Same safety-first shape as Feature 9 (Remove AMI): show
      what would be deleted, extra warning if tagged
      `Environment=production`, type-to-confirm. Also reports any
      per-version failures AWS returns
      (`UnsuccessfullyDeletedLaunchTemplateVersions`), not just a bare
      success count.
- [x] **Delete Launch Template:** whole-template delete
      (`DeleteLaunchTemplate`), same safety-first shape.
- [x] **Tagging:** `CreateLaunchTemplate`'s `TagSpecifications` reuses
      `launch_execute.go`'s existing `buildTagSpecification(types.ResourceTypeLaunchTemplate,
      tags)` unchanged.
- [x] **IMDSv2 (closes the TODO.md bug):** `launch_execute.go`'s
      `RunInstances` call (Features 2 and 3) gains
      `MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens:
      types.HttpTokensStateRequired}` -- previously set no
      `MetadataOptions` at all. Every new template's
      `RequestLaunchTemplateData.MetadataOptions` gets
      `types.LaunchTemplateInstanceMetadataOptionsRequest{HttpTokens:
      types.LaunchTemplateHttpTokensStateRequired}` unconditionally --
      these are two distinct SDK enum types for the same concept,
      confirmed by reading `enums.go`, not assumed. Subnet placement in
      `RequestLaunchTemplateData` has no flat `SubnetId` field (unlike
      `RunInstancesInput`) -- it must go through one `NetworkInterfaces`
      entry, and once that's used, security groups must move into that
      same entry's `Groups` field rather than the top-level
      `SecurityGroupIds` (AWS's own documented constraint, confirmed by
      reading the SDK's field comments).
- [x] `menu.go`/`main.go`: wire the seven new actions into
      `mainMenuItems`/`MenuActions` (List Launch Templates folds into
      the existing "Show resource lists" entry instead of adding an
      eighth).

**Tests:** each new workflow gets an accessible-mode pipe-tested core,
matching every existing Menu-tier workflow's convention.
`launch_execute_test.go`'s existing `fakeEC2Client` embeds
`awsclient.EC2API`, so widening the interface doesn't break any
existing test; the new launch-template methods get added to that same
shared fake rather than a second one. Specific cases to cover: Sync's
identical-content-skips-a-version branch and different-content-shows-
a-diff-then-creates-a-version branch; the plain-`RunInstances` and
new-template `MetadataOptions` both come back `required` in the
captured request; Delete Version(s)/Delete Template's
`Environment=production` extra-warning gate.

**Dependency:** None (builds on the existing EC2 client/inventory/
Menu-tier conventions; does not depend on Phase 20.26).

---

## Phase 21 — CloudFront Domain

**Status: someday/maybe -- not on the active roadmap, no committed
timeline (revised 2026-07-09 from "postponed to a later version," see
DECISIONS.md).** No code written; the `CloudFront` domain-picker entry
was removed rather than left wired to `NotYetImplemented`, so the 0.0.1
UI doesn't expose a menu item that goes nowhere. The design below stays
valid reference for if this is ever picked back up, but it is not
queued as "next" behind anything currently planned (Phase 20.1 is the
active next-release work).

**Effort:** ~8 hours (implementation) + real-AWS verification, now
folded into this phase's own scope rather than Phase 22's -- see below
**Priority:** Deferred (someday/maybe)
**Files:** `internal/awsclient/cloudfront.go`,
`internal/inventory/distributions.go`,
`internal/workflow/{distribution_create,distribution_invalidate}.go`

Implements `DESIGN.md` Features 22-25.

### Work Items

- `CloudFrontAPI` interface: `ListDistributions`, `GetDistribution`,
  `CreateDistribution`, `CreateOriginAccessControl`,
  `CreateInvalidation`, `GetInvalidation`
- Single `us-east-1` client construction — no per-region fan-out;
  CloudFront's control plane is global (see `DESIGN.md`, "Navigation:
  Domain Picker")
- `ListDistributions(ctx)` (`internal/inventory`)
- Show Distribution Detail: read-only, `cloudfront:GetDistribution`
- Create Distribution: pick or create a bucket (hands off to Phase 20's
  Create Bucket), create or reuse an Origin Access Control for that
  bucket, prompt default root object + optional alternate domain
  name(s), confirm (billable-infrastructure notice, plain confirm not
  type-to-confirm), `cloudfront:CreateDistribution`, then update the
  bucket policy scoped to this distribution's ARN (`s3:PutBucketPolicy`),
  then poll (unbounded) until `Deployed`
- Invalidate Cache Paths: pick a distribution, prompt path pattern(s)
  (default `/*`), confirm, `cloudfront:CreateInvalidation`, poll until
  `Completed`
- Wire into the domain picker from Phase 18
- Real-AWS verification for this domain (create a distribution for a
  real test bucket, verify it actually serves content, invalidate,
  confirm the cache refreshes) -- moved here from Phase 22 (see below)
  now that CloudFront is someday/maybe rather than queued as "next";
  Phase 22 no longer needs to wait on this phase to close out

**Tests:** fakes for each new CloudFront call; OAC-then-bucket-policy
sequencing; poll-until-`Deployed`/`Completed` with bounded test timeouts

**Dependency:** Phase 18, Phase 20 (Create Distribution hands off to
Create Bucket)

---

## Phase 22 — Real-AWS Testing: Key Management, S3

**Effort:** ~6 hours
**Priority:** High
**Files:** `TEST_PLAN_REAL_AWS.txt` (extended with new sections)

Mirrors Phase 16's manual-verification approach, extended to Key
Management and S3. Independent of Phase 16/17 (Compute's own
verification and Bash retirement) — see `DECISIONS.md`, 2026-07-02. No
longer covers CloudFront (see `DECISIONS.md`, "Demote CloudFront to
someday/maybe...") — that verification now lives in Phase 21 itself,
whenever it's picked up, so this phase isn't blocked on a someday/maybe
item.

### Work Items

- Extend `TEST_PLAN_REAL_AWS.txt` with sections for Key Management
  (create/import/delete against real AWS, all four regions) and S3
  (create bucket, configure website hosting, sync a small test site,
  browse/delete objects)
- Run manually against the real AWS account, same `[ok]`-marker
  convention as Phase 16
- Update `TEST_PLAN_REAL_AWS.txt` if the Go CLI's exact prompts/flow
  differ from what's documented

**Dependency:** Phase 19, 20

---

## Deferred to a Later Version (Phase 23+, not scheduled)

Not part of v1/v2 — see `DECISIONS.md`, "V1 scope: ship the four primitives
first, defer composite workflows", "Add Show/Export Cloud-Init as a v1
primitive", "Add Backup Archive & Trim as a v1 primitive", "Add Rename
Instance as a v1 primitive; AMI Name is immutable", "Add Create EC2
Instance from Cloud-Init YAML as a v1 primitive", "Add Start/Stop/
Terminate EC2 Instance as v1 primitives", "Structure workflows for future
record/replay", "Redesign navigation as a domain picker...", "CloudFront +
OAC by default for static websites", and `DESIGN.md`, "Deferred to a
Later Version". Recorded here so they're scheduled deliberately once
Phase 16/22 pass, not lost:

- **Recorded Scripts ("session playbooks")** — capture an interactive
  session's actions as an editable, templated YAML script and replay it
  later, with the same confirmation gates as interactive mode always
  enforced (never bypassable for `Environment=production`). Phases
  4-13 are already structured (params-struct/confirm-gate seam) to
  support this without rework once it's built; whether Phases 19-21's
  new workflows get the same seam is an open question for when this is
  actually built, not decided now
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
- **ACM certificate provisioning** for a CloudFront distribution's
  alternate domain name — Phase 21 assumes a matching certificate already
  exists in `us-east-1`; requesting/validating a new one is out of scope
- **CloudFront functions / Lambda@Edge / WAF association** — Phase 21
  creates a plain S3-origin distribution only
- **S3 bucket versioning and lifecycle rules** — Phase 20's Create Bucket
  uses default settings (no versioning, no lifecycle policy)
- **`Environment=production` safety-gate extension** to Delete Key Pair,
  bucket/object deletion, or distribution changes — not extended in
  Phases 19-21; a candidate for a later pass (see `DESIGN.md` Feature 26)

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
| Phase 18 | High | 4h | Phase 14 (runs alongside 16/17) |
| Phase 19 | Medium | 6h | Phase 18 |
| Phase 20 | Medium | 16h | Phase 18 |
| Phase 20.1 | High | 24h | Phase 20 |
| Phase 21 | Deferred (someday/maybe) | 8h | Phase 18, Phase 20 |
| Phase 22 | High | 6h | Phase 19, 20 |
| Phase 23+ | Deferred | — | Phase 16, 22 (see above) |
