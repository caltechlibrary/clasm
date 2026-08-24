---
id: "0144"
title: "Real bug: `docker ps --filter ancestor=postgres` doesn't match a tagged image -- filter client-side instead"
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
uuid: "e22adb96-b5c2-4391-877c-f82dedb4d1a3"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via live testing against CaltechAUTHORS production
(`i-0c4c81336aea33d27`) immediately after Phase 20.52 landed: Run SQL
Backup reported "no running Postgres container found," even though the
user had rebooted the instance that same morning and confirmed Postgres
was working. Root-caused via the `--debug` JSONL log, not guessed
(`clasm-debug-20260729-095959.jsonl`, matched by `CommandId`): the
`SSM.GetCommandInvocation` response for the `docker ps --filter
ancestor=postgres --format '{{.Names}}'` command showed `Status:
"Success"`, `ResponseCode: 0`, and both `StandardOutputContent`/
`StandardErrorContent` genuinely empty -- the command ran cleanly and
found zero matches. A direct, unfiltered `docker ps` on the same
instance, requested from the user, showed `caltechauthors-db-1` running
`postgres:14.13` the whole time, alongside six other containers
(`caltechauthors-mq-1`, `-s3-1`, `-opensearch-dashboards-1`, `-cache-1`,
`-search-1`, `-pgadmin-1`).

**Decision.** Docker's `--filter ancestor=<repo>` does not match a
container whose image carries a specific tag when `<repo>` is given
without one -- confirmed by this real, reproducible failure, not by
reading Docker's docs after the fact. Stopped trusting the filter
entirely: `dockerPSCommand` now lists every running container's image
and name (`docker ps --format '{{.Image}}\t{{.Names}}'`, no `--filter`
at all), and a new `isPostgresImage` matches the image column in Go --
`"postgres"`, `"postgres:<any tag>"`, `"postgres@<digest>"`, or any of
those prefixed with a registry/path (e.g.
`docker.io/library/postgres:14.13`), stripping only the path prefix
before comparing, so a repository name that merely *contains* "postgres"
as a substring (e.g. a hypothetical `my-postgres-exporter`) doesn't
false-match. Matches `ListBackupFiles`' own established precedent:
filter locally, don't trust a remote command's filter flag to do
semantic work clasm can verify itself.

**Consequences.** `internal/workflow/rdm_postgres_config.go`'s
`discoverPostgresContainer` changed (`dockerPSPostgresCommand` renamed
`dockerPSCommand`, filter clause dropped, new `isPostgresImage` helper);
Run SQL Backup and Restore SQL Backup (once implemented) both get the
fix for free, since both call this same shared helper. New test fixture
`realCaltechauthorsDockerPS` reproduces the exact real `docker ps`
output from this incident (all seven containers, tab-separated
image+name) as a standing regression test -- confirmed failing against
the pre-fix code (found "more than one" match, since the naive fake
client's substring-matched fixtures hadn't caught this because they
never modeled Docker's actual filter semantics, only the parsing logic
downstream of it). General lesson, consistent with this project's
repeated experience elsewhere (Phase 20.23's AMI-creation timeout,
Phase 20.33's IAM pagination): a unit test against a fake client can
only be as good as the assumptions baked into the fixture -- this one
could only be caught by exercising a real `docker ps` filter against a
real Docker daemon. Not yet re-verified against real AWS after the fix
(the user's live-testing session is what surfaced this; the fix itself
is fixture-tested only so far).

---

