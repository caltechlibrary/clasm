---
id: "0143"
title: "Restore SQL Backup also discovers-and-reconciles its own target, not just Run SQL Backup's source"
date: "2026-07-29"
status: accepted
kind: decision
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
uuid: "9e090883-c870-4f90-9802-a7442113271c"
origin_host: "MACMINI-RD.local"
---

**Context.** Once Run SQL Backup's source-side container/DB discovery
was decided (see "RDM Postgres container/DB naming," below), the same
question applied to Restore SQL Backup (PLAN.md Phase 20.50): should its
*target* instance's Postgres container/DB identity also be discovered
live, or is trusting `rdm_postgres_config`/a Name-tag assumption enough
there, since the target is presumably already a known, existing
instance?

**Decision.** Restore SQL Backup performs the exact same live
discover-and-reconcile (`docker ps --filter ancestor=postgres`, reconcile
with `rdm_postgres_config`) on its *target* instance, immediately before
running the DROP/CREATE/load sequence -- the user's explicit call, for
the same reason Run SQL Backup does it on its source: the restore target
can be a completely different instance than whatever last touched
`rdm_postgres_config` (e.g. restoring CaltechAUTHORS' own backup onto a
fresh test clone with its own, possibly-never-seen-before container
identity), so a stale or unrelated config entry -- or a bare Name-tag
assumption -- isn't safe to trust blindly here either.

**Consequences.** The shared discovery/reconcile helper (new
`internal/workflow/rdm_postgres_config.go`, built as part of PLAN.md
Phase 20.52 but consumed by both Phase 20.50 and Phase 20.52) is used by
both workflows identically -- no separate, restore-specific variant.
Restore SQL Backup's own work items gain this as a new step before the
existing destructive-overwrite confirm, ahead of the DROP/CREATE/load
sequence. Phase 20.50 now depends on Phase 20.52's shared helper file
existing, in addition to its own already-resolved load-command work.

---

