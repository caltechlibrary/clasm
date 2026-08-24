---
id: "0024"
title: "Add Create EC2 Instance from Cloud-Init YAML as a v1 primitive"
date: "2026-07-01"
status: accepted
kind: decision
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "cb67c67b-4000-4596-addf-565ca7308e18"
origin_host: "MACMINI-RD.local"
---

**Context.** Feature 2 (Create Instance from AMI) already got a cloud-init
file-input and completion-check enhancement (see "Enhance Create Instance
from AMI" below), but the user pointed out that burying it as the 6th of
7 parameters inside an AMI-first workflow doesn't serve the actual use
case: someone who starts from "I have a cloud-init recipe, give me a
machine" has a different mental model than someone who starts from "give
me another copy of this AMI." That deserves to be its own visible
primitive, not an option nested inside Feature 2's parameter list.

**Decision.** Add "Create EC2 Instance from Cloud-Init YAML" as its own
v1 primitive (Feature 8): the cloud-init file is the *first* thing
collected, then a base AMI is picked, then the same remaining launch
parameters Feature 2 already collects. It shares Feature 2's underlying
execution path entirely (the same `LaunchInstanceParams` struct, the same
launch/poll/cloud-init-completion-check logic) — the only difference is
the order and framing of the interactive prompts. This is distinct from
the deferred "Bake AMI from cloud-init" idea: that one snapshots the
result into a new AMI and terminates the instance; this one leaves a real,
running, usable instance.

**Rationale.**
- Matches the user's explicit ask: this needed to be visible in the
  primitive list, not folded into another feature's parameter collection
- Sharing execution logic with Feature 2 avoids duplicating the
  launch/poll/cloud-init-check code — only the front-end prompt sequence
  differs, consistent with the params-struct/confirm-gate seam already
  required of every workflow (see "Structure workflows for future
  record/replay")
- Placed as Feature 8 (after Backup Archive & Trim, before Stop/Terminate
  Instance and the cross-cutting Project/Environment Tagging convention —
  see the companion decision below) rather than immediately after
  Feature 2, to avoid a costly renumbering cascade through Features 3-7
  and their many existing cross-references, for a placement decision that
  doesn't carry strong semantic weight either way

**Rejected alternatives.**
- *A sub-mode within Feature 2 ("how do you want to start: AMI or
  cloud-init?")* — was the initial implementation; rejected because the
  user wants it directly visible as its own primitive, not a branch
  hidden inside another feature's flow
- *Insert immediately after Feature 2, renumbering Features 3-7* —
  arguably better thematic grouping, but not worth the renumbering risk
  across this document's many existing Feature N cross-references for a
  placement question without a strong correctness argument either way

**Consequences.**
- `DESIGN.md` gets a new Feature 8
- `PLAN.md` gets a new Phase 10 ("Create Instance from Cloud-Init YAML"),
  inserted before Main Menu and Integration
- No new IAM permissions — reuses exactly what Feature 2/Phase 4 already
  needs (`ec2:RunInstances`, SSM for the completion check)

---

