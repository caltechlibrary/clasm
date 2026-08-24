---
id: "0169"
title: "Restore OpenSearch Snapshot from S3: a fifth correction -- the restore index prefix must be editable, not silently derived from the target's own tags"
date: "2026-08-19"
status: accepted
kind: correction
trigger: live-test
project: clasm
phase: "20.51"
supersedes: []
superseded_by: []
relates_to: ["0168"]
initiative: ""
session: ""
decisions: []
tags: []
uuid: "2bb68244-48dc-470b-b65b-59c6fc01bb54"
origin_host: "MACMINI-RD.local"
---

**Context.** Setting up a real-AWS test of Phase 20.51 against
`caltechdata-restore-test`: the plan was to restore CaltechDATA
production's real OpenSearch snapshot (archived that same morning,
`rdm-20260819-160031`) onto this dev/test instance -- a genuine
cross-instance restore, unlike every other operation built so far in
this domain, which has only ever been tested as an instance restoring
its own prior backup onto itself. Pre-test reconnaissance (checking
real facts before running a live test, per this project's established
practice) surfaced a real problem before any command was even sent:
`caltechdata-restore-test`'s own Project/Name tags are both
`"caltechdata-restore-test"`, entirely unrelated to the snapshot's real
index prefix, `"caltechdata"` (confirmed directly from that same
morning's actual Archive OpenSearch Snapshot command body, which used
`"caltechdata-rdmrecords-*,caltechdata-users-*,..."` patterns).
`restoreOpenSearchSnapshot`'s current code computes `indexPrefix :=
cmp.Or(inst.Project, inst.Name)` unconditionally from the *target*
instance's own tags, and uses that one value both to detect conflicting
indices and to scope the actual `_restore` call's own `indices`
parameter. That's correct only when source and target happen to share
the same tag-derived prefix (the self-restore-after-disaster case this
domain has exclusively been built and tested against so far) --
silently wrong otherwise: OpenSearch's `_restore` accepts
`ignore_unavailable:true`, so a mismatched pattern doesn't error, it
just restores zero indices and reports success -- the identical
"fails quietly, not loudly" trap Archive OpenSearch's own Name-vs-Project
bug hit before (DECISIONS.md, "Real bug: Archive OpenSearch Snapshot's
index-match patterns used the Name tag, not the Project tag").

**Decision.** Add an editable prompt, "OpenSearch index prefix in the
archived snapshot to restore," defaulting to the same
`cmp.Or(inst.Project, inst.Name)` value the code already computes (so
the common self-restore case needs no new input, just an Enter
keypress), but overridable -- covering the cross-instance case this
real test actually needs. Placed alongside the existing conflict-
detection step (still before any S3 prompt, correction 4's own
step-order lesson unaffected -- prompting for a string is not "S3
activity").

**Rationale.** Same fix shape this project has already used twice for
this exact failure mode (Archive OpenSearch's own Project-vs-Name fix;
Run SQL Backup's `db_name`/`db_user` preferring Project over Name) --
but unlike those two, there is no live "discover the real value from
the source" mechanism available here: a restore only ever connects to
the *target* instance, never the source the snapshot came from, so an
auto-discovery isn't possible and a plain editable prompt is the honest
design. Caught via pre-test reconnaissance -- comparing the real
snapshot's own already-known index-pattern content against the real
target's own tags -- before a live test run would have "succeeded"
while quietly restoring nothing. General lesson worth remembering: for
any cross-instance operation, check whether a value silently derived
from "the instance in hand" is implicitly assumed to also describe some
other party (here, the snapshot's own origin) before trusting a
single-instance default.

**Consequences.** PLAN.md Phase 20.51's Work Item 2 revised and
implemented test-first, same day: `restore_opensearch.go`'s
`indexPrefix` is now a `ui.Prompt` (default `cmp.Or(inst.Project,
inst.Name)`, same as before), with a new regression test
(`TestRestoreOpenSearchSnapshot_IndexPrefixPromptOverridesTargetTagDefault`)
confirming an override reaches every downstream command, not the
target's own tag-derived default; every existing integration test's
input sequence updated for the new prompt line. `go build`/`vet`/
`test -race`/`gofmt` all clean.

**Resolved, 2026-08-19: the live test succeeded on the first attempt
with the fix in place.** Restored CaltechDATA production's real
snapshot (`rdm-20260819-160031`) onto `caltechdata-restore-test`,
overriding the new prompt to `caltechdata` as planned. Every step
(conflict check, sync-down, register-repo, trigger-restore, recovery
poll, verification) reported `Success`; recovery reached `done` on all
43 snapshot-type shard rows. Independently re-confirmed against the
instance directly (not just clasm's own report): 42 indices restored,
all `yellow`/`open`, real doc counts throughout (e.g. 77,258 records,
222,909 request events). Phase 20.51 (Restore OpenSearch Snapshot from
S3) is now fully real-AWS-verified end to end -- see PLAN.md Phase
20.51 (updated).

Confirmed separately, same reconnaissance pass, that no other blocker
remains: `path.repo` is already correctly configured on this instance
(repo registration confirmed live, `{"acknowledged":true}`) and its
attached `rdm-backups` instance profile already grants `s3:GetObject`
on the OpenSearch backups bucket -- no retrofit or IAM change needed
before this test can run.

---

