---
id: "0160"
title: "Associate/Replace IAM instance profile: recoverable `EntityAlreadyExists`"
date: "2026-08-18"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: ["0159"]
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "d2f7a164-292c-49e5-bfea-228a6736d727"
origin_host: "MACMINI-RD.local"
---

**Context.** Found 2026-08-18 creating the `rdm-backups` role
(`agents/knowledge.db` clasm observation id 177): in
`AssociateOrReplaceInstanceProfile`'s "create a new instance profile"
path, `createInstanceProfileForRole`'s "New instance profile name"
prompt defaults to the picked role's own name (`ui.WithDefault(role.Name)`)
-- exactly the name likely to already exist if a profile was created for
that role before, as happened here. On `EntityAlreadyExists`, the loop
only re-prompts for a *different* name -- no way to say "use the
existing one instead." Cancelling that prompt (Esc/Ctrl+C) propagates a
raw error all the way up through `promptIAMInstanceProfileOrCreate`/
`createInstanceProfileInteractive`, aborting the entire Associate/replace
workflow instead of returning to the instance-profile picker -- recovery
required a full clasm restart.

**Decision.** Fix both halves of the trap, combining TODO.md's options
(b) and (c) ((a) is subsumed by (b)):
1. On `EntityAlreadyExists`, `createInstanceProfileForRole` offers "use
   the existing instance profile" alongside "type a different name,"
   instead of only ever re-prompting for a name. Picking "use existing"
   returns that profile's name with `created=false` -- the same shape
   `createInstanceProfileInteractive` already returns for its "no
   SSM-capable roles" case, not a fabricated `created=true`.
2. A cancellation from `ui.Prompt`/`pickRole` inside this flow is treated
   as "no profile created, not an error" (mirroring that same existing
   `created=false, err=nil` return), so `promptIAMInstanceProfileOrCreate`'s
   outer loop redisplays the instance-profile picker instead of the error
   propagating out of `AssociateOrReplaceInstanceProfile` entirely --
   matches this codebase's own established `cancelledIsNil` convention,
   applied here for the first time to a *nested* prompt inside a picker
   loop rather than the loop's own top-level pick.

**Rationale.**
- The actual reported case is "the default name collided" -- offering
  "use existing" fixes that directly, rather than relying on the operator
  to notice and explicitly filter for the existing entry in the outer
  picker (option (a)), which is the same discoverability problem that
  caused the original confusion.
- The cancel-recoverability fix matches a pattern already applied
  everywhere else in this codebase (Phase 20.3-era `cancelledIsNil`) --
  extending it here is consistent, not a new mechanism.

**Consequences.** No change to the instance-profile picker's own
filtering/display -- this is about not trapping someone who picks
"Create new" instead of the existing entry, not about making the picker
smarter. See PLAN.md Phase 20.56, DESIGN.md "Associate/Replace IAM
Instance Profile: Recoverable `EntityAlreadyExists`."

**Real-AWS-verified, 2026-08-19.** A zero-net-change test against
`caltechdata-restore-test` (its already-attached `rdm-backups` role has
a same-named instance profile, so any path through this workflow ends
up back at the same association) confirmed both halves of the fix:
picking "Create new instance profile" for role `rdm-backups` correctly
hit `EntityAlreadyExists` on the default name, the "use the existing
one instead" prompt appeared and worked (`aws ec2 describe-instances`
confirmed `IamInstanceProfile` unchanged, still `rdm-backups`,
afterward); a second run cancelling the name prompt (Esc/Ctrl+C)
correctly returned to the instance-profile picker rather than aborting
the whole workflow, and completing from there left the same unchanged
state.

---

