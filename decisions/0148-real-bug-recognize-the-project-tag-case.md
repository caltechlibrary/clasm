---
id: "0148"
title: "Real bug: recognize the Project tag case-insensitively"
date: "2026-07-29"
status: superseded
kind: correction
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: ["0149"]
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "1dfa7133-0c35-42e3-89e8-51aeddee5c0c"
origin_host: "MACMINI-RD.local"
---

**Context.** After the previous three Run SQL Backup bugs were fixed,
live re-testing against CaltechAUTHORS production still resolved
`db_name`/`db_user` to "newauthors" instead of "caltechauthors" -- even
after the user removed the `rdm_postgres_config` override entirely, a
completely fresh run still produced the same wrong value, proving this
wasn't a stale-config issue. Traced to `inventory.tagValues`
(`internal/inventory/instances.go`): it matches the `Project` tag key by
exact string equality, but this instance's real tag is spelled lowercase
`project` (confirmed via the earlier `EC2.DescribeInstances` debug-log
capture) -- so `inst.Project` was silently empty, and the
`cmp.Or(inst.Project, inst.Name)` fallback (decided earlier this same
session) fell through to `Name` every time, regardless of config state.

Before changing shared code, checked whether this was a one-off typo on
this single instance or a real fleet-wide pattern: scanned every
instance seen across every local `--debug` JSONL log (not guessed) and
found a clean, consistent split -- every real production/legacy
instance (`newauthors`, `oldauthors`, `oldcaltechdata`,
`caltechdata-test`, `new-data`, `etd-workflow-v0.0.1`) uses lowercase
`project`, predating clasm and tagged by whatever originally provisioned
them; every instance clasm itself creates (`test-clasm-*`,
`granian-rdm-v14-*`, `tmorrell-rdm-granian-test`) uses `Project`. No
instance in the fleet uses both, so there's no ambiguity case.

**Decision.** `tagValues` now matches `Project` case-insensitively
(`strings.EqualFold`), since Run SQL Backup's whole purpose is to work
against exactly the real production fleet that uses the lowercase form.
`Name` and `Environment` stay exact-match -- `Name` is an
AWS-console-enforced convention (the console's own Name field always
writes the tag key as exactly `Name`), and no evidence of a similar
`Environment`-casing split was found.

**Consequences.** This is shared code (`internal/inventory`, used by
every feature that groups/filters/displays by Project, not just today's
new RDM workflows) -- any existing feature relying on `Project` now
additionally recognizes the lowercase form too, strictly a superset of
what it recognized before, with no observed fleet-wide conflict. New
regression test `TestListInstances_RecognizesLowercaseProjectTag`
reproduces the real incident directly. This is the fourth real bug this
session that only live-AWS testing surfaced -- none of it was
guessable from reading code or docs alone; each was root-caused by
checking real data (the `--debug` log, a direct `docker ps`, and here, a
survey across every locally-available debug log) before writing a line
of the fix.

---

