---
id: "0171"
title: "UploadBackupFiles: a per-file upload failure warns and continues; only a cancelled context aborts the run"
date: "2026-08-27"
status: accepted
kind: decision
trigger: design
project: clasm
phase: "20.62"
supersedes: []
superseded_by: []
relates_to: ["0170", "0050"]
initiative: ""
session: ""
decisions: []
tags: []
uuid: "01a04410-3497-72a3-9afc-24ad7fff031a"
origin_host: "MACMINI-RD.local"
---

**Context.** Surfaced while designing DR-0170, not by anything that has
gone wrong in the field: tracing what changes once the upload set stops
being the delete set. `UploadBackupFiles` (`backup_upload.go`) treats
both a transport error and a non-`Success` command status for any single
file as a hard error that aborts the whole run:

```go
stdout, status, err := RunShellCommand(...)
if err != nil { return nil, err }
if status != ssmtypes.CommandInvocationStatusSuccess {
    return nil, fmt.Errorf("upload command on %s failed (status: %s)", instanceID, status)
}
```

That was right when every file in flight was a delete candidate: failing
loudly and touching nothing is the correct response when the alternative
is a partially-verified delete set. It is wrong once the upload set is
the whole directory. An SSM hiccup copying one *recent* file -- a file
nobody intended to delete -- would abort the archival and the trim of
every aged file behind it. The user's intake answer for DR-0170 was that
a failed copy of a file that will not be deleted should be a non-fatal
warning; this is the same requirement one layer down, where the abort
actually lives.

A `FAIL` line *inside* a command's own output is already handled
correctly and is not at issue: it comes back as `UploadResult{OK:
false}` and is reported, not fatal. Only the two hard-error paths above
are in scope.

**Decision.** On either hard-error path, record `UploadResult{OK:
false}` for that file, report it through `onProgress`, and continue to
the next file -- **except** when `ctx.Err() != nil`, in which case
return the error and stop. `UploadBackupFiles`' signature is unchanged;
only its error contract is.

**Rationale.** Continuing is safe for a structural reason rather than an
optimistic one: the delete phase is gated on `s3:HeadObject` (DR-0170,
decisions 2 and 3), so a file that failed to upload cannot be deleted no
matter how the loop treats the failure. The blast radius of continuing
is a warning line; the blast radius of aborting is that a transient
error on one unimportant file prevents every other backup from reaching
S3.

The `ctx.Err()` test is what keeps "the operator hit Ctrl+C" separable
from "this one file timed out." `RunShellCommand` wraps the caller's
context in its own per-file `context.WithTimeout` and returns a plain
formatted error on expiry (`"timed out waiting for command %q to finish
on %s"`) rather than wrapping `context.DeadlineExceeded`, so inspecting
the *outer* context distinguishes the two cleanly without inspecting
error strings.

`UploadBackupFiles` has exactly one call site, so the contract change is
contained to the workflow DR-0170 is already rewriting.

**Rejected alternatives.**

- **Leave the abort in place and let DR-0170 accept it.** Rejected: it
  satisfies the letter of the intake answer (the failed file is not
  deleted) while defeating its purpose (nothing else gets archived
  either).
- **Abort only when the failing file is in the delete set.** Rejected:
  it makes the upload loop depend on the retention policy, coupling two
  things this change exists to separate, and it still aborts the run for
  a file the operator could simply be told about.
- **Match on the error string or on `errors.Is(err,
  context.DeadlineExceeded)` to detect a per-file timeout.** Rejected:
  string matching is brittle, and `RunShellCommand` does not wrap the
  sentinel today. Testing the outer context asks the question that
  actually matters -- "was this run cancelled?" -- rather than inferring
  it.
- **Retry a failed file before moving on.** Rejected as scope: worth
  considering on its own evidence, not folded into this change. A
  failure is reported and the next run's pre-pass will retry it anyway,
  since an absent object falls toward copying.

**Consequences.** A run can now finish "successfully" with files that
never reached S3, so the summary must make those unmistakable -- and the
case that matters most is a file that failed to copy *and* was old
enough to trim, which is correctly left on disk but must not be silently
left on disk. Hard-error behaviour on cancellation is preserved
unchanged.

Test-first, PLAN.md Phase 20.62, alongside DR-0170. The cases: a
mid-list transport error leaves the remaining files uploaded and the
failed one marked `OK: false`; the same for a non-`Success` status; a
cancelled context returns the error and stops.

---
