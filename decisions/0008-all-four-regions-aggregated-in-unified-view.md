---
id: "0008"
title: "All four regions aggregated in unified view"
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
uuid: "f0f1d8f3-2306-4450-9eff-150a40184f65"
origin_host: "MACMINI-RD.local"
---

**Context.** The script must operate across four AWS regions. We must decide whether to show each region separately, aggregate all regions, or let the user select a region per operation.

**Decision.** **Aggregate all four regions in a single unified view.**

**Rationale.**
- Provides a complete picture of infrastructure across all regions at a glance
- Matches the user's stated requirement to "list the EC2 instances from one of the four regions we use"
- Simplifies the user experience by eliminating region selection from the critical path
- Instance and AMI IDs are globally unique, so aggregation doesn't cause collisions

**Rejected alternatives.**
- *Single region (user picks first)* — requires extra step and doesn't show full infrastructure state
- *Region per operation* — inconsistent UX, requires region selection for every action

**Consequences.**
- All API calls must be made against each of the four regions
- Aggregation logic needed to combine and deduplicate results
- Display must include region column for all resources
- Performance: slightly slower due to 4x API calls, but acceptable for interactive use

---

