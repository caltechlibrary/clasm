# clasm Menu Review — 2026-07-24

**Status: implemented 2026-07-24.** All 5 reordering suggestions below
were applied except the Tag Management kind-picker one, which turned out
on closer inspection not to need a change (see DECISIONS.md, "Regroup the
Compute menu, and a terminology cleanup across every domain menu," and
PLAN.md Phase 20.43, for what actually happened). The terminology
question below was answered with the "S3 rename + unify verbs" scope, not
the broader capitalization pass. `user_manual.md` and `TUI_REFERENCE.md`
(flagged below as already stale, independent of this review) were also
brought current as part of the same-day v0.0.5 release review, alongside
this file's own status note. This document is left as-is below as the
original review; treat the DECISIONS.md/PLAN.md entries as authoritative
for final state.

Purpose: lay out every interactive menu in clasm as it exists today, in its
current item order, then flag places where that order feels ad hoc rather
than deliberate, and propose a reordering for review.

## Why the ordering feels ad hoc

Two real conventions already exist and are documented in DECISIONS.md:

1. **"Show/List" leads a domain menu** — established 2026-07-02, "Move
   'Show resource lists' to the top of the Compute menu; rename from
   'Refresh'": orient before acting is the natural first move on entering
   a domain.
2. **No "Back"/"Exit" entry anywhere** — 'q' is the universal back/quit
   key (DECISIONS.md, "TUI keybinding conventions").

But there's a third, *unwritten* pattern that's been applied inconsistently:
**new menu entries have almost always been appended to the end of the
list**, specifically so they don't shift the numeric index every existing
accessible-mode pipe-input test depends on (`"7\n"` for "Start EC2
instance", etc.) — a real, understandable engineering reason, but one that
optimizes for *not breaking tests* rather than for *where the item
logically belongs*. That's very likely the specific feeling you're
picking up on: several recently-added items (the two new Instance/AMI
detail views, in particular) sit at the very bottom of the Compute menu
for purely mechanical reasons, disconnected from the related items they
belong next to.

The **IAM menu** is the one domain that was designed all at once, and it
shows: List views, then single-resource Detail views, then Create, then
the CRUD-completion actions — a clean, deliberate shape. It's the best
model for what the others (especially Compute) could look like.

---

## 1. Domain Picker — 6 items

```
1. Compute (EC2 & AMI)
2. Key Management
3. S3 (Buckets & Static Websites)
4. Tag Management
5. IAM
6. Configuration
```

**Assessment:** reasonable as-is. Roughly usage-frequency-first (Compute,
the original single domain, still leads), then the two cross-cutting
domains (Tag Management, IAM), then Configuration last — settings-last is
a common, expected convention. No change suggested.

---

## 2. Compute Menu — 24 items (the biggest offender)

### Current order

```
 1. Show instances
 2. Show AMIs
 3. Show launch templates
 4. Create EC2 instance from AMI
 5. Create EC2 instance from cloud-init YAML
 6. Create EC2 instance from launch template
 7. Start EC2 instance
 8. Stop EC2 instance
 9. Terminate EC2 instance
10. Resize instance's root volume
11. Associate/replace IAM instance profile
12. Manage tags for an instance or AMI
13. Create AMI from EC2 instance (running or stopped)
14. Remove AMI
15. Show/export cloud-init for an instance or AMI
16. Show a launch template
17. Create launch template from cloud-init YAML
18. Sync cloud-init YAML to a launch template
19. Promote a launch template version to default
20. Delete launch template version(s)
21. Delete a launch template
22. Archive stale backups to S3 and trim disk space
23. Show instance detail          <- appended 2026-07-24, purely to avoid reindexing
24. Show AMI detail               <- appended 2026-07-24, purely to avoid reindexing
```

### What's actually going on

- Items 1–3 (the three list views) correctly lead, per the established
  convention.
- Items 23–24 (the new detail views) are the clearest case of "tacked on
  the end" — they're conceptually a pair with items 1–2 and with item 16
  ("Show a launch template"), but sit 20 rows away from all of them.
- Instance actions (4–12), AMI actions (13–14), cloud-init export (15),
  launch-template actions (16–21), and backup maintenance (22) are
  *mostly* grouped already, just interleaved a bit (cloud-init export sits
  between AMI actions and launch-template actions even though it applies
  to instances and AMIs, not templates).
- "Manage tags for an instance or AMI" (12) sits in the middle of
  instance-only actions even though it's cross-cutting (works on
  instances and AMIs both) — same category problem as cloud-init export.

### Suggested reordering

Group by: **View/Inspect → Instance lifecycle → AMI lifecycle → Launch
Template lifecycle → Maintenance** — mirroring the List → Detail → Create
→ Mutate shape the IAM menu already uses.

```
 1. Show instances
 2. Show instance detail
 3. Show AMIs
 4. Show AMI detail
 5. Show launch templates
 6. Show a launch template
 7. Show/export cloud-init for an instance or AMI
 --- (view/inspect group ends)
 8. Create EC2 instance from AMI
 9. Create EC2 instance from cloud-init YAML
10. Create EC2 instance from launch template
11. Start EC2 instance
12. Stop EC2 instance
13. Terminate EC2 instance
14. Resize instance's root volume
15. Associate/replace IAM instance profile
16. Manage tags for an instance or AMI
 --- (instance lifecycle group ends)
17. Create AMI from EC2 instance (running or stopped)
18. Remove AMI
 --- (AMI lifecycle group ends)
19. Create launch template from cloud-init YAML
20. Sync cloud-init YAML to a launch template
21. Promote a launch template version to default
22. Delete launch template version(s)
23. Delete a launch template
 --- (launch template lifecycle group ends)
24. Archive stale backups to S3 and trim disk space
```

