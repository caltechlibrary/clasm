---
id: "0146"
title: "Default db_name/db_user to the instance's Project tag, not its Name tag"
date: "2026-07-29"
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
uuid: "d7703f3b-2af0-421d-81a3-4a208af03434"
origin_host: "MACMINI-RD.local"
---

**Context.** Real bug, found via live testing against CaltechAUTHORS
production immediately after the `docker ps` ancestor-filter fix above:
Run SQL Backup correctly discovered `caltechauthors-db-1`, but ran
`pg_dump` against a database named "newauthors" -- which doesn't exist.
Traced via the `--debug` log's `EC2.DescribeInstances` response for
`i-0c4c81336aea33d27`: its `Name` tag is literally `newauthors` (a
legacy label), while a separate `project` tag correctly reads
`caltechauthors`. The design's fallback (`inst.Name`, matching
`BackupDirectoryFor`'s own Name-tag convention) silently picked the
wrong one.

**Decision.** `RDMPostgresConfigFor`'s single `instanceName` parameter
split into two: `instanceName` (unchanged -- still the `Pattern`-
matching key, an EC2 Name tag, for consistency with
`BackupDirectoryFor`) and a new `fallbackIdentifier` (what `dbName`/
`dbUser` actually default to when no override matches). Callers
(`resolveRDMPostgresConfig`, and `runSQLBackup` which computes it) pass
`cmp.Or(inst.Project, inst.Name)` -- prefer the `Project` tag, fall back
to `Name` only for instances that don't use the Project/Environment
tagging convention at all (DESIGN.md, "Tag Management Domain" already
establishes this convention; `inventory.Instance.Project` was already
populated from it, no new plumbing needed). `Pattern` matching itself
deliberately still keys on `Name`, not `Project` -- changing that too
would diverge `rdm_postgres_config` from every other config section's
identical Name-tag-matching convention, a bigger change than this bug
warrants.

**Consequences.** `RDMPostgresConfigFor`'s signature widened (config.go,
config_test.go, rdm_postgres_config.go, rdm_postgres_config_test.go all
updated); `runSQLBackup` also now prints the resolved container/dbName/
dbUser to the operator immediately before running the dump (`"Using
Postgres container %q, database %q, user %q."`), so a wrong resolution
is visible right away rather than only discoverable afterward by
inspecting the resulting file on disk -- exactly how this incident was
first noticed. New regression test
`TestRunSQLBackup_UsesProjectTagOverNameTagForDatabaseName` reproduces
the real incident directly (Name="newauthors", Project="caltechauthors").
Restore SQL Backup (Phase 20.50, not yet implemented) will need the
identical `cmp.Or(inst.Project, inst.Name)` treatment on its own target
instance once built.

---

