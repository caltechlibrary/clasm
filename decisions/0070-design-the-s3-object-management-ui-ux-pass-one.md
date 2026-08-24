---
id: "0070"
title: "Design the S3 object management UI/UX pass: one interactive file manager, not three separate wizards"
date: "2026-07-09"
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
uuid: "0f76531a-a6a4-4670-a658-f67c71907897"
origin_host: "MACMINI-RD.local"
---

**Context.** The "0.0.1 scope" decision below deferred the UI/UX pass
entirely, flagging `huh` as the leading candidate but starting no work.
This entry begins that work: the S3 domain had grown three independent,
object-touching workflows shipped in Phase 20 -- Sync Local Directory to
Bucket (Feature 20), Browse/Manage Objects (Feature 21, single-object
only), and an ad-hoc bulk delete-by-prefix case -- each with its own
selection model (auto-diff, single-pick, whole-prefix-only) and none
supporting multi-select. Read (download) was never implemented at all
(Phase 20: "object content is never downloaded, only `HeadObject`
metadata").

**Decision 1.** Replace all three with one interactive file manager
screen (DESIGN.md Features 21.2-21.8), single-pane (bucket only) or
double-pane (bucket + linked local directory) depending on whether a
local directory is linked. Tagging one item and acting on it covers
Feature 21's old single-object case; tagging many covers the old
bulk-delete-by-prefix case; both live in the same screen instead of
three parallel implementations of "filter, pick, act."

**Decision 2.** Sync's directory-mirroring workflow is kept as a
first-class, directly reachable capability -- the double-pane/linked
mode -- rather than dissolved into a generic action-first "pick an
action, then pick candidates" flow. Upload candidates only make sense
as a diff against a local directory (there's no way to compute "what
should I upload" from the bucket side alone), so Sync's shape is
inherently different from Download/Delete's "browse and pick" shape,
and it's common enough usage that it deserves a direct path, not a
detour through an action menu.

**Decision 3.** Add Read (Download) to the CRUD scope now:
`s3:GetObject` is added to `S3API`, completing Create/Update (Upload) /
Read (Download) / Delete parity. Previously deferred in Phase 20
("`GetObject` isn't needed since object content is never downloaded").

**Rejected alternatives.**
- *Keep the three existing wizards, add multi-select to each
  independently* -- rejected; perpetuates three separate
  selection/filter/confirm implementations that would need to be kept
  in sync by hand as the UI evolves further.
- *Fold Sync into the generic batch flow as an "Upload" action
  alongside Download/Delete* -- rejected (see Decision 2); there's no
  bucket-side way to build an upload candidate set, and burying a
  common, direct workflow behind an action-first menu makes it harder
  to reach, not easier.
- *Defer Download again, scope this pass to Upload/Delete UX only* --
  rejected; CRUD parity was worth completing given the interactive
  screen already has to support tagging and acting on bucket objects
  generically, and the added cost (one interface method, one action
  handler) is small next to the rest of this phase.

**Consequences.** DESIGN.md Features 20 and 21 are marked superseded
(design-only, not yet implemented) by new Features 21.2-21.8; their
existing text is otherwise untouched and still describes what 0.0.1
actually ships. See `PLAN.md` Phase 20.1.

---

