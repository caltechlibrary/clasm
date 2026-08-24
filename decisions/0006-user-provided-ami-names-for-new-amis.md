---
id: "0006"
title: "User-provided AMI names for new AMIs"
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
uuid: "b6dab521-229c-42b5-80ee-6620a20420bb"
origin_host: "MACMINI-RD.local"
---

**Context.** When creating an AMI from an EC2 instance, we need a name for the new AMI. We must decide how this name is generated.

**Decision.** **Require user to provide the AMI name interactively.**

**Rationale.**
- AMI names are user-facing and should be meaningful
- Users have different naming conventions based on their organization
- Auto-generated names might not match organizational standards
- Simple and predictable for users

**Rejected alternatives.**
- *Auto-generate from instance name* — instance may not have a Name tag; generated names might not be descriptive enough
- *Auto-generate with prefix* — requires configuration of prefix; less flexible
- *Name + tags* — adds complexity for initial implementation

**Consequences.**
- User must think of a name each time (minor friction)
- Can add auto-suggestion in future based on instance metadata
- Name validation needed (AMI name constraints: 3-128 characters, specific allowed characters)

---

