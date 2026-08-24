---
id: "0157"
title: "Run SQL Backup: drop the Archive-SQL auto-chain, rename to \"Generate SQL Backup\""
date: "2026-08-18"
status: accepted
kind: refinement
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "037fa24b-6da2-46c0-a9fc-79b40628fb19"
origin_host: "MACMINI-RD.local"
---

**Context.** Phase 20.52 added a `Confirm("Continue to Archive SQL
Backup to S3 now?")` chain at the end of Run SQL Backup. Live use found
this confusing -- the user's explicit ask (TODO.md) is that this action
should only generate the local `.sql.gz` dump and return to the RDM
Backup & Restore menu, and that the label "Run SQL Backup" should become
"Generate SQL Backup" to match.

**Decision.** Remove the chain entirely, not just default its answer to
"no." `runSQLBackup`'s trailing `Confirm` branch and its `archiveSQL
func(ctx context.Context) error` parameter are removed; the function
returns immediately after reporting the dump's success/failure.
`RunSQLBackup`'s exported signature drops the parameter; `cmd/clasm/main.go`'s
wiring no longer passes an `archiveSQL` closure. `rdmMenuItems`'s label
changes from "Run SQL Backup" to "Generate SQL Backup" (`rdm_menu.go`,
one call site) -- Go identifiers (`RunSQLBackup`/`runSQLBackup`/
`run_sql_backup.go`) are left unchanged, per Phase 20.43's own precedent
that user-facing labels and internal Go names don't need to match.

**Rationale.**
- Matches the user's explicit ask exactly -- no design judgment call
  needed on scope.
- Archive SQL Backup to S3 remains a separate, always-available menu
  entry directly below/near this one; an operator who wants to archive
  right after generating a dump picks it next -- same number of
  keystrokes as confirming "yes" today, without a backup action doing
  something beyond backing up as a surprise.

**Consequences.** See PLAN.md Phase 20.54, DESIGN.md "Run SQL Backup:
Drop the Archive-SQL Auto-Chain, Rename to \"Generate SQL Backup\"."

---

