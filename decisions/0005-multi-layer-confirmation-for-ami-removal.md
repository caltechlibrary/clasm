---
id: "0005"
title: "Multi-layer confirmation for AMI removal"
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
uuid: "90e6284f-51a0-4c4a-8f74-9df00486913d"
origin_host: "MACMINI-RD.local"
---

**Context.** Removing an AMI is destructive and irreversible. We must implement safety mechanisms to prevent accidental deletion.

**Decision.** **Implement three-layer confirmation:**
1. Dry-run first: Show exactly what would be deleted
2. Show dependencies: List any instances currently using this AMI
3. Type to confirm: User must type the AMI ID or name exactly

**Rationale.**
- AMI deletion cannot be undone
- Instances using a deleted AMI cannot be launched or rebooted (if instance-store backed)
- Typo in selection could cause significant data loss
- Multiple confirmation layers provide defense in depth

**Rejected alternatives.**
- *Simple yes/no prompt* — too easy to accidentally confirm
- *Dry-run only* — doesn't prevent accidental confirmation
- *Show dependencies only* — doesn't verify user intentionality

**Consequences.**
- More steps required to delete an AMI (acceptable for safety-critical operation)
- Need to implement dependency checking (query instances by ImageId)
- Must handle case where AMI has no instances using it
- Error handling for typed confirmation mismatch

---

