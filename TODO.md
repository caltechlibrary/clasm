
# Action items

## Now testing (Go rewrite — see PLAN.md, DECISIONS.md 2026-07-01)

PLAN.md Phases 0 through 15.2 are implemented and green (`go build ./...`,
`go vet ./...`, `go test ./... -race`, `gofmt -l` all clean). The user is
actively running `bin/awsops` against a real AWS account, working through
`TEST_PLAN_REAL_AWS.txt`.

- [ ] Continue working through `TEST_PLAN_REAL_AWS.txt` against real AWS,
      all four regions; mark items `[ok]` as confirmed (existing markers
      must be preserved, not overwritten)
- [ ] Phase 16 (PLAN.md): once the manual test plan is fully run, close
      out any gaps it surfaces
- [ ] Phase 17 (PLAN.md): documentation pass + retire `ec2_ami_manager.bash`/
      `ami_copy.bash`/`tests/*.bats` once the Go binary has reached parity
      and passed the manual test plan

## Discussed but not yet designed/implemented

- [ ] Pre-flight sanity check before `RunInstances`: cross-reference the
      picked AMI's `EnaSupport` (from `DescribeImages`) against the picked
      instance type's ENA requirement (`ec2:DescribeInstanceTypes`) to catch
      the `InvalidParameterCombination: Enhanced networking (ENA) is
      required...` class of error before submitting, not after. Surfaced by
      two real launch failures this session (AMI `ami-0da49db6a772dda02`
      isn't ENA-enabled, `t3.micro` requires it). Scope not yet decided --
      just this one failure class, or a short list of other common
      combinations.
- [ ] Retry-on-launch-failure: instead of bouncing back to the main menu on
      any `RunInstances` error, keep the already-collected params and let
      the operator re-enter just the field that's likely wrong (e.g.
      Instance type) instead of re-doing the whole launch flow. Granularity
      not yet decided (just Instance type, or any collected field).

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
