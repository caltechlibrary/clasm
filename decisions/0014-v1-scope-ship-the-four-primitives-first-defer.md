---
id: "0014"
title: "V1 scope: ship the four primitives first, defer composite workflows"
date: "2026-07-01"
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
uuid: "7443393c-2e3d-4342-b9ae-c2f5058c514c"
origin_host: "MACMINI-RD.local"
---

**Context.** The stated goal for this tool — speed up upgrading
deployments, create accurate test environments with confidence — is
naturally served by *composite* operations (e.g. "clone this instance for
testing, inheriting its network/instance-type config" or "upgrade with a
tracked rollback point"), not by the four raw primitives
(create-instance-from-AMI / create-AMI-from-instance / remove-AMI /
refresh) alone. Those primitives require the user to manually chain
operations and re-enter configuration (instance type, security groups,
subnet, IAM profile) from scratch each time, which is exactly the
slowness/accuracy-drift risk the stated goal wants to eliminate.

**Decision.** V1 of the Go tool ships a faithful port of the four existing
primitives (matching `ec2_ami_manager.bash`'s feature set), verified
against real AWS, before any composite workflow is added. "Clone instance
for testing" and "upgrade with a tracked rollback point" are recorded here
as intended fast-follow work, not dropped.

**Rationale.**
- Keeps the rewrite scoped and verifiable: Bash→Go parity is itself
  nontrivial (see "Retarget implementation from Bash to Go" below) and
  mixing in new composite behavior would make it harder to tell whether a
  bug is a porting regression or new-feature bug
- The primitives are the building blocks the composite workflows will call
  — building them first, correctly, is not wasted work
- Real-AWS verification (`TEST_PLAN_REAL_AWS.txt`) is easier to reason
  about against a known, already-specified feature set

**Rejected alternatives.**
- *Build composite workflows into v1 directly* — more directly serves the
  stated goal sooner, but risks conflating porting bugs with new-feature
  bugs during the highest-risk phase (initial Go implementation)

**Consequences.**
- `PLAN.md` gets a "Deferred / Future Work" section describing "Clone
  instance for testing" and "Upgrade with rollback point" so they aren't
  lost, to be scheduled once Phase 9 (real-AWS verification) passes
- The Project/Environment tagging convention (see companion decision
  above) is still built in v1, since the composite workflows will depend
  on it and it's needed for the listing/removal-friction behavior anyway

---

