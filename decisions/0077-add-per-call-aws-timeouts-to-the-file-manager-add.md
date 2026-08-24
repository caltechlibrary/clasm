---
id: "0077"
title: "Add per-call AWS timeouts to the file manager; add a direct unlink-to-single-pane action"
date: "2026-07-09"
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
uuid: "61a3f406-4caf-4203-99e3-d448ee57a0d8"
origin_host: "MACMINI-RD.local"
---

**Context.** The file manager "appeared hung" after uploading a batch
of files to a real bucket. Investigated live: attached Delve
(non-destructively -- inspected goroutine stacks, then fully detached,
leaving the process running and untouched) to the running process
rather than guessing. Found, at that moment, a genuinely in-flight
`s3:PutObject` HTTP request -- not a deadlock in this project's own
code. By a second snapshot ~10 seconds later that request had completed
normally and the whole program was sitting in bubbletea's ordinary idle
event loop. The operator confirmed pressing a key brought the screen
back -- it had finished the batch and was showing the progress
overlay's "(press any key to continue)" state (DESIGN.md 21.4's
never-auto-dismiss rule), not actually stuck.

**Decision 1 -- add per-call timeouts anyway.** Even though this
specific instance wasn't a true hang, the investigation surfaced a real
gap: every direct AWS call in `internal/filemanager`/`internal/s3diff`
used the caller's own long-lived context with no per-call deadline,
unlike `internal/workflow`'s established `withCallTimeout` (30s)
convention (`call_timeout.go`) used throughout the EC2/AMI/Key
Management domains. A *genuinely* stalled connection (not just a slow-
but-progressing one) would hang the calling goroutine forever, with no
recovery short of killing the whole program. Added
`s3diff.WithCallTimeout` (30s, for lightweight metadata/listing/delete
calls) and `s3diff.WithTransferTimeout` (5 min, for Upload/Download's
actual data-transfer calls, since transfer time scales with object size
and connection speed, not just request/response latency) -- duplicated
from `internal/workflow`'s pattern rather than imported, for the same
import-cycle reason `internal/s3diff` itself exists (see the earlier
"Extract internal/s3diff..." entry). Applied at every direct
`ListObjectsV2`/`HeadObject`/`GetObject`/`PutObject`/`DeleteObject` call
site in both packages. `downloadOne`'s timeout has to span the whole
download (GetObject call + the later `io.Copy` of its response body),
not just the initial call, since the response body read is governed by
the same request context. While here, replaced `actions.go`'s
`uploadOne` (a near-duplicate of `s3diff.UploadFile` that was missing
Content-Type inference) with a direct call to `s3diff.UploadFile` --
one less duplicate implementation, and the Upload action now gets
correct Content-Type headers Sync's Upload already had.

**Decision 2 -- direct unlink action.** Separately reported: "I need a
way to go from two panels back to displaying only the S3 bucket." The
`l` hotkey's existing unlink path (open `:link <path>` pre-filled,
clear it, submit empty) was reachable but not discoverable as *the* way
back. `l` while linked (or `:unlink`) now goes straight to a Confirm
("Unlink `<path>` and return to single-pane view?"); accepting applies
instantly (`applyLink("")`) since unlinking is a state change, not a
background operation -- it never touches `beginAction`/
`overlayProgress`. `:link` (with an explicit empty argument) still
unlinks too, unchanged, so nothing that depended on the old path breaks.

**Rejected alternatives.**
- *Use the same 30s timeout for Upload/Download as everything else* --
  rejected; a large file's legitimate transfer time can easily exceed
  30s on a slow connection, and the goal is recovering from a stalled
  connection, not penalizing a slow-but-working one.
- *Make DefaultCallTimeout/TransferCallTimeout const, matching
  workflow's own const* -- rejected; kept as `var` specifically so
  tests can shrink them and prove the recovery behavior without an
  actual 30-second (or 5-minute) wait.

**Consequences.** `internal/s3diff.go` gained `WithCallTimeout`/
`WithTransferTimeout` (+ tests proving a stalled fake connection
recovers via timeout rather than hanging).
`internal/filemanager/{listing,actions,sync}.go` apply them at every
AWS call site; `uploadOne` is gone, replaced by `s3diff.UploadFile`.
`internal/filemanager/{commandline,actions}.go` gained
`startUnlinkConfirm`/`actionUnlink` and the `:unlink` command; the
hotkey legend now reads "l Unlink" once linked. New tests:
`TestModel_Unlink_LHotkeyGoesStraightToConfirm`,
`TestModel_Unlink_DeclineStaysLinked`,
`TestModel_Unlink_ColonCommand`. All pre-existing tests pass unchanged;
`go test -race ./...` clean.

---

