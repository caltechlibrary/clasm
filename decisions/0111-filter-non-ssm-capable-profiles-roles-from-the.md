---
id: "0111"
title: "Filter non-SSM-capable profiles/roles from the picker, don't just annotate them"
date: "2026-07-22"
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
uuid: "0d4d296f-6a2e-4033-a5e8-c8e6bfc1fc6d"
origin_host: "MACMINI-RD.local"
---

**Context.** Live testing of Phase 20.33 Part 2 (SSM-capable instance
profile enforcement): creating a launch template from cloud-init YAML
for the Granian test instance, the operator hit "Create new instance
profile" and was shown the account's full IAM role list, annotated per
role with SSM-capability -- but with real accounts holding many roles
for unrelated services (Lambda execution roles, service-linked roles,
...), a long annotated list that's mostly "cannot be selected" entries
was harder to use than no annotation at all, not easier. Since SSM
support is now a hard, unconditional requirement (no opt-out, same as
IMDSv2), there's no scenario where picking a non-capable entry is ever
valid -- showing it at all just adds noise to scan past.

**Decision.** `buildInstanceProfileChoices`/`buildRoleChoices`
(`create_instance_profile.go`) now filter out non-capable profiles/roles
entirely rather than including them with a `" -- NOT SSM-capable..."`
label suffix. The `ssmCapable` field and the post-pick rejection branch
in `promptIAMInstanceProfileOrCreate`/`createInstanceProfileInteractive`
are removed as dead code -- filtering guarantees every remaining choice
is already capable, so there's nothing left to reject. If filtering
empties the role list entirely, `createInstanceProfileInteractive`
reports it the same way it already reports "no roles at all in this
account" (a clear message, `created=false`, not an error) -- same shape,
new reason.

**Rejected alternative (superseded).** *Show every profile/role,
annotated, reject on selection* -- this session's original Part 2
design. Chosen at the time because DESIGN.md's own "Not decided yet"
note explicitly left "shown-but-blocked vs. ... " open; live usage
answered it: for a list of any real size, annotation-without-filtering
just makes the operator read past irrelevant entries to find the ones
that matter, providing no benefit over not showing them at all (there's
no "pick anyway" override to explain, since SSM support isn't
optional).

**Consequences.** Simpler code (fewer fields, fewer branches) and a
shorter, more usable list matching what the enforcement itself already
requires. If an account has zero SSM-capable roles, the operator now
sees a single clear "none found" message instead of a long list of
entries none of which can be selected.

---

