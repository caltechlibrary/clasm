---
id: "0057"
title: "Retire ec2_ami_manager.bash, ami_copy.bash, and the Bash test suite"
date: "2026-07-08"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "98fe8173-f8c0-4e45-b0b3-ba8400ddbd60"
origin_host: "MACMINI-RD.local"
---

**Context.** Phase 16's manual real-AWS verification (`TEST_PLAN_REAL_AWS.txt`)
is now fully signed off: all 112 checklist items confirmed against real AWS
across EC2/AMI instance and AMI lifecycle management, tag management,
cloud-init inspection, Backup Archive & Trim, and the `~/.awsops` config
file. Per this project's own retire-after-verify criterion (see
"Retarget implementation from Bash to Go" and the 2026-06-30 entries below),
`ec2_ami_manager.bash` and `ami_copy.bash` were kept, unchanged, purely as
the working reference for the Go rewrite's behavior until parity was
verified against real AWS. That condition is now met.

**Decision.** **Delete `ec2_ami_manager.bash`, `ami_copy.bash`,
`ami_copy_basic_steps.md`/`.html`, and the entire `tests/` directory
(`tests/*.bats`, `tests/lib/test_helper.bash`, `tests/README.md`) from the
repository.** `awsops` (Go) is now the sole implementation.

**Rationale.**
- Two parallel implementations of the same functionality is a maintenance
  cost with no offsetting benefit, once the newer one is verified equivalent
  or better -- the same reasoning as the 2026-06-30 retirement of
  `check_ami.bash`/`check_ec2_instances.bash`.
- `tests/lib/test_helper.bash` sources `ec2_ami_manager.bash` directly, and
  `tests/README.md` documents that BATS suite specifically -- neither has
  any purpose once the Bash script they test is gone, so the whole `tests/`
  directory retires together, not just the `.bats` files.
- Verified before deleting: the Makefile, `website.mak`, and Go source
  under `cmd/`/`internal/` have no functional dependency on these files
  (some Go source comments cite `ec2_ami_manager.bash` for historical
  lineage only, which don't affect `go build`/`go vet`/`go test`).

**Rejected alternatives.**
- *Keep the Bash files as an archived reference* — rejected; git history
  already preserves them in full (see the 2026-06-30 entries' precedent of
  actual deletion, not archival), and a live copy in the tree invites
  someone eventually running the stale, unmaintained version by mistake.

**Consequences.**
- `TODO.md`'s "Superseded (Bash version)" section and its remaining
  Bash-specific items are now historical only -- the files they describe no
  longer exist in the working tree.
- `DESIGN.md`'s top-of-file banner note about `ec2_ami_manager.bash`
  "remaining unchanged... until the Go version reaches parity" is updated
  to record this retirement.
- `README.md`, `INSTALL.md`, `user_manual.md`, `software_requirements.md`,
  and `codemeta.json` (and its generated `about.md`/`CITATION.cff`) were
  rewritten in the same pass to describe the Go `awsops` binary instead of
  the retired Bash scripts -- they had drifted since the 2026-07-01
  Bash-to-Go retarget and still described the original proof-of-concept.

---

