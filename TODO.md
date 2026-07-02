
# Action items

## Now testing (Go rewrite — see PLAN.md, DECISIONS.md 2026-07-01)

PLAN.md Phases 0 through 15.2 are implemented and green (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). The user is
actively running `bin/awsops` against a real AWS account, working through
`TEST_PLAN_REAL_AWS.txt`.

- [ ] Continue working through `TEST_PLAN_REAL_AWS.txt` against real AWS,
      both configured regions (us-west-1, us-west-2 -- narrowed from four; see DECISIONS.md, "Narrow configured regions to us-west-1/us-west-2"); mark items `[ok]` as confirmed (existing markers
      must be preserved, not overwritten)
- [ ] Phase 16 (PLAN.md): once the manual test plan is fully run, close
      out any gaps it surfaces
- [ ] Phase 17 (PLAN.md): documentation pass + retire `ec2_ami_manager.bash`/
      `ami_copy.bash`/`tests/*.bats` once the Go binary has reached parity
      and passed the manual test plan

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

## Superseded (Bash version — kept for reference, see DECISIONS.md)

Real-world use surfaced three bugs (`eval` quoting crash in the interactive
picker, a locale-dependent `grep` failure, and a malformed AWS CLI tag
string that silently prevented AMI creation) that led to the decision to
retarget to Go rather than keep patching. `ec2_ami_manager.bash` and its
BATS suite remain untouched as the working spec/reference until Phase 17
retires them.

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
