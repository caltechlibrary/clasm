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

## Phase 20 — S3 Domain (Buckets & Static Websites)

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

- Broaden the `S3API` interface beyond Feature 11's `HeadObject`-only
  scope: `ListBuckets`, `GetBucketWebsite`, `CreateBucket`,
  `PutPublicAccessBlock`, `PutBucketWebsite`, `PutObject`,
  `ListObjectsV2`, `DeleteObject`, `PutBucketTagging`, `GetBucketTagging`,
  `GetBucketLifecycleConfiguration`, `PutBucketLifecycleConfiguration`
  (`GetBucketLocation` already exists; `PutBucketPolicy`/`GetObject` are
  NOT added — the former is only needed by the deferred public-read
  opt-out, the latter isn't needed since object content is never
  downloaded, only `HeadObject` metadata)
- `ListBuckets(ctx)` (`internal/inventory/buckets.go`) with per-bucket
  region (`BucketRegion`), static-website-hosting status
  (`GetBucketWebsite`), and `Purpose` tag (`GetBucketTagging`) — all
  three enrichment calls on a region-scoped client via `newS3Client`,
  never the global client, per the established `MovedPermanently`
  lesson from Backup Archive & Trim — treat `NoSuchWebsiteConfiguration`
  and a missing/absent `Purpose` key (or `NoSuchTagSet`) as "not
  configured"/"untagged," not failures
- Create Bucket: prompt name (validated locally against S3 naming rules)
  + region + purpose (Website/Backup/Internal pick list), `s3:CreateBucket`,
  `s3:PutPublicAccessBlock` (all four settings on), then
  `s3:PutBucketTagging` with `Purpose: <choice>`
- Configure Static Website Hosting: pick bucket, prompt index/error
  documents, `s3:PutBucketWebsite`. **Public-read bucket policy opt-out
  deferred** (DECISIONS.md) — only the default private-bucket path ships
  in this phase; where CloudFront hand-off would go, print that
  CloudFront isn't implemented yet (Phase 21)
- Sync Local Directory to Bucket: dry-run diff (by key + size) against
  the local directory, confirm (plain y/n), upload new/changed
  (`s3:PutObject`, per-file progress line matching Backup Archive &
  Trim's established convention), then a **separate**, stronger
  `ConfirmDestructive` (type the bucket name) gate for bucket-only
  objects (`s3:DeleteObject`) — never bundled into the upload
  confirmation
- Browse/Manage Objects: **optional key-prefix filter added**
  (DECISIONS.md) before listing; paginated object listing (reuse Phase
  15's PickList pagination for >50 items, unchanged), metadata display,
  per-object delete with a plain yes/no confirm
- **New: Manage Bucket Lifecycle Policies** (`bucket_lifecycle.go`,
  DESIGN.md Feature 21.1): pick a bucket, `s3:GetBucketLifecycleConfiguration`
  (`NoSuchLifecycleConfiguration` = no rules yet, not an error), branch
  on the bucket's `Purpose` tag —
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
- Wire into the domain picker from Phase 18, following Phase 19's
  `KeyMgmtActions`/`RunKeyMgmtMenu` shape (`S3Actions`/`RunS3Menu` — six
  menu items: Show resource lists, Create Bucket, Configure Static
  Website Hosting, Sync Local Directory to Bucket, Browse/Manage
  Objects, Manage Bucket Lifecycle Policies, Back to domain picker)

**Tests:** fakes for each new S3 call covering success/error paths
(bucket-name-taken, website-not-configured treated as non-error, sync
diff correctness, upload/delete confirmations never bundled, prefix
filter narrows the object listing, lifecycle guided-vs-generic branch
selection by `Purpose` tag, rule add/edit/remove round-trips through the
fetch-modify-PutBack cycle correctly) — TDD: write each test first,
confirm it fails, then implement.

**Dependency:** Phase 18

---

## Phase 21 — CloudFront Domain

**Effort:** ~8 hours
**Priority:** Medium
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

**Tests:** fakes for each new CloudFront call; OAC-then-bucket-policy
sequencing; poll-until-`Deployed`/`Completed` with bounded test timeouts

**Dependency:** Phase 18, Phase 20 (Create Distribution hands off to
Create Bucket)

---

## Phase 22 — Real-AWS Testing: Key Management, S3, CloudFront

**Effort:** ~6 hours
**Priority:** High
**Files:** `TEST_PLAN_REAL_AWS.txt` (extended with new sections)

Mirrors Phase 16's manual-verification approach, extended to the three
new domains. Independent of Phase 16/17 (Compute's own verification and
Bash retirement) — see `DECISIONS.md`, 2026-07-02.

### Work Items

- Extend `TEST_PLAN_REAL_AWS.txt` with sections for Key Management
  (create/import/delete against real AWS, all four regions), S3 (create
  bucket, configure website hosting, sync a small test site, browse/
  delete objects), and CloudFront (create a distribution for a real test
  bucket, verify it actually serves content, invalidate, confirm the
  cache refreshes)
- Run manually against the real AWS account, same `[ok]`-marker
  convention as Phase 16
- Update `TEST_PLAN_REAL_AWS.txt` if the Go CLI's exact prompts/flow
  differ from what's documented

**Dependency:** Phase 19, 20, 21

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
| Phase 20 | Medium | 10h | Phase 18 |
| Phase 21 | Medium | 8h | Phase 18, Phase 20 |
| Phase 22 | High | 6h | Phase 19, 20, 21 |
| Phase 23+ | Deferred | — | Phase 16, 22 (see above) |
