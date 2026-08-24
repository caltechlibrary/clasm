---
id: "0132"
title: "Poll snapshot/restore status to completion and verify doc counts post-restore, rather than trusting a fixed timeout or a bare success response"
date: "2026-07-28"
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
uuid: "f19d37a6-5dae-4dc6-91e0-1610427aa631"
origin_host: "MACMINI-RD.local"
---

**Context.** An earlier (2024/early-2025) attempt at OpenSearch backup/
restore reportedly loaded corrupted data; the user's own memory of the
specific root cause is no longer precise enough to design directly
against.

**Decision.** Rather than reconstruct the exact old bug, design against
the general failure class. Every snapshot-creation and restore call
polls its own status endpoint until a definitive terminal state
(`SUCCESS`/`FAILED`/`PARTIAL`) rather than assuming completion from the
initiating call's response -- following this project's own prior fix for
the same shape of bug in AMI creation (a short fixed timeout that didn't
account for real-world creation times). Restore additionally runs a
post-restore sanity check comparing per-index document counts between
the snapshot's own metadata and the actually-restored indices, surfacing
any mismatch rather than declaring success once the restore call itself
returns.

**Consequences.** `PollSnapshotUntilComplete` and its restore-completion
equivalent (PLAN.md Phase 20.49/20.51) both need a two-tier timeout
shape -- a long overall deadline, a short per-check SSM round trip --
since each status check is itself a full SSM command, not a direct API
call. The doc-count check adds an extra `_cat/indices` round trip after
every restore, accepted as the cost of not repeating an
unverified-success mistake.

---

