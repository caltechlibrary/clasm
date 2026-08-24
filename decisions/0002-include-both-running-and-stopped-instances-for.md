---
id: "0002"
title: "Include both running and stopped instances for AMI creation"
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
uuid: "7118f07d-526d-430c-962c-47fb104c9acc"
origin_host: "MACMINI-RD.local"
---

**Context.** When creating an AMI from an instance, the instance can be in various states. We must decide which states are allowed.

**Decision.** **Allow both running and stopped instances to be selected for AMI creation.**

**Rationale.**
- AWS supports creating AMIs from both running and stopped instances
- Stopped instances are common targets for AMI creation (clean state)
- Running instances might need AMIs created for backup purposes
- Matches the user's stated requirement

**Rejected alternatives.**
- *Running only* — excludes common use case of creating AMI from stopped instance
- *Stopped only* — excludes emergency backup scenarios
- *All states including terminated* — terminated instances cannot have AMIs created

**Consequences.**
- Need to filter out terminated instances from the pick list
- May want to warn user about creating AMI from running instance (potential inconsistency)
- For running instances, can offer no-reboot option

---

