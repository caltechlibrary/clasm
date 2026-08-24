---
id: "0167"
title: "Real bug: `RunShellCommand`'s SSM `SendCommand` never sets `TimeoutSeconds`, so AWS silently applies its own 1-hour default independent of the caller's timeout"
date: "2026-08-19"
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
uuid: "b4070fe6-c304-4bdb-ad3e-95a3b147d284"
origin_host: "MACMINI-RD.local"
---

**Context.** Checking on the overnight Restore SQL Backup left running
against `caltechdata-restore-test` (Phase 20.59), `ps aux` on the
instance showed the load process gone, and `pg_database_size` had grown
only marginally (1424MB -> 1454MB) since the prior session's last check
-- much smaller than the 900MB+ jumps seen earlier in the same load,
suggesting it stopped rather than finished. Rather than guess from that
alone, queried AWS directly for the ground truth: `aws ssm
get-command-invocation` against the load step's own `CommandId` (read
out of the session's `--debug` JSONL log) returned `Status: TimedOut`,
`StatusDetails: ExecutionTimedOut`, start/end exactly 1h0.5s apart. This
is separate from the client's own wait, which (old, not-yet-rebuilt
30-minute-timeout binary) had already given up polling *30 minutes
earlier still*, at 23:14Z, and reported a "timed out" client-side error
at that point -- meaning two independent timeouts were in play, neither
in sync with the other, and neither in sync with Phase 20.59's already-
widened `DefaultSQLRestoreTimeout` (2 hours). Reading `ssm.go` confirmed
why: `RunShellCommand`'s `SendCommandInput` never sets `TimeoutSeconds`
-- AWS's own SSM document execution silently defaults to 3600 seconds
regardless of the `timeout`/`pollInterval` values a caller passes in,
which only ever governed the *client's* `GetCommandInvocation` polling
loop, never the actual remote execution window. Phase 20.59's timeout
widening therefore had no real effect on how long AWS itself would let
the load run. The real, in-flight SQL load is confirmed truncated as a
result, leaving `rdm14-granian` on `caltechdata-restore-test` in a
partial, unusable state -- not a completed restore to spot-check, a
confirmed-incomplete one to redo.

**Decision.** Set `TimeoutSeconds` on every `RunShellCommand`'s
`SendCommandInput`, derived from the same `timeout` parameter already
threaded through by every caller (floored at AWS's own 30-second
minimum), so the AWS-side execution window and the client-side wait
window are the same duration instead of two independent, uncoordinated
ones.

**Rationale.** A helper that already accepts a `timeout` parameter
should make that parameter govern the actual remote execution, not just
its own local polling -- otherwise a caller that deliberately widens its
timeout (as Phase 20.59 did, 30min -> 2h) gets no real benefit once
AWS's own unset default is shorter than that. This is a distinct fix
from the still-open, separately-filed question in TODO.md ("Nice to
have") of whether a client-side timeout should also
`ssm:CancelCommand` the remote process -- that question is about what
happens once the *client* gives up early; this fix is about making sure
AWS's own execution window isn't silently shorter than the client is
willing to wait in the first place. Both remain worth doing; this one
directly explains and closes last night's incident.

**Consequences.** `rdm14-granian`'s restored database on
`caltechdata-restore-test` must be redone from scratch (drop/recreate/
reload) once this fix lands and clasm is rebuilt -- the old load
process is confirmed genuinely dead (`ps aux` empty), so a fresh
Restore SQL Backup attempt's `pg_terminate_backend` step is no longer a
collision risk. Logged as PLAN.md Phase 20.61, new TODO.md "Bugs and
issues" entry.

**Same-day follow-up, same phase: the fix above was real but targeted
the wrong parameter -- the actual re-run still died at exactly 1 hour.**
Rebuilt clasm with the `TimeoutSeconds` fix and re-ran Restore SQL
Backup against `caltechdata-restore-test` (this time picking the `.gz`
backup variant), live-monitored via the `--debug` log. The load step
still failed with `Status: TimedOut`/`StatusDetails: ExecutionTimedOut`
at exactly 1h0.5s (17:11:00Z -> 18:11:00Z) -- despite `aws ssm
list-commands` confirming `TimeoutSeconds: 7200` was genuinely set on
this exact command. Queried AWS's own document definition directly
(`aws ssm describe-document --name AWS-RunShellScript`) rather than
re-guessing, and found the real cause: `AWS-RunShellScript` has its own
**`executionTimeout`** document parameter ("The time in seconds for a
command to complete before it is considered to have failed. Default is
3600 (1 hour)."), passed inside `Parameters` alongside `commands` --
entirely separate from the top-level `SendCommandInput.TimeoutSeconds`
field, which (per AWS's actual behavior) bounds how long AWS will wait
for the command to *start* running, not how long it may keep running
once started. `TimeoutSeconds` was therefore a real, worthwhile fix
(the delivery/start window genuinely wasn't being bounded by the
caller's own timeout either) but not the parameter actually responsible
for this specific symptom -- `executionTimeout`, never set at all,
silently applied its own 3600-second default regardless of
`TimeoutSeconds`.

**Revised decision.** Also set `Parameters["executionTimeout"]` on
`RunShellCommand`'s `SendCommandInput` to the same computed
`timeoutSeconds` value (as a string, `AWS-RunShellScript`'s own
parameter type), alongside the existing `commands` parameter. Keep
`TimeoutSeconds` as set by the original fix -- both are real, both
matter, they just govern different phases of a command's lifecycle
(queued-and-not-yet-started vs. running-too-long).

**Consequences (revised).** `rdm14-granian` is confirmed truncated a
second time and needs a third restore attempt once this corrected fix
lands and clasm is rebuilt again. General lesson: confirming a fix
against AWS's own authoritative source (the `SendCommand`/
`GetCommandInvocation` API response the first time; the document's own
`describe-document` parameter list the second time) rather than
re-guessing from symptoms alone is what caught both the original bug
and this refinement -- the symptom ("dies at ~1 hour") looked identical
both times, but the two root causes were genuinely different
parameters.

**Resolved, 2026-08-19: the third attempt succeeded.** Rebuilt with both
fixes in place and re-ran Restore SQL Backup (the `.gz` backup variant)
against `caltechdata-restore-test`. `aws ssm list-commands` confirmed
the load command carried both `TimeoutSeconds: 7200` and
`executionTimeout: "7200"` this time; it ran for ~68 minutes -- past the
exact 1-hour mark that killed both earlier attempts -- before AWS itself
reported `Status: Success`, not a timeout. `countRestoredTables`
reported 80 tables; independently cross-checked,
`pg_database_size('rdm14-granian')` = 1710 MB, larger than either
truncated attempt's final size (1424MB, 1454MB). Phase 20.50 (Restore
SQL Backup from S3) is now fully real-AWS-verified end to end.

---

