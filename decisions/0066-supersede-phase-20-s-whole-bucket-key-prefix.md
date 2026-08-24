---
id: "0066"
title: "Supersede Phase 20's whole-bucket key-prefix filter with per-directory-level (Delimiter-based) listing and a substring filter"
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
uuid: "8b4dfea4-fccd-4549-9345-d35e49f97810"
origin_host: "MACMINI-RD.local"
---

**Context.** The 2026-07-08 "Phase 20 (S3 domain) scope decisions"
entry (below) added a key-prefix filter to Browse/Manage Objects
specifically because a single real bucket (e.g.
`sql-backups.library.caltech.edu`) can hold many objects across many
per-instance prefixes, and listing everything unconditionally doesn't
scale to this team's actual usage. That filter works against one flat,
whole-prefix listing (`ListObjectsV2` scoped to whatever prefix the
operator typed once, upfront). The file manager instead browses
hierarchically, one directory level at a time (DESIGN.md 21.5) -- a
different listing shape that changes what "filtering" should mean and
what it costs.

**Decision.** List one directory level per call via `ListObjectsV2`
with `Delimiter=/` (`CommonPrefixes` for folders, `Contents` for
files). Filtering (`f` / `/`) narrows the current level's already-
fetched rows by substring match -- cheap, since "current level" is
never the whole bucket regardless of how the tree is shaped below it.

**Rejected alternatives.** *Keep Feature 21's original flat,
whole-prefix listing plus its upfront prefix prompt* -- rejected; it
doesn't support hierarchical drill-down/breadcrumb navigation at all,
which the file manager's browsing model depends on. *Add a client-side
substring/glob filter on top of a flat whole-bucket listing* --
considered and rejected earlier in this same design pass, before
per-level Delimiter-based listing was decided, specifically because it
would mean fetching a potentially large flat listing before any
filtering could narrow it; per-level listing removes that cost by
construction, which is why the filter approach could be revisited and
approved here.

**Consequences.** Feature 21's original "Filter by key prefix (blank for
all)" prompt is retired along with the rest of Feature 21's standalone
wizard (see "Design the S3 object management UI/UX pass," above) once
Phase 20.1 ships.

---

