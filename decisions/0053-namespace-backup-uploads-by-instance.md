---
id: "0053"
title: "Namespace backup uploads by instance"
date: "2026-07-02"
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
uuid: "599b99ed-007d-49b4-bf24-087838a2f716"
origin_host: "MACMINI-RD.local"
---

**Context.** `sql-backups.library.caltech.edu` is meant to hold backups
from multiple systems, not just one instance -- but every upload key was
just `path.Base` of the source file (e.g. `caltechauthors-db-1-...sql.gz`
directly at the bucket root). Two instances producing identically- or
similarly-named backup files (a very real possibility -- not every
system's backup script embeds a distinguishing name the way this one
happens to) would silently collide and overwrite each other in the
shared bucket.

**Decision.** Every upload key is now namespaced by the source
instance's Name tag: `s3://bucket/<name>/<filename>`. `uploadKey(prefix,
filePath)` builds this from `path.Join`, used consistently for the
`aws s3 cp` destination, the `printf`'d key the instance reports back,
and the tool's own `s3:HeadObject` verification and `rm -f` path
resolution. An untagged instance (blank Name) falls back to its
instance ID as the prefix -- every instance needs *some* non-empty,
distinguishing prefix, and the ID is always available.

**Rationale.**
- Name (not instance ID) as the primary prefix keeps the bucket
  browsable by a human restoring from backup -- `newauthors/...` reads
  far better than `i-0c4c81336aea33d27/...`. The ID-on-blank-Name
  fallback covers the one case where Name alone wouldn't be usable at
  all.
- Building the prefix once, in `BackupArchiveAndTrim`, and threading it
  through as a single `prefix` parameter to `UploadBackupFiles` (rather
  than each of upload/verify/delete separately reconstructing "which
  instance is this for") keeps the same value used everywhere a key is
  built or matched, so upload, verification, and delete can never
  disagree about a file's key.

**Consequences.** `buildUploadCommand` and `UploadBackupFiles` gain a
`prefix` parameter; `BackupArchiveParams`'s `Directory`/`Bucket`-style
plain fields didn't need to change since the prefix is derived from the
already-available picked instance, not a new prompt. Existing backups
already sitting at the bucket root today are not automatically
migrated -- an operator restoring from a backup uploaded before this
change should account for that when scripting a bulk migration into the
new per-instance prefixes, if desired.

---

