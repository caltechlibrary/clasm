
# Action items

- [x] UI bug, we should check to make sure aws cli is on remote system before invoking it, that way we can explicitly say it is missing and how to install it -- implemented as `CheckAWSCLIAvailable` (`internal/workflow/backup_cli_check.go`), see DECISIONS.md, "Preflight check: AWS CLI availability before Backup Archive & Trim" (2026-07-02).

## UX improvements

- [ ] We have "exit", "cancel" (which sometimes is the same", "back to ..." for navigating the heirachy of menus (workflows). This should be more consistant. "cancel" should mean aborting an action not closing a particular menu select an action or workflow. Since the application is mostly a list of things then either a sub list of things or an action I think we should standardize that heirarchy so when you close a nested menu list it just backs you up to the menu you previously were on. We should standardize either on "quit" or "exit" to close a menu when done. I'm OK with standardizing on "quit" but right now we have two many words meaning almost the same thing. When you quit all the menu's you've finally exit the TUI.


## Done (Go rewrite — see PLAN.md, DECISIONS.md 2026-07-01)

PLAN.md Phases 0 through 17 are implemented, tested, and verified against
real AWS as of 2026-07-08 (`go build ./...`, `go vet ./...`,
`go test ./... -race`, `gofmt -l` all clean; `TEST_PLAN_REAL_AWS.txt`
fully checked off, 112/112 items).

- [x] Work through `TEST_PLAN_REAL_AWS.txt` against real AWS, both
      configured regions (us-west-1, us-west-2 -- narrowed from four; see
      DECISIONS.md, "Narrow configured regions to us-west-1/us-west-2")
- [x] Phase 16 (PLAN.md): manual test plan fully run, no open gaps
- [x] Phase 17 (PLAN.md): documentation pass + retire
      `ec2_ami_manager.bash`/`ami_copy.bash`/`tests/*.bats` -- done
      2026-07-08, see DECISIONS.md, "Retire ec2_ami_manager.bash,
      ami_copy.bash, and the Bash test suite"
- [x] Phase 19 (PLAN.md): Key Management domain (list/create/import/
      delete key pairs) -- done 2026-07-08, wired into the domain picker
- [x] Phase 20 (PLAN.md): S3 domain (buckets, static website hosting,
      directory sync, object browsing, lifecycle policies) -- done and
      real-AWS verified 2026-07-08, wired into the domain picker. One
      real bug found and fixed during verification (removing a bucket's
      last lifecycle rule now correctly calls `DeleteBucketLifecycle`
      instead of an invalid empty-`Rules` `PutBucketLifecycleConfiguration`
      -- see DECISIONS.md, "Clear a bucket's lifecycle configuration via
      DeleteBucketLifecycle, not an empty PutBucketLifecycleConfiguration").
- [x] Manage Bucket Lifecycle Policies' guided flow now validates locally
      that a transition's days is strictly less than the rule's
      expiration days (and vice versa in the generic editor), instead of
      surfacing AWS's raw rejection -- see `validateLessThan`/
      `validateGreaterThan` in `internal/workflow/bucket_lifecycle.go`.
      Closes the gap noted below under "Discussed but not yet designed/
      implemented" as of 2026-07-08.
- [x] Manage Bucket Lifecycle Policies' "View rule details" action: pick
      a rule and see its full expiration/transition/filter configuration
      without entering the Add/Edit flow; loops back to the action menu
      instead of exiting the workflow.
- [x] Delete Bucket: refuses a non-empty bucket (reports the object
      count) and gates the irreversible `s3:DeleteBucket` call with
      `ConfirmDestructive` (type the bucket name back).
- [x] Delete Objects by Prefix: bulk-deletes every object under a key
      prefix (blank prefix = whole bucket) without needing a local
      directory to diff against the way Sync does; gated the same way,
      typing the prefix (or bucket name) back to confirm.
- [x] `ui.PickList` filter-as-you-type: non-numeric input narrows any
      picker's list by a case-insensitive label substring match (bucket
      lists, instance lists, etc.), and the bucket list itself is now
      returned sorted alphabetically by `inventory.ListBuckets`.
- [x] PLAN.md Phase 20.1 (DESIGN.md Features 21.2-21.8): the S3 domain's
      UI/UX pass -- one interactive "Browse & Manage Objects" file
      manager (`internal/filemanager`, a scoped `bubbletea` `Model`)
      replacing Sync Local Directory to Bucket, Browse/Manage Objects,
      and Delete Objects by Prefix. Single-pane (bucket only) or
      double-pane (bucket + linked local directory); tagging, per-level
      substring filter, Upload/Download/Delete/Show-metadata actions
      with a Confirm/ConfirmDestructive/progress overlay, a dedicated
      Sync action (whole-tree diff against the bucket, reusing the new
      `internal/s3diff` package's `Compute`/`WalkLocalTree`/
      `ListAllBucketObjects`/`UploadFile` -- extracted from the retired
      wizard rather than reimplemented), Find (recursive glob-on-
      basename search), and a `:`-prefixed command line mirroring every
      hotkey. `s3:GetObject` added to `S3API` for Download, completing
      Create/Update/Read/Delete parity. huh added as a dependency for
      the screen's own bucket-selection pre-flight
      (`internal/workflow/object_browser.go`) -- every other S3 wizard
      stays on termlib. Tests use `github.com/charmbracelet/x/exp/
      teatest` (resolves PLAN.md's open testing question); `go test
      -race` caught one real bug during development (a background
      action goroutine mutating pane state directly instead of only
      sending over its progress channel -- fixed). Not yet verified
      against real AWS -- see PLAN.md Phase 22.

## Someday/maybe (not on the active roadmap)

- CloudFront domain (PLAN.md Phase 21): designed in DESIGN.md/PLAN.md,
  no code written yet. Postponed by the user (2026-07-09), then further
  demoted the same day from "postponed to a later version" to
  someday/maybe -- no committed timeline (see DECISIONS.md, "Demote
  CloudFront to someday/maybe..."). Removed from `DomainActions`/the
  domain picker rather than left as a "not yet implemented" placeholder
  entry, so 0.0.1 doesn't expose a menu item that goes nowhere. The
  design in DESIGN.md/PLAN.md stays valid reference for if this is ever
  picked back up. Phase 22 (real-AWS testing for Key Management/S3) no
  longer depends on it.

## Postponed to a later version

- UI/UX overhaul, remaining scope: the S3 domain's file manager (see
  "Done" above) is the only part of `internal/workflow` migrated off
  termlib so far. `huh`/`bubbletea` adoption elsewhere -- Compute, Key
  Management, the other ~35 wizard call sites -- is still deliberately
  deferred; nothing about finishing the S3 domain commits to migrating
  the rest.

## Discussed but not yet designed/implemented

- [x] Pre-flight sanity check before `RunInstances`: cross-reference the
      picked AMI's `EnaSupport` against the picked instance type's ENA
      requirement. Both known failure classes from this session are now
      implemented: instance-type/Availability-Zone (DECISIONS.md,
      "Pre-flight check: instance type vs. subnet Availability Zone") and
      instance-type/AMI-ENA-support (DECISIONS.md, "Pre-flight check:
      instance type vs. AMI ENA support"). If a third incompatibility
      class turns up, that's the point to reconsider a shared framework,
      not before (see either decision's "Rejected alternatives").
- [ ] Retry-on-launch-failure (general case): instead of bouncing back to
      the main menu on any `RunInstances` error, keep the already-collected
      params and let the operator re-enter just the field that's likely
      wrong instead of re-doing the whole launch flow. Granularity not yet
      decided (just Instance type, or any collected field). NOTE: the
      instance-type/AZ pre-flight check above already does a scoped version
      of this (change instance type / pick a different subnet / abort) for
      that one failure class, pre-flight rather than reactive -- this item
      is about generalizing to *any* RunInstances failure, not just that one.

## Nice to have

- [ ] More color usage will make the interface easier to read, we can show relationship between menu items using color to group
- [ ] For actions that take more than a few minutes, a spinner that shows progress would be nice
- [ ] Bulk object delete (the file manager's Delete/Sync actions, `internal/filemanager`) currently loops one `s3:DeleteObject` call per key. `github.com/peak/s5cmd/v2`'s `storage.S3.MultiDelete` batches keys into groups of up to 1000 and calls the batch `s3:DeleteObjects` API in parallel chunks -- not importable directly (aws-sdk-go v1 + urfave/cli coupling, vs. this project's aws-sdk-go-v2), but `aws-sdk-go-v2`'s `s3` package already exposes `DeleteObjects`, so the same batching pattern could be reimplemented natively without a new dependency. Evaluated 2026-07-09, flagged again as an open question in PLAN.md Phase 20.1's work items, still not started.

## Superseded (Bash version — retired 2026-07-08, see DECISIONS.md)

Real-world use surfaced three bugs (`eval` quoting crash in the interactive
picker, a locale-dependent `grep` failure, and a malformed AWS CLI tag
string that silently prevented AMI creation) that led to the decision to
retarget to Go rather than keep patching. `ec2_ami_manager.bash`,
`ami_copy.bash`, `ami_copy_basic_steps.md`, and the BATS suite have now
been deleted from this repo (see DECISIONS.md, "Retire
ec2_ami_manager.bash, ami_copy.bash, and the Bash test suite") -- the
items below are historical record only, not actionable.

- [x] Phase 5b (PLAN.md, Bash version): port ami_copy.bash's volume-size
      estimate, SSM fstrim step, unbounded creation polling, and
      Invenio-RDM running-instance guidance into ec2_ami_manager.bash's
      Phase 5 workflow. Implemented and unit-tested
      (tests/test_create_ami.bats, 27 tests).
- [ ] ~~Manually verify ec2_ami_manager.bash's create-AMI workflow against a
      real EC2 instance~~ — superseded; verification now targets the Go
      binary (see PLAN.md Phase 16)
- [ ] ~~Write BATS tests for Phase 6 (AMI removal) and Phase 7 (main
      menu)~~ — superseded; those workflows get Go tests instead
