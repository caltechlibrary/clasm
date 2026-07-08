
# Action items

- [x] UI bug, we should check to make sure aws cli is on remote system before invoking it, that way we can explicitly say it is missing and how to install it -- implemented as `CheckAWSCLIAvailable` (`internal/workflow/backup_cli_check.go`), see DECISIONS.md, "Preflight check: AWS CLI availability before Backup Archive & Trim" (2026-07-02).



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

## Not yet started

- CloudFront domain (PLAN.md Phases 21-22): designed in DESIGN.md/PLAN.md,
  no code written yet -- needs scope questions answered before starting

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
- [ ] Manage Bucket Lifecycle Policies' guided flow doesn't locally
      validate that transition-days is less than expiration-days before
      calling AWS (a real constraint -- `PutBucketLifecycleConfiguration`
      rejects the opposite ordering). Currently surfaces AWS's raw error
      message instead of catching it locally first, the way
      `validateBucketName` does for Create Bucket. Found during Phase
      20's real-AWS verification 2026-07-08 (DECISIONS.md); not fixed
      yet, workaround is choosing valid test values.
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
