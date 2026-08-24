---
id: "0001"
title: "Refresh data after each operation"
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
uuid: "e882adaf-3218-4b84-acd5-d0318bac05de"
origin_host: "MACMINI-RD.local"
---

**Context.** After performing operations (create instance, create AMI, remove AMI), the displayed resource lists become stale. We must decide when to refresh.

**Decision.** **Refresh all resource data after each operation that modifies state, and provide a manual refresh option.**

**Rationale.**
- Ensures user always sees current state
- Automated refresh after modifications provides immediate feedback
- Manual refresh option allows user to see latest state at any time
- Simple to implement (re-run the listing functions)

**Rejected alternatives.**
- *Refresh only on demand* — user might not realize data is stale
- *Periodic auto-refresh* — adds complexity, unnecessary API calls
- *No refresh* — poor UX, user sees outdated information

**Consequences.**
- Each operation will include the cost of 8 API calls (4 regions x 2 resource types)
- Need to consider performance for slow connections
- Can add progress indicators during refresh
