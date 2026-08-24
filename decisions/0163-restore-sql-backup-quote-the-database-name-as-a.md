---
id: "0163"
title: "Restore SQL Backup: quote the database name as a SQL identifier, not just shell-quote it"
date: "2026-08-18"
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
uuid: "698d05bf-74aa-4e05-989d-92043fcc5efb"
origin_host: "MACMINI-RD.local"
---

**Context.** Implementing Phase 20.50 against a real target
(`caltechdata-restore-test`, whose actual Postgres database is named
`rdm14-granian`, hyphenated -- `docker-services.yml`'s own
`POSTGRES_DB`/`POSTGRES_USER` use the instance's real, hyphenated
`INSTANCE_NAME` directly, not the underscored `PACKAGE_NAME` used only
for the Python module) surfaced that DECISIONS.md's already-committed
"SQL restore load command" entry (2026-07-29, below) describes the
DROP/CREATE DATABASE statements as unquoted, matching
`invenio-sql-restore.bash`'s own real, unquoted statements verbatim.
That's syntactically invalid for any `dbName` containing a hyphen (or
any other character not valid in a bare identifier) --
`DROP DATABASE IF EXISTS rdm14-granian` parses as `rdm14 - granian`
(subtraction), not a single identifier. Every production instance's real
`dbName` so far has been a plain lowercase word, so this gap never
surfaced there.

**Decision.** `buildRestoreSQLCommands` wraps `dbName` in a proper SQL
quoted identifier (`quoteSQLIdentifier`: double-quote wrapped, embedded
double quotes doubled) inside the DROP/CREATE statements specifically --
not the load command's own `psql --username=<user> <dbName>` argument,
which is a plain connection-target string, not SQL syntax, and stays
shell-quoted only (`shellQuote`, unchanged).

**Rationale.**
- A quoted identifier is a strict superset: for every existing
  production `dbName` (all plain lowercase words), quoting changes
  nothing observable -- Postgres treats `"caltechauthors"` and
  `caltechauthors` identically. This isn't a divergence from the real
  script's *intent* (drop-and-recreate a database by name), only a
  necessary correction for names the original scripts never had to
  handle.
- Confirmed via `TestBuildRestoreSQLCommands_HyphenatedDBNameIsQuotedAsIdentifier`,
  grounded directly in this real instance's own `dbName`, not a
  synthetic example.

**Consequences.** None to the already-decided load command's overall
*structure* (drop -> create -> load, still three separate commands) --
only the DROP/CREATE statements' own SQL text changed. See PLAN.md Phase
20.50.

---

