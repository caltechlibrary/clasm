---
id: "0020"
title: "Broaden Rename Instance into a general Manage Tags primitive"
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
uuid: "aa6d4c19-9235-476b-afc0-6bc587c7c15b"
origin_host: "MACMINI-RD.local"
---

**Context.** Immediately after adding "Rename Instance" (below), the user
pointed out the obvious generalization: renaming is just "update the
`Name` tag," and if the tool can create tags, it should be able to
manage tags generally — add, update, and remove, on any resource, not
just set `Name` on an instance.

**Decision.** Replace Feature 5 ("Rename Instance") with a general
"Manage Tags" primitive: pick a resource (instance or AMI), see its
current tags, then add a new tag, update an existing one, or remove one.
Renaming is simply the common case of updating `Name` through this same
flow — no separate operation. Confirmation stays at the same lightweight
tier Rename Instance had (simple yes/no — tag edits are cheap and
reversible, not the dry-run/type-to-confirm tier reserved for actually
destructive operations), routed through the same reusable confirmation
gate as every other workflow.

**Rationale.**
- Avoids two overlapping menu items backed by the same underlying API
  (`ec2:CreateTags`) doing the same thing at different scopes
- AMIs get tags too (Project/Environment are set at creation per Feature
  3) but had no way to edit them after the fact — this closes that gap
  symmetrically for both resource types
- One general primitive is simpler to reason about, test, and eventually
  expose to Recorded Scripts than two narrow ones

**Rejected alternatives.**
- *Keep both Rename Instance and a separate Manage Tags primitive* — lets
  renaming stay a one-step action, but two menu items for the same
  underlying operation is redundant and was explicitly rejected in favor
  of consolidation
- *Tag management as an instance-only feature* — considered, but AMIs
  need the same capability (they carry Project/Environment tags too), so
  scoping it to instances only would just recreate the same gap for AMIs

**Consequences.**
- `DESIGN.md` Feature 5 is retitled "Manage Tags" and rewritten; no
  renumbering needed elsewhere since it's a same-slot replacement
- `DESIGN.md`'s IAM permission list gains `ec2:DeleteTags` (removal) —
  `CreateTags`/`DescribeTags` were already planned
- `PLAN.md` Phase 9 is retitled "Manage Tags" and its work items rewritten
  to cover add/update/remove on both instances and AMIs; still Phase 9,
  no renumbering elsewhere

---

