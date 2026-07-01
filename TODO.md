
# Action items

## Next (Go rewrite — see PLAN.md, DECISIONS.md 2026-07-01)

- [ ] Review the retargeted DESIGN.md/DECISIONS.md/PLAN.md and agree on the
      Go module path and package layout before starting Phase 0
- [ ] Phase 0 (PLAN.md): `go mod init`, add aws-sdk-go-v2 dependencies,
      package skeletons
- [ ] Work through PLAN.md Phases 1-7 in order (each depends on the last)
- [ ] Phase 9: run TEST_PLAN_REAL_AWS.txt against the Go binary, all four
      regions, before retiring the Bash tool

## Superseded (Bash version — kept for reference, see DECISIONS.md)

Real-world use surfaced three bugs (`eval` quoting crash in the interactive
picker, a locale-dependent `grep` failure, and a malformed AWS CLI tag
string that silently prevented AMI creation) that led to the decision to
retarget to Go rather than keep patching. `ec2_ami_manager.bash` and its
BATS suite remain untouched as the working spec/reference; the items below
are not being pursued further in Bash.

- [x] Phase 5b (PLAN.md, Bash version): port ami_copy.bash's volume-size
      estimate, SSM fstrim step, unbounded creation polling, and
      Invenio-RDM running-instance guidance into ec2_ami_manager.bash's
      Phase 5 workflow. Implemented and unit-tested
      (tests/test_create_ami.bats, 27 tests).
- [ ] ~~Manually verify ec2_ami_manager.bash's create-AMI workflow against a
      real EC2 instance~~ — superseded; verification now targets the Go
      binary (see PLAN.md Phase 9)
- [ ] ~~Write BATS tests for Phase 6 (AMI removal) and Phase 7 (main
      menu)~~ — superseded; those workflows get Go tests instead
