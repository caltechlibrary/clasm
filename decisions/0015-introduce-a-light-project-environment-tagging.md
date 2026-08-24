---
id: "0015"
title: "Introduce a light Project/Environment tagging convention"
date: "2026-07-01"
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
uuid: "ffc3ce07-44eb-4b63-b07c-e0b539be856f"
origin_host: "MACMINI-RD.local"
---

**Context.** The stated goal for this tool is to speed up upgrading RDM
deployments and creating accurate test environments across production and
development instances. A read-only check of the live account
(`aws ec2 describe-instances`) found tagging is inconsistent today: a
`project` tag exists on some instances (`caltechauthors`, `caltechdata`,
`caltechthesis`) but not others (`new-plots`, `thesis`,
`authors-test-recovery`), and there is no dedicated environment tag —
production vs. test is encoded only in the instance *name string* (e.g.
`caltechdata-test` vs. `oldcaltechdata`). `newauthors` is additionally
managed via an EC2 Launch Template, not ad hoc parameters.

**Decision.** The tool suggests/requires `Project` and `Environment`
(`production` | `development` | `test`) tags when creating new instances
and AMIs, uses them to group the resource listing, and adds extra
confirmation friction for destructive actions (AMI removal) on anything
tagged `production`. Existing untagged resources display as "unknown"
until tagged — the tool does not retroactively rewrite tags on resources it
didn't create.

**Rationale.**
- Directly serves the stated goal: distinguishing production from
  development/test at a glance, and grouping by application (`Project`),
  is exactly what "manage our RDM production and development instances"
  requires
- Inferring environment from free-text instance names (today's de facto
  approach) is fragile and inconsistent, as the live-account check showed
- Extra confirmation on production-tagged resources targets friction at
  the resource class where a mistake is most costly, rather than applying
  uniform friction everywhere

**Rejected alternatives.**
- *Work with what exists, no enforcement* — keeps inferring environment
  from the name string with no structured signal; considered, but doesn't
  move the account toward consistency and leaves "is this production?"
  a matter of reading a name carefully rather than checking a tag
- *Retroactively tag all existing resources* — out of scope for this tool;
  a one-time cleanup task, not an ongoing tool responsibility

**Consequences.**
- `PLAN.md` Phase 2 (listing) groups/filters by `Project`/`Environment`;
  Phases 4/5 (creation) prompt for and default these tags; Phase 6
  (removal) adds a heightened warning for `Environment=production`
- `DESIGN.md`'s IAM permission list must include `ec2:CreateTags` and
  `ec2:DescribeTags` (already present in `software_requirements.md`'s
  policy but missing from `DESIGN.md`'s own Assumptions section — fixed in
  this update)
- No migration or backfill of existing untagged resources is planned

---

