---
id: "0158"
title: "Delete Role: correct the wrong-remedy message, add the missing instance-profile-membership actions"
date: "2026-08-18"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "7388f692-2adf-4ab5-9333-816f6ed21fa1"
origin_host: "MACMINI-RD.local"
---

**Context.** Found 2026-08-18 cleaning up the now-unused
`rdm-opensearch-backup` role after rolling out the consolidated
`rdm-backups` role (`agents/knowledge.db` clasm observation id 180).
Delete Role refused with "role %q is still referenced by instance
profile(s) %s -- detach it first (Compute domain, \"Associate/replace IAM
instance profile\")" -- but that refusal (`IAMRoleDetail.ReferencedByProfiles`,
`iam_detail.go`) is based on `iam:ListInstanceProfiles`' `Roles` field,
i.e. whether this role is a *member* of an instance-profile object.
Associate/replace IAM instance profile only ever changes an EC2
instance's *association* to a profile -- it has no effect on a profile's
role membership. Following the message's own suggested remedy can never
unblock this refusal. clasm has no action anywhere for
`iam:RemoveRoleFromInstanceProfile` or `iam:DeleteInstanceProfile`
(confirmed via a source grep, zero matches for either) -- the operator
worked around it via the raw AWS CLI.

**Decision.** Keep Delete Role's refusal exactly as scoped by the
2026-07-23 CRUD-completion decision (below, "...support CRUD for
DLD-owned roles") -- don't make it auto-cascade into removing the role
from any instance profile. Instead:
1. Correct the message to name the real relationship ("is a member of
   instance profile(s) %s") and point at a real remedy (below), not
   Associate/replace.
2. Add the missing capability as two new small, standalone IAM-domain
   actions, mirroring Attach/Detach Policy's existing shape (Phase
   20.40) rather than folding either into Delete Role itself: **"Remove
   role from instance profile"** (`iam:RemoveRoleFromInstanceProfile`,
   gated the same DLD-owned-role way as every other role-mutating IAM
   action) and **"Delete instance profile"** (`iam:DeleteInstanceProfile`,
   which AWS itself refuses if a role is still attached -- so the
   natural order is remove-role-then-delete-profile). Both use a plain
   `Confirm`, not `ConfirmDestructive`, matching Attach/Detach Policy's
   own reversibility-based tiering.

**Rationale.**
- Nothing about this finding changes the 2026-07-23 reasoning for keeping
  "detach from a running instance" and "delete the role" as separate,
  composable actions -- it only reveals the message described the wrong
  mechanism and clasm was missing the actual remedy as a standalone
  action.
- Not auto-deleting the instance profile once it's empty: an operator
  might intentionally keep an empty profile around (e.g. about to attach
  a different role) -- deletion stays an explicit second step.

**Rejected alternatives.**
- *Have Delete Role auto-remove the role from any instance profile it's
  a member of* -- rejected, same scope-creep reasoning as 2026-07-23:
  a role-deleting workflow silently mutating instance-profile membership
  as a side effect is a bigger blast radius than this bug warrants,
  especially since the profile might still be genuinely in use by a
  running instance in the general case (this specific incident's profile
  wasn't, but the check can't distinguish that).

**Consequences.** See PLAN.md Phase 20.55, DESIGN.md "Delete Role's
Wrong-Remedy Message + Missing Instance-Profile-Membership Capability."

**Real-AWS-verified, 2026-08-19.** A disposable `test-verify-2055-role`/
`test-verify-2055-profile` pair (tagged `origin=dld` so the DLD-owned-only
picker filter would surface them, deliberately never attached to any EC2
instance) confirmed all four steps end to end: Delete Role's corrected
message named the real blocker and pointed at the right action; Remove
Role from Instance Profile detached it (confirmed via `iam:GetInstanceProfile`
showing zero roles); Delete Instance Profile removed the now-empty
profile (confirmed `NoSuchEntity` afterward); Delete Role then succeeded
cleanly once unblocked. Both fixture resources ended up fully deleted,
no cleanup left behind.

---

