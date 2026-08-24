---
id: "0166"
title: "Restore load steps should report real progress, not just wait on a timeout; no parallel restores during validation"
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
uuid: "d1f95e04-36e1-4839-a8dc-2bf841ffc0b6"
origin_host: "MACMINI-RD.local"
---

**Context.** Live-testing Phase 20.50's Restore SQL Backup against
`caltechdata-restore-test` exposed that a fixed client-side timeout is
the wrong shape of fix for a long-running load step -- Phase 20.59's
fifth follow-up had already widened `DefaultSQLRestoreTimeout` from 30
minutes to 2 hours after a real ~45-minute restore, but a timeout
expiring doesn't mean the remote `psql` load actually stopped (see the
still-open `RunShellCommand`-timeout-doesn't-cancel TODO.md finding);
the operator had to hand-poll `ps aux`/`pg_database_size`/table counts
via commands read out in chat for over an hour to tell "still working"
from "actually stuck." Separately, the user asked whether SQL restore
and OpenSearch restore could run in parallel on the same instance to
save wall-clock, and provided real `free -h` data mid-restore
(m5.large, 2 vCPU/8GB: 3.4GB "available", 248MB raw "free", 0B swap)
to ground the discussion instead of speculating.

**Decision.** (1) Design (not yet implement) extending Phase 20.53's
`pollWithProgress` pattern to the SQL restore load step: report elapsed
time plus `pg_database_size` growth (and its delta since the last tick)
on an interval, instead of blocking silently until success/timeout;
set expectations up front with a one-time "this can take 45 minutes to
over an hour" message before the load starts; keep the timeout as a
backstop, not the primary signal; apply the same lesson to Phase 20.51
from the start (OpenSearch's own restore-status API gives it a better
progress signal than SQL restore's size-growth proxy). (2) Do not run
SQL restore and OpenSearch restore in parallel on one instance, for
now -- not because the real memory data ruled it out (3.4GB available
even mid-restore suggests it likely would have fit, since OpenSearch's
JVM heap is normally a fixed allocation rather than one that grows with
restore size), but because (a) this instance has zero swap configured,
so any real spike is a hard OOM kill with no safety margin, and (b)
Phase 20.50 and the not-yet-built Phase 20.51 are each being live-
tested specifically to validate their own correctness -- running both
at once on an unvalidated new code path would make a failure ambiguous
between "real Phase 20.51 bug" and "resource contention from Phase
20.50 running alongside it."

**Rationale.** A progress-reporting redesign directly targets the root
problem this session actually hit (client-side timeout expiring while
real remote progress was invisible), where further timeout tuning would
only ever be treating a symptom -- there is no fixed number that is
"long enough" for every backup size. Grounding the parallelism question
in real `free -h` data rather than guessing avoided both an over-cautious
blanket "never" and an under-cautious "sure, try it" -- the honest
answer is memory headroom probably isn't the blocker, validation
isolation is, and that's a temporary constraint (specific to *this*
being the first real test of Phase 20.51), not a permanent architectural
one.

**Consequences.** Logged as PLAN.md Phase 20.60 / DESIGN.md "Restore
Progress Reporting: Extend `pollWithProgress` to the SQL/OpenSearch
Restore Load Steps; No Parallel Restores" -- designed only, explicitly
deferred past today's session (user's call: no time left today for
either this or Phase 20.51 testing). Whether parallel restores are
viable in practice remains an open, answerable-later question once
Phase 20.51 has its own real peak-resource-usage data point to compare
against a concurrent SQL restore's footprint.

---

