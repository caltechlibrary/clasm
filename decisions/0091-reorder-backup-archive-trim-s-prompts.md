---
id: "0091"
title: "Reorder Backup Archive & Trim's prompts"
date: "2026-07-13"
status: accepted
kind: refinement
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
uuid: "96082f4c-0664-4ed1-97dc-4ec8f3a1ae56"
origin_host: "MACMINI-RD.local"
---

**Context.** The workflow asked its four questions in an order that
didn't match how an operator actually thinks about the task: instance,
backup directory, age threshold (days), S3 bucket -- age threshold sat
between "where are the files" (instance, directory) and "where are
they going" (bucket), which reads oddly since the threshold is more
naturally understood once both endpoints are already known. Requested
directly, with the exact desired order confirmed: instance, directory,
bucket, then age threshold.

**Decision.** Moved the S3 bucket prompt (and its immediately-following
`BucketRegion`/`newS3Client`/`CheckS3BucketAccess` pre-flight sequence)
to run directly after the backup directory prompt, ahead of the age
threshold prompt, which now runs last, immediately before the dry-run
listing.

**Rationale.** "Of the files in that directory, which are old enough to
move to that bucket" reads as one coherent question once both the
source directory and destination bucket are already fixed, rather than
asking "how old" before the destination is even known.

**Consequences.** No parameter or return-type changes -- purely a
reordering of existing prompt calls within `backupArchiveAndTrim`.
Every existing test's input string (four `\n`-joined answers in read
order) was updated to match the new order; assertions were otherwise
unchanged.

---

