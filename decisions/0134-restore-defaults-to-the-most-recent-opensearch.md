---
id: "0134"
title: "Restore defaults to the most recent OpenSearch snapshot, with the option to pick a specific dated one -- requires each archive run to land in its own S3 sub-prefix, not a shared one"
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
uuid: "0e550aca-1f04-4adf-b1d0-07c74de25c93"
origin_host: "MACMINI-RD.local"
---

**Context.** DESIGN.md's "Restore OpenSearch Snapshot from S3" needed a
way to browse backup history in S3. An earlier draft of "Archive
OpenSearch Snapshot to S3" used `aws s3 sync --delete` into one shared
destination prefix per instance -- directly mirroring EBS's own
single-snapshot state.

**Decision.** Rejected the shared, `--delete`-synced prefix. Syncing a
shared prefix with `--delete` makes S3 mirror EBS exactly -- meaning S3
would only ever hold the *current* snapshot too, since the local one is
deleted after every sync. That defeats "pick a specific dated backup"
entirely, since there'd be nothing but the latest to pick from. Each
archive run instead syncs into its own new, snapshot-named sub-prefix
(`opensearch-snapshots/<snapshot-name>/`), with no `--delete` -- S3
accumulates real history independent of what EBS currently holds.

**Consequences.** Restore lists sub-prefixes (`ListObjectsV2` with a
`/` delimiter, `CommonPrefixes`) rather than individual objects,
defaulting to the most recent by name (snapshot names are timestamped,
so they sort lexically) with the option to pick an older one. S3 storage
for OpenSearch backups now grows with every archive run, not just with
data volume, since nothing is deduplicated across runs -- exactly what
the app-managed cleanup decision above exists to bound.

---

