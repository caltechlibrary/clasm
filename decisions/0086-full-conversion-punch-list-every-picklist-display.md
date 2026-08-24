---
id: "0086"
title: "Full conversion punch list: every PickList/Display* call site classified by target tier"
date: "2026-07-10"
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
uuid: "93b837b0-eb72-444d-8366-96ae113d9aa8"
origin_host: "MACMINI-RD.local"
---

**Context.** After Phase 20.9 (lifecycle action menu → `huh.Select`),
the user asked to "review the source code and identify all the places
where we want to upgrade to the huh.Select, our Picker and View lists"
— a comprehensive punch list to work through quickly, extending the
Picker-only map from the previous decision entry to also cover Menu
(`huh.Select`) and List (`tui.ListView`) targets.

**Decision.** Surveyed every `ui.PickList` call site (33 total) and
every `ui.Display*` function (4 total) directly from source, classified
each into Menu / Picker / List, and recorded the full result in
DESIGN.md's "Picker tier" section as three tables (one per target tier)
with file:line references and a done/not-started status column.
Classification rule applied consistently: fixed, small, compile-time-
known option sets (domain/action menus, curated instance-type lists,
storage-class enums, kind pickers) → Menu; fetched, potentially long,
variable-length AWS resource collections → Picker; read-only resource
displays → List.

**Rejected alternatives.**
- *Classify storage-class selection (`bucket_lifecycle.go:296,399`) as
  Picker, since it's technically a list of AWS-defined values* —
  rejected: both lists are fixed and known at compile time (one
  curated to 4, one the full but still static `TransitionStorageClass`
  enum), not fetched from AWS at runtime, and short enough that
  scrolling/filtering wouldn't help — matching the Menu tier's
  definition, not the Picker tier's.
- *Classify region selection (`bucket_create.go:26`,
  `keymgmt_common.go:25`) as Picker* — rejected for the same reason:
  these are this team's own configured region list (typically 2
  entries), not a fetched AWS resource collection.

**Consequences.** No code changed by this decision — it's a planning
artifact. Nothing beyond what's already marked "done" in the three
tables is scheduled; each conversion still gets picked up and scoped
individually, per this project's established incremental discipline
(TODO.md, "Termlib Removal (before 0.0.2)").

---

