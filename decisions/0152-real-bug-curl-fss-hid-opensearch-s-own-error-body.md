---
id: "0152"
title: "Real bug: `curl -fsS` hid OpenSearch's own error body; switch to `--fail-with-body`"
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
uuid: "2980ece6-879c-4b04-ac2d-fb69ec220c84"
origin_host: "MACMINI-RD.local"
---

**Context.** First live test of Archive OpenSearch Snapshot to S3
against CaltechAUTHORS production (`i-0c4c81336aea33d27`, `--debug` on)
hit `registering snapshot repo "rdm_backup_repo" ... failed (status:
Failed)` -- no further detail. The `--debug` JSONL log showed the SSM
invocation's `StandardErrorContent` was only curl's own generic `curl:
(22) The requested URL returned error: 500 / failed to run commands:
exit status 22`. OpenSearch's actual error -- almost certainly the
`path.repo` prerequisite (`rdm-opensearch-path-repo-retrofit.md`) not
yet being configured on this instance -- was never captured anywhere,
because `-f` (`--fail`) makes curl both exit non-zero *and* discard the
response body on a non-2xx status. Confirmed the account's real
production instance, not a fixture -- a unit test with a fake SSM client
never would have caught this, since the fake always returns whatever
canned stdout a test supplies.

**Decision.** **Switch every OpenSearch REST-over-SSM `curl` command from
`-fsS` to `--fail-with-body -sS`**, and thread the captured response
body into every resulting error message (new `curlFailureError` helper,
`internal/workflow/opensearch_snapshot.go`).

**Rationale.**
- `--fail-with-body` (curl >= 7.76, present on every Ubuntu LTS this
  project targets -- 22.04/24.04/26.04) keeps `-f`'s exit-code-based
  failure detection (needed for `RunShellCommand`'s own SSM invocation
  status to reflect a real OpenSearch-side error) while still writing
  the HTTP response body to stdout on a non-2xx status.
- OpenSearch's own JSON error bodies are specific and actionable (e.g.
  "location doesn't match any of the locations specified by
  path.repo") -- surfacing them directly in clasm's own error message
  means an operator no longer has to dig through a `--debug` JSONL log
  to diagnose a registration/snapshot/delete/poll failure.

**Rejected alternatives.**
- *Parse `StandardErrorContent` too* -- would still only ever show
  curl's own generic message, never the server's actual explanation;
  doesn't fix the underlying problem of `-f` discarding the body.
- *Leave `-fsS`, tell operators to always run with `--debug`* --
  workable but user-hostile; every prior "Preflight check" decision in
  this log has favored a clear, immediate, in-app error over pushing
  diagnosis onto a separate log file.

**Consequences.**
- `buildRegisterRepoCommand`/`buildCreateSnapshotCommand`/
  `buildSnapshotStateCommand`/`buildDeleteSnapshotCommand` all changed;
  their own unit tests' exact-string/substring assertions updated to
  match.
- `RegisterSnapshotRepo`/`CreateSnapshot`/`DeleteSnapshot`/
  `PollSnapshotUntilComplete`'s SSM-failure branch now include the
  captured stdout body in their returned error (new regression tests
  per function, confirmed failing against the pre-fix code first).

---

