---
id: "0011"
title: "Retire check_ami.bash and check_ec2_instances.bash"
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
uuid: "5aa8e7ce-8ec6-4028-9291-d4abdded76eb"
origin_host: "MACMINI-RD.local"
---

**Context.** `check_ami.bash` and `check_ec2_instances.bash` predate
`ec2_ami_manager.bash`. Both are non-interactive, read-only listing scripts
across the same four regions; their functionality is fully covered by
`list_ec2_instances()`/`list_amis()` and `display_instances()`/`display_amis()`
in `ec2_ami_manager.bash`, which additionally aggregate and sort consistently.

**Decision.** **Retire both scripts; the unified manager is the single
listing entry point.**

**Rationale.**
- No functionality in either script is missing from the manager
- Two parallel implementations of the same AWS queries is a maintenance cost
  with no offsetting benefit
- DESIGN.md's file-structure section listed them as "Existing" scripts to
  keep alongside the new manager without deciding their long-term role —
  this resolves that gap

**Rejected alternatives.**
- *Keep as quick non-interactive utilities* — considered for cron/scripting
  use cases, but nothing in this project currently invokes them
  non-interactively, and `ec2_ami_manager.bash` could add a non-interactive
  `--list` flag later if that need arises

**Consequences.**
- DESIGN.md's file-structure section should drop these two scripts
- Deletion is a separate, explicit step — not yet performed as of this entry

---

