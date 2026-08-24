---
id: "0062"
title: "Add Feature 21.1, Manage Bucket Lifecycle Policies, with a Purpose-tag-driven guided/generic split"
date: "2026-07-08"
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
uuid: "79da6b2b-d9d9-49db-8186-5cb084148243"
origin_host: "MACMINI-RD.local"
---

**Context.** Before writing any Phase 20 code, the user identified three
bucket use cases this tool should make easy: a website bucket (already
Features 18-19), a shared backup bucket needing expiration and
transition-to-cheaper-storage policies on its objects (the pattern
Backup Archive & Trim already produces, per DECISIONS.md "Namespace
backup uploads by instance"), and an internal-use bucket with no
predictable policy shape. None of this existed in DESIGN.md — S3
Lifecycle Configuration management wasn't designed at all.

**Decisions (asked, user confirmed all four):**
1. **Bucket "purpose" is tagged and remembered, not just a creation-time
   wizard.** Create Bucket (Feature 18) now prompts for a purpose
   (Website/Backup/Internal) and applies it as a `Purpose` tag
   (`s3:PutBucketTagging`); Feature 17 (List Buckets) reads it back for
   every bucket so later features don't need to re-ask.
2. **Lifecycle policy scope is an optional prefix, blank = whole
   bucket** — the same convention as Feature 21's browse-filter
   addition, rather than forcing either a mandatory prefix or a
   whole-bucket-only model.
3. **Two different UIs, selected automatically by the `Purpose` tag**:
   `backup` gets a guided flow (two yes/no-shaped prompts: expire after
   N days, transition after N days); `internal` (and `website`, and any
   untagged bucket) gets a generic rule editor (named rules, arbitrary
   transitions, optional expiration) — one feature (21.1), one menu
   entry, branching internally, not two separate menu items.
4. **Multiple named rules with full CRUD**, not a single-policy-per-scope
   model — fetch all existing rules, let the operator pick one to edit
   or remove, or add a new one, then write the complete rule set back
   (the only way AWS's `PutBucketLifecycleConfiguration` API supports
   changes at all — it always replaces the whole rule set atomically).

**Smaller decisions made without a separate question round:**
- **Guided flow's storage-class choices are curated** (Standard-IA,
  Intelligent-Tiering, Glacier Flexible Retrieval, Glacier Deep Archive)
  rather than the full AWS enum — these four cover "make backups
  cheaper over time" without exposing storage classes irrelevant to
  that goal (One Zone-IA's reduced durability isn't appropriate for the
  only copy of a backup; Reduced Redundancy Storage is legacy). The
  generic editor (`internal`/`website`/untagged buckets) exposes the
  *full* `types.TransitionStorageClass` enum instead, matching its
  "unpredictable needs" framing.
- **Numbered 21.1, not renumbered into the 22-26 sequence** — CloudFront
  Features 22-26 already have ~15 cross-references across
  DESIGN.md/DECISIONS.md/PLAN.md; renumbering them to make room risked
  missing one silently. Mirrors PLAN.md's own existing convention of
  decimal-numbered insertions (Phase 15.1 through 15.26) rather than
  introducing a new pattern.
- **Rule removal and edits stay a plain yes/no confirm**, not the
  stronger dry-run + type-to-confirm tier, but the confirmation text
  must say plainly that this schedules *future* automated deletion, not
  an immediate one (see DESIGN.md, Security Considerations #13) — AWS
  evaluates lifecycle rules on its own cadence (typically within 24-48
  hours), not instantly on `PutBucketLifecycleConfiguration`.

**Rationale.** All four user-facing decisions were asked and confirmed
before any implementation started, per this project's design-then-code
discipline. The Purpose-tag branch (one feature, one menu entry) was
chosen over two separate menu entries because the user's own framing —
"the internal bucket is similar" — describes one capability used two
ways, not two capabilities.

**Rejected alternatives.**
- *Two separate menu entries* (e.g. "Manage Backup Policies" / "Manage
  Object Lifecycle Policies") instead of one Purpose-branching feature —
  rejected per the user's own framing above; also would require the
  operator to already know a bucket's purpose before picking the right
  menu item, defeating the point of tagging it.
- *Single active policy per bucket/prefix* (no rule naming/listing) —
  rejected; doesn't fit the internal bucket's explicitly "unpredictable"
  needs, and the guided flow's simplicity doesn't actually require
  giving up multi-rule support underneath (the guided prompts just
  populate one more named rule in the same underlying store).

**Consequences.** `S3API` grows further to include `PutBucketTagging`,
`GetBucketTagging`, `GetBucketLifecycleConfiguration`, and
`PutBucketLifecycleConfiguration` on top of Phase 20's already-broadened
surface (see "Phase 20 (S3 domain) scope decisions," below). `internal/
inventory.Bucket` gains a `Purpose` field, fetched during Feature 17's
existing per-bucket enrichment fan-out (no new listing pass). Phase
20's effort estimate in PLAN.md needs updating to reflect this
meaningfully larger scope.

---

