---
id: "0165"
title: "Real bug: Configure clasm edits didn't take effect until restart -- reload cfg after returning from the Configure clasm domain"
date: "2026-08-18"
status: accepted
kind: correction
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
uuid: "96a9f3b3-9025-4e84-bc1e-75e8035ef482"
origin_host: "MACMINI-RD.local"
---

**Context.** Found live testing Restore SQL Backup against
`caltechdata-restore-test`: added an `rdm_postgres_config` rule via
Configure clasm's "Edit RDM Postgres config," hit "Save," confirmed the
correct values landed in `~/.clasm` on disk, then re-ran Restore SQL
Backup in the *same still-running* clasm process -- and it still
resolved the wrong `db_name`/`db_user`. Root cause: `RunConfigureMenu`
(`configure_menu.go`) calls `config.Load(configPath)` into its own,
independent `cfg` variable, edits that copy, and its own `Save` action
writes it to disk correctly -- but `main()`'s own `cfg` (loaded once at
startup, and what every other domain's closures -- `RunSQLBackup`/
`RestoreSQL`/`ArchiveSQL`/`ArchiveOpenSearch`/the IAM domain's
`OriginTag` -- read `cfg.XXX` from at call time) was never reloaded
after that. A full restart was silently required for *any* Configure
clasm edit (RDM Postgres config, backup directories, Origin tag) to
actually take effect elsewhere in the same running process -- not just
the already-documented, deliberate exception for Regions (whose
dependent SSM/EC2 clients genuinely can't be rebuilt without a restart).

**Decision.** `main()`'s `Configuration` domain closure reloads
`configPath` via `config.Load` immediately after `RunConfigureMenu`
returns, and reassigns it to the same outer `cfg` variable (not a new
`:=` shadow). Every other closure reads `cfg.XXX` live at call time
already (confirmed by reading each one -- none pre-extract a value at
closure-creation time into a separately-captured variable), so this one
reassignment is sufficient to make every Configure-clasm-editable
setting take effect immediately, without a restart. Regions stay the
one deliberate exception, unchanged: `ssmClients`/`ec2Clients`/etc. are
still only built once at startup from the original region list.

**Rationale.** Minimal, surgical fix at the one call site where the
staleness is introduced, rather than restructuring how `cfg` is threaded
through `main()`. No dedicated test -- this is pure procedural wiring
inside `main()`, which (unlike the `workflow` package) has no existing
test harness in this project; verified via live use instead (the exact
repro that surfaced the bug).

**Consequences.** `cmd/clasm/main.go`'s `Configuration` closure only.
See PLAN.md Phase 20.58.

---

