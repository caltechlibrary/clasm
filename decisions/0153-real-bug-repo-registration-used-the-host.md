---
id: "0153"
title: "Real bug: repo registration used the host directory as `location`, not the container-internal `path.repo` path"
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
uuid: "45136827-c8a8-4d5a-b57c-3fa2a15d4791"
origin_host: "MACMINI-RD.local"
---

**Context.** After the `--fail-with-body` fix (below) surfaced
OpenSearch's actual error for the first time, the user immediately began
retrofitting `path.repo` on CaltechAUTHORS production
(`i-0c4c81336aea33d27`) per `rdm-opensearch-path-repo-retrofit.md`. That
runbook bind-mounts the operator's chosen *host* directory (e.g.
`/opt/rdm_opensearch_backups`) to a fixed *container-internal* path,
`/usr/share/opensearch/backups`, and sets `path.repo` to the
container-internal path -- because OpenSearch runs inside the `search`
container and has no visibility into host paths at all. `archiveOpenSearchSnapshot`
(`internal/workflow/opensearch_archive.go`) passed the same `directory`
value (the operator-typed host path) to *both*
`SyncOpenSearchBackupsToS3` (correct -- `aws s3 sync` runs directly on
the host) and `RegisterSnapshotRepo` (wrong -- OpenSearch needs the
container path). Even after a correct retrofit, registration would have
kept failing with the identical "location doesn't match any of the
locations specified by path.repo" error, just for a different reason
than before the retrofit (empty `path.repo` vs. a `path.repo` that's set
but doesn't include the host path, since it was never meant to).

**Decision.** **`RegisterSnapshotRepo` is now called with a new fixed
constant, `DefaultOpenSearchContainerRepoPath =
"/usr/share/opensearch/backups"`, never the operator-typed `directory`.**
The `directory` prompt continues to mean "host directory," used only for
`aws s3 sync` (and, previously, the create-directory-yourself step the
operator did manually).

**Rationale.**
- The host path and the container-internal path are two genuinely
  different concepts (one filesystem visible to the host's `aws` CLI,
  the other visible only inside the `search` container) that happened
  to collapse into a single `directory` parameter in this phase's
  original design -- a real gap, not caught until an actual retrofit
  was attempted against a real, already-partially-fixed instance.
- The container-internal path is fixed by the retrofit runbook itself
  (`/usr/share/opensearch/backups`, identical convention for every
  production instance, matching `granian-rdm-v14`'s own cloud-init for
  new instances) -- hardcoding it as a constant matches
  `DefaultOpenSearchRepoName`'s own precedent ("one fixed name/path, not
  per-instance-configurable").

**Rejected alternatives.**
- *A second, distinct config-driven prompt for "container repo path"* --
  unnecessary given the runbook fixes this value identically everywhere;
  would just be one more thing an operator has to type correctly every
  run for no real benefit.

**Consequences.**
- New `DefaultOpenSearchContainerRepoPath` constant
  (`opensearch_archive.go`), doc comment on `RegisterSnapshotRepo`'s call
  site distinguishing host vs. container paths explicitly so this
  doesn't quietly regress again.
- New regression test,
  `TestArchiveOpenSearchSnapshot_RegistersRepoWithContainerPathNotHostDirectory`
  -- types a host directory distinct from the container path and asserts
  the register-repo command uses the fixed container path while the sync
  command still uses the typed host directory. Confirmed failing against
  the pre-fix code first.

---

