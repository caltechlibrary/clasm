---
id: "0161"
title: "Real bug: instance/AMI creation wrote the Project tag key capitalized, the fleet convention is lowercase"
date: "2026-08-18"
status: accepted
kind: correction
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: ["0149"]
initiative: ""
session: ""
decisions: []
tags: []
uuid: "13d56d10-6879-439d-965c-d2e5ba807f0d"
origin_host: "MACMINI-RD.local"
---

**Context.** Found while preparing to launch a fresh dev/restore-test
instance via clasm, ahead of implementing Phases 20.50/20.51. All three
of clasm's instance/AMI-creation call sites (`launch_instance.go`,
`launch_from_cloud_init.go`, `create_ami_from_instance.go`) still wrote
the tag key as capitalized `"Project"`. But `inventory.tagValues`'
matching was reverted 2026-07-29 (see "Revert Project tag matching to
exact-match lowercase 'project' -- the fleet is clean now", below) to
exact-match *lowercase* `"project"` only, once every existing
capitalized-`Project`-tagged resource in the account was retagged to
match the team's real standard. That reversion's own doc comment already
noted, in the past tense, that "instances clasm itself created used
'Project'" -- but nobody went back and fixed the *write* side to match
going forward. The practical effect: every instance/AMI clasm creates
from 2026-07-29 onward gets a `Project` tag its own `inst.Project` field
can never read back, silently falling through to the `Name`-tag fallback
everywhere `cmp.Or(inst.Project, inst.Name)` is used (Run SQL Backup's
db name/user resolution, Archive OpenSearch Snapshot's index-prefix
resolution) -- the identical failure *shape* as the two incidents that
`cmp.Or` fallback exists to guard against, just triggered by the write
side instead of a legacy tag.

**Decision.** Change all three call sites to write `"project": project`
(lowercase), matching `tagValues`' read side exactly. No change to the
user-facing prompt label ("Project tag") -- only the tag key stored in
AWS.

**Rationale.**
- Same fix shape as every other tag-casing incident in this project
  (2026-07-29's two entries, 2026-08-17's OpenSearch index-prefix fix) --
  once again, the fleet's *real* convention is the authority, not
  whatever a given code path happens to write.
- Caught before it caused a second live incident, by re-reading
  `tagValues`' own doc comment while working on unrelated infrastructure
  setup -- worth remembering the general lesson: a "the fleet is clean
  now" reversion decision is only complete once every *write* path is
  checked against the same convention, not just the read side and the
  already-existing data.

**Consequences.** `TestCollectLaunchInstanceParams`,
`TestCollectLaunchInstanceParamsFromCloudInit_HappyPath` (both already
asserted on this tag, updated for the new key) and a new
`TestCollectCreateAMIParams_ProjectTagKeyIsLowercase` (no pre-existing
assertion at this call site) lock in the fix. Every other test file using
`"Project"` as a plain, arbitrary example key for a *generic* Tag
Management/CRUD or S3-bucket-tagging fixture (`bucket_tags_test.go`,
`manage_tags_test.go`, `tag_management_test.go`, plus the execute/build-
layer tests that just forward an already-built `Tags` map verbatim --
`create_ami_execute_test.go`, `launch_execute_test.go`,
`launch_template_create_test.go`) is correctly left untouched, per the
same "arbitrary example key vs. the real convention" distinction the
2026-07-29 reversion itself already drew. See PLAN.md Phase 20.57.

---

