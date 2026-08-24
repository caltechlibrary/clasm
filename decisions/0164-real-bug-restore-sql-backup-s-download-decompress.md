---
id: "0164"
title: "Real bug: Restore SQL Backup's download/decompress assumed every backup's S3 key ends in \".gz\", and swallowed stderr on failure"
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
uuid: "f92706a1-b0ad-487f-97a3-27c4c0f6df57"
origin_host: "MACMINI-RD.local"
---

**Context.** Live-testing Restore SQL Backup against
`caltechdata-restore-test`, restoring CaltechDATA production's own real
archived backup, hit `downloading/decompressing ... failed (status:
Failed)` with no further explanation. The debug log's
`StandardErrorContent` (never surfaced in clasm's own error message)
showed the real cause: `gzip: /tmp/caltechdata-db-1-caltechdata-2026-08-16.sql:
unknown suffix -- ignored`. Two independent real gaps:
1. The archived object's actual key is `...2026-08-16.sql` -- no `.gz`
   -- even though its content is gzip'd. `gunzip -f <path>` derives its
   *output* filename from the *input* path's own suffix and refuses on
   anything not ending in a recognized compressed extension, regardless
   of whether the bytes are actually gzip data.
2. `RunShellCommand` (`ssm.go`) only ever returns
   `StandardOutputContent` -- `StandardErrorContent` is captured by SSM
   but discarded entirely. `gunzip`'s (and generally, any shell
   command's) own error text lands on stderr by default, so it was
   simply never seen.

**Decision.**
1. `buildDownloadAndDecompressCommand` downloads to and decompresses
   into two **fixed** `/tmp` scratch paths
   (`remoteRestoreDownloadPath`/`remoteRestoreSQLPath`), no longer
   derived from the S3 key's own basename/suffix, and decompresses via
   `gunzip -c <download-path> > <sql-path>` -- `-c` streams to stdout
   regardless of the input's own name, sidestepping the suffix-based
   restriction entirely. A fixed name is safe here since only one
   restore ever runs against a given instance at a time.
2. That command (and `buildRestoreSQLCommands`' drop/create/load, which
   -- like the download step -- never parse their own stdout for
   meaningful content on success, only check the SSM invocation
   status) are now wrapped in a `{ ...; } 2>&1` group, merging stderr
   into the one stream `RunShellCommand` actually captures.
   `detectExistingSQLData`/`countRestoredTables` are deliberately left
   un-redirected -- both parse stdout content on success (a tuple
   count/row count), and merging in a stray psql NOTICE could corrupt
   that parsing; fixing their own error-visibility gap needs a
   different mechanism (see Consequences).

**Rationale.**
- Fixed-path decompression is strictly more robust than name-derived
  decompression -- it works identically whether the source key ends in
  `.sql.gz`, `.sql`, or anything else, confirmed by a regression test
  asserting the built command is identical (source key aside) for both
  cases.
- `{ ...; } 2>&1` is the minimal, safe fix for the two "status-only"
  commands in this file; widening `RunShellCommand`'s own signature to
  return stderr generally would ripple through every existing call site
  across the whole `internal/workflow` package -- out of scope for this
  live-testing pass, tracked separately (see Consequences).

**Consequences.** `RunShellCommand`'s stdout-only capture is a
project-wide gap, not unique to this file -- any command whose
meaningful error text lands on stderr (most shell tools' default
behavior) can hit the identical "(status: Failed)" opacity. Added to
TODO.md as a Nice to have, scoped as its own future design pass (widen
`RunShellCommand` to return stderr separately, let each caller decide
whether/how to fold it into an error message) rather than fixed
project-wide here. See PLAN.md Phase 20.59.

**Same-day follow-up, same phase: the fixed scratch paths above were
initially placed under `/tmp`, which turned out to be a second, distinct
real gap.** With the stderr fix in place, the very next retry surfaced a
genuine, different failure: `[Errno 28] No space left on device`.
Confirmed live (`df`/`mount` on `caltechdata-restore-test`) that this
project's own RDM cloud-init images mount `/tmp` as a **fixed-size
tmpfs** (~3.9GB, RAM-backed via systemd's default `tmp.mount` sizing) --
entirely separate from the root disk, which had 44GB genuinely free at
the time. A multi-gigabyte SQL dump downloaded to `/tmp` can exhaust
that fixed cap regardless of how much real disk space exists.
`remoteRestoreDownloadPath`/`remoteRestoreSQLPath` moved to `/var/tmp`
instead -- confirmed live, via the identical `df`/`mount` check, to be
plain root-disk space on this same instance, not a separate mount at
all, matching FHS's own convention that `/var/tmp` (unlike `/tmp`) is
for larger, persistent-across-reboots scratch files. No test changes
needed beyond updating the one fixture that matched on the literal old
path.

**Third same-day follow-up, same phase, same live-testing pass: the
".gz" key always meant "gzip content" assumption above was itself
wrong.** With both prior fixes in place, the very next retry surfaced a
*third* distinct real failure -- `gzip: /var/tmp/clasm-sql-restore.download:
not in gzip format`. The real CaltechDATA legacy file (bare `.sql` key)
turns out to be genuinely, plainly uncompressed -- not gzip content
under a misleading name, which is what this phase's very first fix
(above) assumed when it made decompression unconditional. New
`needsDecompression(key string) bool` (suffix check: `.gz` decompresses,
anything else doesn't) restores the conditional -- safe because every
backup clasm itself has ever produced is unconditionally gzip'd and
named accordingly, so the key's own suffix is a reliable signal in both
directions once actually checked rather than assumed. Suggested
directly by the user mid-live-test, and correct: simpler than the
original always-decompress design, not just a workaround.
`downloadAndDecompressSQLBackup` now returns `remoteRestoreDownloadPath`
(the raw file) when no decompression happened, `remoteRestoreSQLPath`
otherwise.

**Fourth same-day follow-up, same phase, same live-testing pass: `DROP
DATABASE` itself refuses while any other session is connected, and
stopping the target's own app wasn't sufficient to guarantee zero
connections.** With all three prior fixes in place, the restore reached
the actual drop/create/load sequence for the first time and hit
`ERROR: database "rdm14-granian" is being accessed by other users`.
Stopping `rdm.target` (the target's own RDM app) did not clear it --
`pg_stat_activity` still showed 3 idle connections from
`172.18.0.1` (the Docker bridge) more than two minutes after the app
process was confirmed stopped. A killed process's already-open TCP
connections to Postgres can linger in `pg_stat_activity` until
Postgres's own TCP keepalive notices the dead peer -- governed by
keepalive timing, not app shutdown, and not something an operator
stopping the app can rely on happening promptly. The real
`invenio-sql-restore.bash` doesn't handle this either (it just assumes
zero other connections); that assumption held up worse in practice here
than the design originally expected. `buildRestoreSQLCommands`'
`dropCmd` now runs `SELECT pg_terminate_backend(pid) FROM
pg_stat_activity WHERE datname=<dbName> AND pid <> pg_backend_pid()`
before `DROP DATABASE IF EXISTS`, in the same command group -- excludes
its own connection (`pg_backend_pid()`), scoped only to the target
database, and safe regardless of whether the database is empty,
already correctly emptied, or (as here) still holding stale idle
connections. New regression test
`TestBuildRestoreSQLCommands_DropTerminatesOtherSessionsFirst`.

**Fifth same-day follow-up, same phase, same live-testing pass:
`DefaultSQLRestoreTimeout` (30 minutes, matching
`DefaultBackupUploadTimeout`'s own bound) was itself too short.** With
the connection-termination fix in place, the restore finally reached
the load step and ran long enough for the user to note, from direct
experience manually restoring CaltechAUTHORS-scale data, that a real
load like this typically takes **~45 minutes** -- `--column-inserts`
dumps load as individual `INSERT` statements, not a bulk `COPY`, so this
is expected, not a hang. The 30-minute bound risked killing a
genuinely-still-progressing restore before it could finish. Widened to
2 hours, matching `DefaultSnapshotCreateTimeout`'s own precedent
(`opensearch_snapshot.go`) for "this can legitimately be big and slow"
-- the (much faster) download step costs nothing by sharing the same
generous bound. No test changes needed (no test asserted the specific
timeout value). Note: this fix could not rescue the in-flight restore
attempt that surfaced it -- that process already had the old 30-minute
bound compiled in; only a subsequent rebuild benefits.

---

