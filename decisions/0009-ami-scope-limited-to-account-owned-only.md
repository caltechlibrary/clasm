---
id: "0009"
title: "AMI scope limited to account-owned only"
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
uuid: "8610734a-0e18-49a2-9982-a6fe84860d03"
origin_host: "MACMINI-RD.local"
---

**Context.** When listing available AMIs for instance creation, we must choose between showing all AMIs (public + private), only AWS marketplace AMIs, only account-owned AMIs, or a filtered subset.

**Decision.** **Show only AMIs owned by the current AWS account.**

**Rationale.**
- Reduces clutter and confusion for users managing their own infrastructure
- Public AMIs are typically accessed through other workflows (Launch Templates, console)
- Owned AMIs are the primary concern for lifecycle management (creation/removal)
- Simplifies the pick list to relevant, actionable items
- Aligns with the AMI removal feature which only makes sense for owned AMIs

**Rejected alternatives.**
- *All AMIs (public + private)* — would overwhelm users with thousands of irrelevant AMIs
- *Owned + AWS marketplace* — AWS marketplace AMIs cannot be deleted, creating inconsistency in the removal feature
- *Custom filter by tags* — adds complexity for initial implementation; can be added later as a filter option

**Consequences.**
- Users cannot launch instances from public AMIs through this script
- If needed, a future enhancement could add a "Show public AMIs" toggle
- The script focuses on managing custom/private infrastructure

---

