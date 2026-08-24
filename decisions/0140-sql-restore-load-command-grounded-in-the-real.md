---
id: "0140"
title: "SQL restore load command: grounded in the real invenio-sql-backup.bash/invenio-sql-restore.bash, not guessed"
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
uuid: "772282e3-55b4-4c32-8639-3923b3f98729"
origin_host: "MACMINI-RD.local"
---

**Context.** PLAN.md Phase 20.50 (Restore SQL Backup from S3) was blocked
on the exact load command, since it depends on the format the user's own
existing dump scripts actually produce -- not visible from clasm's own
codebase. The user pointed at `invenio-sql-backup.bash`/
`invenio-sql-restore.bash` in `~/WorkLab/caltechauthors` as the real,
currently-cron-run scripts (`dump-opensearch-index.bash` in the same
directory is explicitly out of scope -- incorrect, superseded by this
domain's own OpenSearch snapshot design).

**Decision.** Match the real scripts exactly, not a generic
`pg_dump`/`pg_restore` custom-format pipeline:
- **Backup format** (already produced by the existing cron job, clasm
  only needs to consume it): `pg_dump --username=<DB_USERNAME>
  --column-inserts <DB_NAME>` -- plain SQL text (`INSERT`-statement
  form, not `COPY`), gzip'd after the fact
  (`<container>-<db>-<date>.sql.gz`). Not `--format=custom`, so the load
  side is `psql`, never `pg_restore`.
- **Restore sequence**: `psql --dbname postgres -c "DROP DATABASE IF
  EXISTS <DB_NAME>"` -> `psql --dbname postgres -c "CREATE DATABASE
  <DB_NAME>"` -> pipe the decompressed `.sql` file into `psql
  --username=<DB_USERNAME> <DB_NAME>`. Drop-and-recreate, not
  restore-in-place -- matches the existing script's own behavior
  unchanged, including its harmless redundant `createdb` call right
  after `CREATE DATABASE` (not "fixed," since it isn't clasm's script to
  edit and the existing behavior is what operators already expect).
- **`DB_NAME`/`DB_USERNAME`**: hardcoded per-instance at the top of the
  real backup script, equal to the RDM project shortname
  (`caltechauthors`/`caltechauthors`) -- confirms the assumption already
  flagged in Phase 20.49/20.50 ("instance Name == RDM project
  shortname") holds for at least this instance. Not yet confirmed for
  every other RDM instance clasm might target; PLAN.md Phase 20.50
  defaults `DB_NAME`/`DB_USERNAME` to the target instance's own `Name`
  tag, editable, rather than hardcoding a single value.

**Consequences.** Phase 20.50's load/verify work items (5/8/9) are no
longer blocked -- the real command sequence above replaces the earlier
"command TBD" placeholders in PLAN.md and DESIGN.md. Since the source
`.sql.gz` is plain gzip (not OpenSearch's snapshot format), the download
step just needs a `gunzip` before piping into `psql` -- no new
decompression mechanism beyond what Phase 20.44's user-data gzip
handling already establishes as a pattern in this codebase (though this
is unrelated code, just the same general shape).

---

