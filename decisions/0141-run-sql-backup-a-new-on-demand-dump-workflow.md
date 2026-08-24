---
id: "0141"
title: "Run SQL Backup: a new on-demand dump workflow, chaining into Archive SQL rather than replacing it"
date: "2026-07-29"
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
uuid: "34ea3baf-061c-458a-9097-c1558ed50206"
origin_host: "MACMINI-RD.local"
---

**Context.** Feature 11 (Archive SQL Backup to S3, relocated into this
domain unchanged in Phase 20.48) only ever uploads and trims files
*already present* in a backup directory -- it never runs `pg_dump`
itself, fully depending on the existing nightly cron job (running
`invenio-sql-backup.bash` independently of clasm) to have produced them
first. The user wants to be able to do a full backup via clasm alone, no
pre-installed script or SSH session required -- but also doesn't want to
lose the cron job, since it's what keeps backups running when no
operator is available to use clasm at all (e.g. staff on leave).

**Decision.** A new "Run SQL Backup" action triggers `pg_dump` directly
via SSM into the operator-chosen backup directory, then prompts "Continue
to Archive SQL Backup to S3 now?" -- on yes, it invokes the *same*
Archive SQL Backup closure directly (a full, ordinary run of
`BackupArchiveAndTrim`, not a special abbreviated path). Archive SQL
Backup itself is untouched and remains fully independent, still the
mechanism that manages the cron job's own output; the cron job stays in
production exactly as it is. Both workflows share the same
`BackupHistory`/`backup_directories` recall for instance/directory
choices (writing through the same `hist.Save` callback), so chaining
from Run SQL Backup into Archive SQL means confirming pre-positioned
defaults, not re-typing them.

**Consequences.** New `internal/workflow/run_sql_backup.go` (PLAN.md
Phase 20.52); `rdmMenuItems` gains a fifth entry, "Run SQL Backup,"
placed first (before "Archive SQL Backup to S3") since it's the natural
first step for an instance with no existing dump yet. No change to
Archive SQL Backup's own behavior or its menu position. See also "RDM
Postgres container/DB naming," below, for how the dump command itself
resolves the container/DB to run against.

---

