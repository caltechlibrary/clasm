---
id: "0010"
title: "AMI-from-instance: fold ami_copy.bash capabilities into Phase 5"
date: "2026-06-30"
status: accepted
kind: decision
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "e6b27cf7-ea8c-4706-a48d-e3d4edb528e2"
origin_host: "MACMINI-RD.local"
---

**Context.** `ami_copy.bash` (and `ami_copy_basic_steps.md`) was merged in
from a separate repository (`git log`: "merged ami copy from ami_copy
repo") before this project's DESIGN.md/DECISIONS.md/PLAN.md existed. It was
never reconciled with the design: it duplicates the "Create AMI from EC2
Instance" feature (Phase 5 — see "Include both running and stopped
instances for AMI creation" above) but is single-region only, and it
contains capabilities Phase 5's `create_ami_from_instance_workflow` lacks:
volume-size-based time estimates, prior-snapshot detection, an SSM `fstrim`
pre-snapshot optimization step, unbounded elapsed-time polling during
creation, and Postgres/OpenSearch-specific crash-consistency guidance for
running instances (relevant because this team's primary AMI target is
Invenio RDM instances).

Separately, Phase 5's `post_ami_creation_actions()` times out after 600
seconds (10 minutes) of polling. Per `ami_copy_basic_steps.md`'s own timing
table, this is too short for real usage — even small volumes take 5–15
minutes, and an Invenio RDM instance is estimated at 20–60+ minutes.

**Decision.** **Port `ami_copy.bash`'s capabilities into Phase 5's
multi-region workflow (tracked as Phase 5b in PLAN.md), then retire
`ami_copy.bash`.** Keep the multi-region aggregation Phase 5 already has
rather than narrowing to `ami_copy.bash`'s single-region scope.

**Rationale.**
- Multi-region aggregation (an earlier decision, see "All four regions
  aggregated in unified view" above) is more valuable than what
  `ami_copy.bash` offers and shouldn't be lost
- The volume-size estimate, fstrim step, and unbounded polling are real
  operational value specific to this team's large, stateful instances —
  losing them by simply deleting `ami_copy.bash` would be a regression
- The 600-second timeout in Phase 5 is a correctness bug independent of the
  consolidation question and should be fixed regardless

**Rejected alternatives.**
- *Keep both scripts indefinitely* — two divergent implementations of the
  same operation with different region scope is confusing and doubles
  future maintenance
- *Retire ami_copy.bash immediately without porting* — would silently drop
  working functionality (fstrim optimization, realistic time estimates,
  Invenio-specific guidance) that was never documented elsewhere

**Consequences.**
- New PLAN.md Phase 5b enumerates the specific functions/behavior to port
- `ami_copy.bash` and `ami_copy_basic_steps.md` are retired only after
  Phase 5b is implemented and verified, not before
- DESIGN.md's Core Features and File Structure sections should be updated
  once Phase 5b lands

---

