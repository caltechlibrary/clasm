---
id: "0130"
title: "OpenSearch snapshot scope: current-version record/list indices plus stats aggregates, excluding raw stats events and system indices"
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
uuid: "5044a3a8-4fee-43df-b81e-f200914783a3"
origin_host: "MACMINI-RD.local"
---

**Context.** RDM relies on OpenSearch for two distinct things --
list-view/JSON-API search indices (fully derivable from Postgres, just
slow: 6-7 hours for a 100,000+-record repository) and usage-statistics
indices (not derivable from Postgres at all, the only copy). Sizing
this against a real `_cat/indices` pull from CaltechAUTHORS production
(2026-07-28) showed the raw `events-stats-*` indices at ~11.3GB and
still growing ~1.5-2GB/month, versus ~5.6GB for everything else
combined -- confirmed with the user that no regular or ad-hoc report
queries the raw-event indices directly, all reporting comes from
Postgres.

**Decision.** Snapshot current-version `rdmrecords-*`/`users-*`/
`communities-*`/`requests*`/`requestevents-*`/`names-*`/
`affiliations-*`/`funders-*`/`awards-*`/`subjects-*`/`vocabularies-*`/
`groups-*`/`domains-*`/`communitymembers-*` (~1.5GB) plus the
`stats-record-view-*`/`stats-file-download-*` monthly aggregates and
`stats-bookmarks` (~4.1GB) -- the aggregates are what actually back
displayed view/download counts, not the raw events.
`.ds-<prefix>-auditlog-audit-log-*` (~0.3GB) included provisionally on
the same "irreplaceable if lost" reasoning, droppable later if unneeded.
Raw `events-stats-*` indices and system/plugin indices (`.opensearch-*`,
`.kibana*`, `.plugins-*`) are excluded.

**Consequences.** Target snapshot size for a CaltechAUTHORS-scale
repository is under ~8GB, not ~17GB. If ad-hoc reporting ever starts
querying raw stats events directly, this exclusion needs revisiting.
Empty, zero-doc versioned indices left over from past schema migrations
aren't explicitly filtered -- 208 bytes each, not worth the added
pattern complexity.

---

