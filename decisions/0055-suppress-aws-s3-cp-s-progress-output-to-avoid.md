---
id: "0055"
title: "Suppress aws s3 cp's progress output to avoid truncating the OK/FAIL signal"
date: "2026-07-02"
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
uuid: "8ff51864-8ddf-43c5-86a5-f7b22366ae0d"
origin_host: "MACMINI-RD.local"
---

**Context.** After fixing the target instance's IAM permissions, real-AWS
testing against `newauthors` still reported every file as `FAIL` in
`awsops`'s own upload progress line -- but the debug log showed the
underlying `aws s3 cp` had actually succeeded (real transfer progress,
no error). Inspecting the full `ssm:GetCommandInvocation`
`StandardOutputContent` showed it was truncated at exactly 24,000
characters -- SSM's documented cap on captured command output -- cut off
mid-way through `aws s3 cp`'s own `\r`-updated progress meter
("Completed 350.0 MiB/972.0 MiB ..."), with this script's own
`printf 'OK\t<key>\t<size>\n'` signal never reached because the noise
ahead of it already filled the entire captured buffer.

**Decision.** The remote `aws s3 cp` invocation in
`buildUploadCommand` now runs with `--only-show-errors`, which
suppresses its normal per-chunk progress output entirely while still
writing real error messages to stderr (confirmed still working via the
earlier `AccessDenied` diagnosis). This keeps stdout limited to just
this script's own short `OK`/`FAIL` line, regardless of file size.

**Rationale.**
- The bug scaled with file size, not a fixed defect -- small files
  never produced enough progress-meter text to hit the 24,000-character
  cap, which is why it wasn't caught until testing against real,
  multi-hundred-MB backup files.
- `--only-show-errors` (rather than redirecting `aws s3 cp`'s stdout to
  `/dev/null` inline, or post-processing to strip progress lines) is
  the AWS CLI's own supported way to ask for exactly this: quiet on
  success, verbose on failure -- no fragile string-stripping needed.

**Consequences.** No result-shape or parsing change -- `parseUploadResults`
and `UploadResult` are unchanged; the fix is entirely in what the remote
script asks `aws s3 cp` to emit. Retroactively, this also means any
earlier "FAIL" report for a large file could have been a false negative
if the underlying IAM/CLI issue had otherwise been fixed at the time --
not a concern in practice here, since every prior real-AWS run failed
for an unrelated, definitively-confirmed reason (missing AWS CLI, then
missing IAM permission) before ever reaching a file large enough to
trigger this truncation.

---

