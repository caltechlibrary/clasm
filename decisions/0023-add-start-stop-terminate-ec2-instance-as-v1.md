---
id: "0023"
title: "Add Start/Stop/Terminate EC2 Instance as v1 primitives"
date: "2026-07-01"
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
uuid: "958fa72e-6f0e-4d86-aa83-bf2e5799c07b"
origin_host: "MACMINI-RD.local"
---

**Context.** Immediately after adding "Create Instance from Cloud-Init
YAML," the user pointed out more gaps: there's no way to stop a running
instance or to terminate/remove one, and then — while this very decision
was being written up — no way to start a stopped one back up either. The
v1 list only covered AMI lifecycle and instance *creation*, not the rest
of an instance's power-state lifecycle.

**Decision.** Add three more v1 primitives:
- **Start EC2 Instance** (Feature 9): pick a stopped instance, simple
  yes/no confirm (safe and reversible, the symmetric counterpart to Stop),
  `ec2:StartInstances`, poll until `running`, display connection info
  (public IP may have changed unless an Elastic IP is in use)
- **Stop EC2 Instance** (Feature 10): pick a running instance, simple
  yes/no confirm (stopping is reversible — data on EBS volumes persists,
  the instance can be started again), `ec2:StopInstances`, poll until
  `stopped` (bounded timeout)
- **Terminate EC2 Instance** (Feature 11): pick an instance, dry-run
  showing what would be destroyed — **including whether any attached EBS
  volume has `DeleteOnTermination=true`**, since that volume's data
  (including any not-yet-archived backups — see Backup Archive & Trim) is
  destroyed along with the instance — an `Environment=production` warning
  if tagged, type-to-confirm, then `ec2:TerminateInstances`. Same safety
  tier as Remove AMI (Feature 4)

**Rationale.**
- Stopping/starting and terminating are fundamentally different risk
  levels (reversible vs. permanent), so they get different confirmation
  tiers — matching this project's existing principle of scaling friction
  to actual risk rather than applying one blanket confirmation style
  everywhere
- Surfacing `DeleteOnTermination` in the dry-run closes a real gap this
  project already cares about: an instance can be terminated with its
  root volume set to delete-on-termination, destroying exactly the kind
  of not-yet-archived backup data Backup Archive & Trim exists to protect
- `ec2:TerminateInstances` was already a planned permission (for Show/
  Export Cloud-Init's AMI-path cleanup); only `ec2:StartInstances`/
  `ec2:StopInstances` are new

**Rejected alternatives.**
- *One combined "manage instance power state" primitive covering start/
  stop/terminate* — considered, but start/stop and terminate have
  different confirmation tiers and different risk profiles; combining
  them risks the lighter-weight start/stop confirmation habituating users
  to clicking through what should be a heavier gate for terminate

**Consequences.**
- `DESIGN.md` gets three new Features (9, 10, 11); Project/Environment
  Tagging moves from Feature 8 to Feature 12 (four new features inserted
  ahead of it: Feature 8 Create-from-Cloud-Init-YAML, Feature 9 Start,
  Feature 10 Stop, Feature 11 Terminate)
- `PLAN.md` gets three new Phases after the Create-from-Cloud-Init-YAML
  phase, before Main Menu and Integration; later phases renumbered
  accordingly
- `DESIGN.md`'s IAM permission list gains `ec2:StartInstances` and
  `ec2:StopInstances`

---