Notes on specific placements:
- "Show instance detail"/"Show AMI detail" paired immediately after their
  respective list ("Show instances"/"Show AMIs"), matching IAM's
  List-then-Detail pairing.
- "Show/export cloud-init" moved into the view/inspect group (it's a read
  operation, same as the detail views) rather than sitting between AMI
  actions and launch-template actions.
- "Manage tags for an instance or AMI" moved to the end of the instance
  group rather than the middle — it's the last, most general-purpose
  instance/AMI action before moving on to AMI-specific and
  template-specific actions.
- Everything else keeps its existing relative order within its group —
  this is a regrouping, not a full reshuffle.

**Cost of applying this:** `mainMenuItems`' order is asserted by index
throughout `internal/workflow/menu_test.go` (roughly a dozen call sites
like `newHuhAccessibleInput("7\n") // Start EC2 instance`). Reordering
would mean updating each of those literal indices to match — contained to
that one file, not spread across the workflow test suite, since every
other workflow's own tests drive it directly rather than through the
Compute menu's position.

---

## 3. Key Management Menu — 4 items

```
1. Show resource lists
2. Create Key Pair
3. Import Key Pair
4. Delete Key Pair
```

**Assessment:** already well-ordered — view, then two ways to create,
then delete. No change suggested.

---

## 4. S3 Menu — 6 items

```
1. List S3 Buckets
2. Create Bucket
3. Configure Static Website Hosting
4. Browse & Manage Objects
5. Manage Bucket Lifecycle Policies
6. Delete Bucket
```

**Assessment:** already well-ordered — view, create, configure, use,
maintain, delete. No change suggested.

---

## 5. Tag Management Menu — 2 items

```
1. Manage tags
2. Show all tags
```

**Assessment:** this is backwards relative to every other domain's own
convention — "Show" should lead. This 2-item menu predates the "Show
leads" convention being applied consistently elsewhere and was never
revisited.

### Suggested reordering

```
1. Show all tags
2. Manage tags
```

**Cost of applying this:** small — only `tagmgmt_menu_test.go`'s index
literals (2 items, so trivial either way).

---

## 6. IAM Menu — 9 items (the model to follow)

```
1. Show Roles
2. Show Instance Profiles
3. Show Policies
4. View Role Detail
5. View Instance Profile Detail
6. Create Role from Template
7. Delete Role
8. Attach Policy to Role
9. Detach Policy from Role
```

**Assessment:** the cleanest menu in the app — List → Detail → Create →
CRUD-completion. This is the shape Compute's reordering above is modeled
on.

**Optional minor tweak:** items 7–9 go Delete → Attach → Detach. Since
Attach/Detach are both reversible and Delete is not, ordering them
Attach → Detach → Delete (least-destructive-first) would put the one
truly irreversible action last. Small enough that it's worth a mention,
not a strong recommendation.

---

## 7. Configuration Menu — 5 items

```
1. Show current config
2. Edit regions
3. Edit backup directory rules
4. Edit Origin tag config
5. Save
```

**Assessment:** already well-ordered — view, three edit actions, save
last as the natural "commit" step. No change suggested.

---

## Notable sub-menus (secondary, for completeness)

These aren't top-level domain menus, but they're still menus an operator
navigates, so they're worth a quick pass too.

**Tag Management's "kind" picker** (which resource type to tag) —
`Instance, AMI, Launch Template, Key Pair, S3 Bucket, IAM Role, IAM
Instance Profile, IAM Policy`. Order reflects the sequence each kind was
added to the domain (EC2-backed types first, S3 Bucket next, IAM last),
not usage frequency or any grouping logic. Low priority, but flagging it
since it's the same "grew by appending" pattern as the Compute menu.

**Bucket Lifecycle action menu** — `Add rule, Edit rule, Remove rule, View
rule details`. This one **is** inconsistent with the "view leads"
convention: "View rule details" is last instead of first. Manage Tags'
own action menu (`Show tags, Add, Update, Remove`) gets this right by
contrast. Suggested fix: move "View rule details" to the front.

**Manage Tags action menu** — `Show tags, Add, Update, Remove`. Already
correct (view leads). No change.

**Show Launch Template's action choice** — `Show version detail, List all
versions, Diff two versions`. Already view-oriented, no change.

**Edit regions / Edit backup directory rules sub-menus** — `Add, Remove,
Done`. No separate "Show" entry needed here — the current list is shown
via the Select's own Description text (the 2026-07-24 silent-scroll
fix), not as a separate menu choice.

---

## Summary of concrete suggestions, ranked by impact

1. **Reorder the Compute menu** into View/Inspect → Instance → AMI →
   Launch Template → Maintenance groups (see above). Biggest win, most
   directly addresses the "ad hoc" feeling. Touches `menu.go` +
   `menu_test.go`.
2. **Swap Tag Management's two items** so "Show all tags" leads. Small,
   brings it in line with every other domain.
3. **Move "View rule details" to the front** of the Bucket Lifecycle
   action menu, matching the view-leads convention.
4. *(Optional, low priority)* Reorder IAM's last three items to
   Attach → Detach → Delete.
5. *(Optional, low priority)* Reorder Tag Management's "kind" picker by
   some more meaningful grouping (e.g. EC2-backed / S3 / IAM clusters)
   rather than historical addition order.

Let me know which of these you'd like applied — happy to do them
one at a time or all together, with the corresponding DECISIONS.md entry
and test updates.
