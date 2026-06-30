
# Action items

## Next

- [x] Phase 5b (PLAN.md): port ami_copy.bash's volume-size estimate, SSM
      fstrim step, unbounded creation polling, and Invenio-RDM running-
      instance guidance into ec2_ami_manager.bash's Phase 5 workflow.
      Implemented and unit-tested (tests/test_create_ami.bats, 27 tests).
- [ ] Manually verify ec2_ami_manager.bash's create-AMI workflow against a
      real EC2 instance (all four regions), then retire ami_copy.bash and
      ami_copy_basic_steps.md
- [ ] Write BATS tests for Phase 6 (AMI removal) and Phase 7 (main menu) —
      see tests/README.md coverage table
