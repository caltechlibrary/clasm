---
id: "0096"
title: "Bucket picker for Backup Archive & Trim"
date: "2026-07-13"
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
uuid: "db2a6384-d559-4046-bf44-b339a9ce6669"
origin_host: "MACMINI-RD.local"
---

**Context.** Backup Archive & Trim's S3 bucket prompt was pure free
text -- no memory aid for an operator who has to recall the exact
bucket name from scratch every run, unlike every other bucket-selection
call site in the app (`pickBucket`, Phase 20.4), which already offers a
filterable pick list. Requested directly, with an explicit requirement
that typing a bucket name (not just picking from a list) must remain
possible.

**Decision.** New `promptBackupBucket`: fetches this account's S3
buckets (`inventory.ListBuckets`, the same call `refreshS3` already
uses) and offers them as a filterable pick list (`'/'` to filter,
matching every other filterable screen in this app), plus an "Other
(type a bucket name)" entry that falls through to the original
free-text prompt. Falls back to the free-text prompt directly (no
picker at all) if the listing fails or comes back empty -- there's
nothing more reliable, or for an empty account nothing useful, to offer
instead (mirrors `promptKeyPairNameOrCreate`'s own precedent for the
identical reason). Deliberately built as a Menu-tier `huh.Select`
(via the existing `pickComparable` helper) rather than a Picker-tier
`tui.RunPicker` (unlike every other bucket-selection call site) --
`promptBackupBucket` must stay embedded inside `backupArchiveAndTrim`'s
own pipe-testable prompt sequence (directory, then bucket, then age
threshold, per "Reorder Backup Archive & Trim's prompts" below), and a
real bubbletea Program can't be driven by a test's pipe input the way
`pickBucket`'s callers already accept -- huh's own built-in `'/'`
filtering (confirmed: same default keybinding as this project's
Picker/List-tier filter convention) covers the "let me narrow this down
by typing" need without needing the untestable Picker-tier component at
all.

**Rationale.** huh.Select's accessible-mode pipe-testing path expects a
row *number*, not free text, so existing tests (which never populate
`fakeS3Client.buckets`) naturally exercise the empty-list fallback
branch unchanged -- zero existing tests needed rewriting. New tests
cover the populated-list path (picking a known bucket by number),
the "Other" escape hatch, and the listing-error fallback, each
asserting on the resulting bucket name via the upload command's
`s3://<bucket>/...` destination (the same verification technique
`TestBackupArchiveAndTrim_UntaggedInstanceUsesIDAsKeyPrefix` already
established).

**Consequences.** New `bucketChoice` type and `promptBackupBucket`
function in `internal/workflow/backup_archive.go`; no signature changes
to `BackupArchiveAndTrim`/`backupArchiveAndTrim` (bucket resolution
still happens in the same place in the same testable core, just via a
different prompt function).

---

