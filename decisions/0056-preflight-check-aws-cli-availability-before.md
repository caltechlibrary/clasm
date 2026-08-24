---
id: "0056"
title: "Preflight check: AWS CLI availability before Backup Archive & Trim"
date: "2026-07-02"
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
uuid: "2ca6a462-b7ee-4e0d-8729-2f0e2ea8285f"
origin_host: "MACMINI-RD.local"
---

**Context.** A missing AWS CLI on the target instance was, by a wide
margin, the most common real-AWS failure this project hit while
verifying Backup Archive & Trim -- it happened on `newauthors`, then
again on `data-new`. Both times the symptom was identical and
indirect: every file in the upload phase reported `FAIL`, with the real
cause (`aws: not found`) buried in the -debug JSONL log's stderr
capture, several prompts and a full dry-run list deep into the
workflow. The user's own framing: "That should be an explicit error we
check for much like with shell scripts we check if a command exists
before trying to execute it."

**Decision.** `BackupArchiveAndTrim` now calls a new
`CheckAWSCLIAvailable`, right after picking the instance and before any
other prompt, which runs `command -v aws` via SSM and aborts
immediately with `"AWS CLI not found on instance <id> -- install it
before running Backup Archive & Trim"` if it's missing.

**Rationale.**
- `command -v aws` is the same POSIX-portable existence check idiom
  this project already reaches for elsewhere (shellQuote's own doc
  comment references similar shell-safety discipline) -- no new pattern
  introduced, just applied one layer earlier.
- Checking immediately after instance selection, before the directory/
  age/bucket prompts and the dry-run list, means a missing CLI is
  caught in the time of one quick SSM round-trip instead of after
  several prompts and a potentially large `find` listing have already
  run for nothing.
- A dedicated check (not just "let the first `aws s3 cp` fail
  naturally") turns a misleading generic `FAIL` on every file --
  requiring a debug-log trip to explain -- into one specific,
  actionable, immediate error.

**Consequences.** One extra `ssm:SendCommand` round-trip per Backup
Archive & Trim run (negligible cost against the dry-run list and
uploads that follow). `internal/workflow/backup_cli_check.go` is new;
five existing tests' fake SSM clients needed a `"command -v aws"`
response entry added, and four `sendCommandCalls()` count assertions
shifted by one to account for the new leading check.

---

