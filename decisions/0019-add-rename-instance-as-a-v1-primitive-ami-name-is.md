---
id: "0019"
title: "Add Rename Instance as a v1 primitive; AMI Name is immutable"
date: "2026-07-01"
status: accepted
kind: refinement
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
uuid: "906e96bc-dbcd-497d-b1ef-3789e71ce6b2"
origin_host: "MACMINI-RD.local"
---

**Context.** The user noticed renaming was missing from the v1 primitive
list and asked whether the AWS SDK supports it. The two resources behave
completely differently:
- An EC2 instance's "Name" is not a real API attribute at all — it's just
  the `Name` tag by convention (the same tag this project's Project/
  Environment tagging convention already reads/writes). Changing it is a
  plain `ec2:CreateTags` call, already a planned permission
- An AMI's `Name` is a real attribute, but it is set once at `CreateImage`
  time and **cannot be changed afterward via the AWS API** — this is an
  AWS EC2 limitation, not a gap in the SDK or this tool. `ModifyImageAttribute`
  allows changing `Description` and launch permissions, but not `Name`.
  The only way to get an AMI with a different name is `CopyImage`
  (produces a brand-new AMI with a new ID and duplicated snapshots) plus
  deregistering the original — a materially heavier operation than a
  rename, closer in cost/risk to Feature 3 (create-AMI) than a quick edit

**Decision.** Add "Rename Instance" as a v1 primitive (pick an instance,
prompt for a new `Name` tag value, confirm, `ec2.CreateTags`). Do not add
an "AMI rename" primitive of any kind. Feature 3 (Create AMI from EC2
Instance) keeps its existing default-name-suggestion behavior unchanged,
but gains an explicit note that the name is permanent once created, so
the user isn't surprised later. "Edit AMI Description" (the one AMI
attribute that *is* mutable) is recorded as a deferred, not-yet-requested
idea rather than built now.

**Rationale.**
- Renaming an instance is cheap, reversible, and was a real gap — no
  reason to defer it
- Silently supporting "rename" for AMIs by only updating a tag while the
  real `Name` attribute stays stale would be actively misleading, since
  AWS's own console/CLI would keep showing the original name
- Building a "rename via copy + deregister" primitive for AMIs was
  considered and rejected: it's a different operation with different
  risk (storage duplication, a new AMI ID, anything referencing the old
  ID breaks) than what a user asking to "rename" would expect

**Rejected alternatives.**
- *AMI rename via CopyImage + DeregisterImage* — technically the only way
  to get a differently-named AMI, but it's a heavyweight operation
  disguised as a rename; not built for v1
- *Only update the AMI's tags, leave Name attribute alone* — would create
  a confusing mismatch between this tool's tag-based "name" and the AMI's
  actual `Name` shown everywhere else in AWS

**Consequences.**
- `DESIGN.md` gets a new Feature 5 ("Rename Instance"), renumbering
  Show/Export Cloud-Init, Backup Archive & Trim, and Project/Environment
  Tagging by one
- `PLAN.md` gets a new Phase 9 ("Rename Instance"); Phases 9-12 in the
  prior draft are renumbered to 10-13 accordingly
- No new IAM permission needed — `ec2:CreateTags` was already planned for
  the tagging convention

---

