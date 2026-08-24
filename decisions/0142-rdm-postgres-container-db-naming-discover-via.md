---
id: "0142"
title: "RDM Postgres container/DB naming: discover via docker ps every run, reconcile with rdm_postgres_config"
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
uuid: "378b072d-d7fe-49c5-9884-ba558f643c95"
origin_host: "MACMINI-RD.local"
---

**Context.** Both Run SQL Backup (new) and Restore SQL Backup (PLAN.md
Phase 20.50) need to know which Docker container is running Postgres,
and what DB name/user to connect as, before running `pg_dump`/`psql`
inside it. The real `invenio-sql-backup.bash` hardcodes these per
instance at the top of its own script copy -- not something clasm should
imitate wholesale, since the user has directly observed this naming
drift over time (Docker Compose v1's underscore-joined container names
replaced by v2's dash-joined ones), so a single hardcoded assumption
baked into Go source would eventually go stale exactly the way the old
approach did.

**Decision.** `container_name` is never assumed -- it's discovered live,
every single run of Run SQL Backup or Restore SQL Backup, via
`docker ps --filter ancestor=postgres --format '{{.Names}}'` over SSM
(filtering by the Postgres image itself, not a name pattern, so it
survives naming-convention changes like the underscore-to-dash shift).
Zero or more-than-one result is a hard error, not a guess. The result is
then reconciled against a new `rdm_postgres_config` YAML section in
`~/.clasm` (same pattern-matched-by-instance-Name shape as
`backup_directories`): if the discovered name differs from what's saved
(or nothing was saved yet), clasm updates and persists it immediately,
telling the operator what changed. `db_name`/`db_user` are **not**
discovered the same way -- confirmed reliable as an extrapolation from
the instance's own `Name` tag for a stock Invenio RDM deployment, so they
default that way, but are independently overridable via
`rdm_postgres_config` (editable through the Configure clasm domain's new
"Edit RDM Postgres config" action) for any instance customized beyond
what shipped from the Invenio RDM release -- this is the *only* way to
correct those two fields, since nothing discovers them automatically.

**Consequences.** This makes `rdm_postgres_config`'s persisted
`container_name` not a performance cache (discovery is never skipped),
but a shared, visible record: both workflows read/write the same entry,
`Show current config` can display what clasm currently believes about
each instance's Postgres setup, and a rename shows up as a reported,
recorded change rather than a silent one. A known limitation, not solved
here: a deployment using a custom-tagged (non-`postgres`-ancestor)
Postgres image won't be found by this filter -- flagged in DESIGN.md's
"Not decided yet," not designed around.

---

