---
id: "0124"
title: "User-data pre-flight size check: hard error, no remediation loop-back; switch to gzip.BestCompression"
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
uuid: "700cfbc2-506a-43af-8ea2-af1ad9a8bf5d"
origin_host: "MACMINI-RD.local"
---

**Context.** `InvalidUserData.Malformed: User data is limited to 16384
bytes` recurred syncing `granian-rdm-v14/cloud-init.yaml` (57849 raw
bytes) to a launch template -- Phase 20.34's gzip fix is confirmed
necessary but not sufficient here: the file gzips (default compression,
level 6) to 16671 bytes, 287 over the limit; even `gzip.BestCompression`
only reaches 16576, still 192 over. `encodeUserData` (`userdata_gzip.go`)
has never validated size against AWS's limit at any of its three write
sites -- the 16384-byte figure exists only as a source comment. See
DESIGN.md, "User-Data Pre-Flight Size Check", PLAN.md Phase 20.44.

**Decision.** Two changes: (1) switch `encodeUserData`'s
`gzip.NewWriter` to `gzip.NewWriterLevel(..., gzip.BestCompression)`
-- same "always take the free win, no conditional" reasoning Phase
20.34 already applied to gzipping unconditionally; (2) widen
`encodeUserData`'s signature to `(string, error)` (matching
`decodeUserData`'s existing shape) and add a hard pre-flight check
against a new `maxUserDataBytes = 16384` constant, comparing the
gzip-compressed byte count (before base64) -- the same quantity AWS's
own error is measuring, confirmed via the incident's debug-log numbers
above. On failure: **hard error, abort** -- the operator sees the
compressed size and the overage, then trims the cloud-init file
themselves and retries the whole workflow. All three write-site callers
(`launch_execute.go`, `launch_template_create.go`,
`launch_template_sync.go`) now propagate this error instead of calling
AWS with a payload already known to be doomed.

**Rejected alternatives.**
- *Offer to pick a different file inline* (loop back to
  `promptCloudInitYAMLFile` on failure instead of aborting) -- rejected
  by the user's own explicit call: the failure surfaces deep in the
  call chain (inside `encodeUserData`, several frames past where the
  file was originally selected), so a clean loop-back would need new
  plumbing to re-enter file selection from inside what is currently a
  simple encode step; a hard abort is simpler and consistent with this
  codebase's "fail loud, don't guess" precedent (`growRootFilesystem`'s
  SSM/layout-detection fallback).
- *A multipart/`#include`/S3-reference mechanism* to split payloads too
  large even at max compression -- a real, standard cloud-init/EC2
  pattern, but a materially larger feature (new upload step, new
  IAM/S3 permissions surface, new decode/diff/show read-paths) with no
  second use case yet; this incident was resolved by trimming the
  cloud-init content itself, outside clasm. Left as a candidate in
  TODO.md's Someday/maybe if oversized files recur after this
  pre-flight check ships.
- *Leave the check as advisory (warn, let the operator proceed
  anyway)* -- rejected because AWS would reject the call unconditionally
  regardless; there's no scenario where proceeding past this check
  succeeds, so a warn-and-continue would just delay the same failure by
  one step while adding a confusing "are you sure" prompt for something
  that can never be sure.

**Consequences.** `encodeUserData` callers that previously ignored a
return value now must handle an error -- a small, mechanical change at
exactly three call sites, no behavior change for any payload that was
already under the limit. No new dependency, no new AWS permissions, no
change to `decodeUserData` or any read site.

---

