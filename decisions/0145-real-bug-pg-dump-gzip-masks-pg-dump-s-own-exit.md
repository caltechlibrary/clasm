---
id: "0145"
title: "Real bug: pg_dump | gzip masks pg_dump's own exit status -- match invenio-sql-backup.bash's real two-step approach instead"
date: "2026-07-29"
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
uuid: "857ac712-36e3-466c-8263-6adca2a1b824"
origin_host: "MACMINI-RD.local"
---

**Context.** Same live-testing session as the two bugs above: after the
`docker ps` fix landed, Run SQL Backup against CaltechAUTHORS production
reported success, but the resulting file was only 20 bytes (vs. ~985MB
for a real backup already sitting in the same directory from the cron
job). Root cause: `buildSQLDumpCommand` piped `pg_dump` directly into
`gzip` (`pg_dump ... | gzip > file`) -- a shell pipeline's exit status is
its *last* command's, so `pg_dump` failing (in this case, connecting to
the nonexistent "newauthors" database, see the fallback-identifier
decision above) was invisible: `gzip` compressed the empty/error output
and exited 0 regardless, so SSM reported `Success` on a garbage file.
Checking the real `invenio-sql-backup.bash` (the script this command was
supposed to match "exactly") revealed it never pipes at all -- it
redirects `pg_dump` to a plain `.sql` file first, then runs `gzip -f` on
that file as a wholly separate second step.

**Decision.** Match the real script's actual two-step structure, not a
piped one-liner: `set -e; docker exec <container> pg_dump ... > <file>.sql;
gzip -f <file>.sql` -- `set -e` (this project's own established pattern
for a multi-step SSM command, `ssm_grow.go`'s
`rootFilesystemGrowCommand`) means `pg_dump`'s own failure aborts the
script before `gzip` ever runs. No pipe exists to mask anything.

**Consequences.** `buildSQLDumpCommand` rewritten; its target path
changed from `<container>-<db>-<date>.sql.gz` (passed directly to
`gzip`'s stdout redirect) to `<container>-<db>-<date>.sql` (pg_dump's
own redirect target, which `gzip -f` then renames to `...sql.gz` in
place) -- the *final* filename convention is unchanged, matching
`invenio-sql-backup.bash` exactly, so files Run SQL Backup produces
remain indistinguishable from the cron job's own output. New regression
test `TestBuildSQLDumpCommand_NoPipeAvoidsExitStatusMasking` asserts no
`|` appears in the built command and `set -e` does. General lesson,
consistent with this project's repeated experience (Phase 20.34's
gzip-insufficiency, this session's own `docker ps` filter bug): "matches
the real script" needs to mean matching its actual command *structure*,
not just its eventual output shape -- a piped reimplementation that
produces the same filename convention on the happy path can still differ
sharply from the original in how failures propagate.

---

