---
id: "0119"
title: "IAM Profile & Role Management: support CRUD for DLD-owned roles"
date: "2026-07-23"
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
uuid: "0b8a24d5-ed7a-4687-a6fd-f2c74587679f"
origin_host: "MACMINI-RD.local"
---

**Context.** Live usage surfaced the gap directly: "I think that we will
need to support CRUD operations for roles that are owned by our group
(example I now have two test roles cluttering things up)," after testing
Phase 20.39's creation templates left behind test roles with no way to
remove them from within clasm. Three scoping questions, resolved via
`AskUserQuestion`:

1. **Scope: delete a role, or also attach/detach policies on an existing
   one?** Chose **delete + attach/detach**, not delete alone -- narrowing
   to only delete would still leave no way to fix a role's policies
   without going back to the AWS Console, undermining the whole point of
   this domain.
2. **A role's dedicated policy (created alongside it by a Phase 20.39
   template, named `<role>-policy`): delete it too, or leave it?** Chose
   **delete both together**, since an orphaned policy left behind is
   exactly the same "test-role clutter" problem this feature exists to
   solve -- but only if `ListEntitiesForPolicy` confirms nothing else
   still uses it, never assumed from the naming convention alone.
3. **Confirmation gate: plain yes/no, or type-to-confirm?** Chose the
   **stronger, type-to-confirm tier** (`ConfirmDestructive`, matching
   Terminate Instance/Remove AMI) for Delete Role specifically --
   deleting a role is irreversible and the blast radius (anything still
   assuming it) isn't always fully visible to the operator. Attach/Detach
   Policy use the lighter plain `Confirm` instead: each is trivially
   reversible via its own paired action, unlike a deleted role.

**Decision.** Three new IAM-domain actions (PLAN.md Phase 20.40):

- **Delete Role** -- picker filtered to DLD-owned roles only (no
  legitimate reason to offer deleting a role clasm doesn't recognize as
  DLD's); refuses upfront, before any confirmation, if the role is still
  referenced by an instance profile (detaching that is already Phase
  20.33's own "Associate/replace IAM instance profile" action --
  automating it here would be scope creep into an existing, separate
  workflow); deletes inline policies, detaches managed policies, deletes
  the role, then deletes its dedicated policy if unused elsewhere --
  order matches AWS's own documented `DeleteRole`/`DeletePolicy`
  preconditions (confirmed via the vendored SDK's doc comments).
- **Attach Policy to Role** -- pick a DLD-owned role, pick *any*
  customer-managed policy to attach (not filtered to DLD-owned policies
  -- attaching an IMSS- or AWS-authored policy to a DLD-owned role is a
  legitimate, expected case), confirm, attach.
- **Detach Policy from Role** -- pick a DLD-owned role, pick one of its
  currently-attached policies, confirm, detach. Never also deletes the
  detached policy -- that cascade is specific to Delete Role's own
  dedicated-policy convention, not a general rule, since a detached
  policy may still be in use elsewhere or may never have been clasm's to
  manage at all.

Every action defensively re-checks `RequireDLDOwned` even though the
picker already filtered to DLD-owned resources -- belt-and-suspenders,
matching `deleteIAMRoleConfirmed`'s own re-check, and this is
`RequireDLDOwned`'s first real caller since it was built ahead of any
caller in Phase 20.36.

---

