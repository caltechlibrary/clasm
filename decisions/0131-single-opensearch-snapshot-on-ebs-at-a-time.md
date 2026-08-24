---
id: "0131"
title: "Single OpenSearch snapshot on EBS at a time; delete via the OpenSearch API, never a raw filesystem delete"
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
uuid: "907adc75-43b5-4241-abae-3c38f0ed98a5"
origin_host: "MACMINI-RD.local"
---

**Context.** SQL backups keep several days' `.sql.gz` files on EBS
before Feature 11 trims them by age -- EBS cost makes the same model
impractical for OpenSearch snapshots, and OpenSearch's own snapshot
repository is incremental (later snapshots can reference earlier ones'
segment files), so blind age-based file deletion (Feature 11's own
model) is unsafe applied here.

**Decision.** Exactly one snapshot is ever live on EBS. Once the
current one is confirmed synced to S3, clasm deletes it via
OpenSearch's own `DELETE /_snapshot/<repo>/<name>` API -- never a raw
`rm` on the repo directory -- so the repository's own metadata and any
still-referenced segments stay consistent.

**Consequences.** Local disk usage for OpenSearch backups stays roughly
flat regardless of dataset size, unlike SQL's several-days-of-
accumulation-then-trim pattern. A restore of anything but the current
snapshot must come from S3, since EBS never retains history at all --
directly motivating Restore's S3-side picker (see above).

---

