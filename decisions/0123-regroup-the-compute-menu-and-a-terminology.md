---
id: "0123"
title: "Regroup the Compute menu, and a terminology cleanup across every domain menu"
date: "2026-07-24"
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
uuid: "78782959-5d4a-46b9-b3c9-b50e30e8935f"
origin_host: "MACMINI-RD.local"
---

**Context.** The user asked for a review of every menu's item ordering,
which felt ad hoc. See `MENU_REVIEW.md` for the full audit (all seven
top-level menus plus notable sub-menus, current order, and reasoning).
Root cause identified there: new menu entries have generally been
*appended* to the end of a list specifically to avoid shifting the
numeric index every existing accessible-mode pipe-input test depends on
-- a real engineering constraint, but one that optimizes for not
breaking tests rather than for where an item logically belongs. The two
newest Compute entries (Show instance/AMI detail, Phase 20.41) were the
clearest case: tacked onto the very end, 20 rows away from the "Show
instances"/"Show AMIs" entries they're conceptually paired with.

**Decision 1: regroup the Compute menu into View/Inspect -> Instance
lifecycle -> AMI lifecycle -> Launch Template lifecycle -> Maintenance**,
modeled directly on the IAM domain menu's own List -> Detail -> Create ->
CRUD shape (the one domain designed all at once, and the cleanest as a
result). Each list-view now sits directly next to its own detail view
(Show instances + Show instance detail, Show AMIs + Show AMI detail, Show
launch templates + Show launch template detail), matching IAM's
Show-Roles-then-Detail pairing. "Show/export cloud-init" moved into the
view/inspect group (it's a read operation, same as the detail views).
"Manage tags for an instance or AMI" moved to the end of the instance
group rather than sitting in the middle of instance-only actions, since
it's cross-cutting (instances and AMIs both). This is a regrouping, not a
full reshuffle -- every item's relative order within its own group is
unchanged. `menu_test.go`'s numeric-index literals were updated to match
(contained to that one file; every other workflow's own tests drive it
directly, not through the Compute menu's position).

**Decision 2: apply the same "view leads" convention to two menus that
had drifted from it.** Tag Management's two items were backwards ("Manage
tags" before "Show all tags") relative to every other domain -- swapped.
The Bucket Lifecycle action menu had "View rule details" last instead of
first (Manage Tags' own action menu, "Show tags, Add, Update, Remove",
already got this right by contrast) -- moved to the front.

**Decision 3: reorder IAM's last three items to Attach -> Detach ->
Delete** (was Delete -> Attach -> Detach). Attach/Detach are both
trivially reversible via their paired action; Delete Role is not, so the
one truly irreversible action now sits last.

**Decision 4 (considered, not applied): reorder Tag Management's "kind"
picker.** `MENU_REVIEW.md` flagged this as a possible cleanup (order
reflects historical addition sequence, not an obvious grouping). On
closer inspection while implementing the rest of this pass, the current
order (`Instance, AMI, Launch Template, Key Pair` -- all EC2-backed --
then `S3 Bucket`, then the three IAM kinds) already reads as a sensible
cluster grouping, coincidentally aligned with addition order. Left
unchanged rather than reordering for its own sake.

**Decision 5: terminology cleanup, scoped to "unify verbs," not a full
capitalization pass.** Three renames, all label-text only (no Go
identifier renamed):
- S3's `"List S3 Buckets"` -> `"Show Buckets"` -- the one menu using
  "List" where every other domain says "Show."
- Key Management's `"Show resource lists"` -> `"Show Key Pairs"` -- a
  vague, generic name left over from before Compute's own "Show resource
  lists" was split into specific per-resource entries (2026-07-20); Key
  Management only ever manages one resource type, so the specific name
  is both clearer and consistent.
- IAM's `"View Role Detail"`/`"View Instance Profile Detail"` ->
  `"Show Role Detail"`/`"Show Instance Profile Detail"`, and Compute's
  `"Show a launch template"` -> `"Show launch template detail"` --
  unifying three different phrasings of the same "show one resource's
  detail" concept onto one verb and shape. `IAMActions`'
  `ViewRoleDetail`/`ViewInstanceProfileDetail` Go field names are
  unchanged -- only the user-facing label changed.

**Rejected: normalizing capitalization across all menus to sentence
case.** Key Management/S3/IAM use Title Case throughout; Compute/Tag
Management/Configuration use sentence case -- a real, visible split
across domains. Flagged in `MENU_REVIEW.md` as the largest, purely
cosmetic option (~18 labels) and explicitly declined by the user for
this pass in favor of the narrower verb-unification scope above. Left as
a known, still-open inconsistency if picked up later.

**Known, pre-existing gaps not addressed by this pass:** `user_manual.md`
and `TUI_REFERENCE.md`'s screen-map section are both already
substantially stale independent of this change (missing the Tag
Management/IAM/Configuration domains entirely, wrong item counts,
`user_manual.md` still describing a removed "Back to domain picker" menu
item) -- bringing either fully current is a separate, larger task, not
attempted here to keep this pass scoped to the actual menu-ordering/
terminology request.

---

