---
id: "0061"
title: "Phase 20 (S3 domain) scope decisions: defer public-read opt-out, add a key-prefix filter"
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
uuid: "f73d12a8-be34-4d24-9458-b76398e49694"
origin_host: "MACMINI-RD.local"
---

**Context.** Before starting Phase 20 (S3 domain: List/Create Bucket,
Configure Static Website Hosting, Sync Local Directory to Bucket,
Browse/Manage Objects), two places where DESIGN.md's existing Features
19 and 21 needed a concrete scope call that the design text itself
didn't fully pin down.

**Decision 1 — defer the public-read bucket policy opt-out.** Feature 19
(Configure Static Website Hosting) mentions an operator can explicitly
opt into a public-read bucket policy instead of the CloudFront + Origin
Access Control default -- but CloudFront (Phase 21, Feature 24) doesn't
exist in this codebase yet, so there's nothing for the default path to
actually hand off to. Phase 20 implements only the default path
(configure website documents; bucket stays private via Feature 18's
`PutPublicAccessBlock`); where DESIGN.md's text says "hand off to
CloudFront," awsops instead prints that CloudFront support isn't
implemented yet. The public-read opt-out (its own explicit warning,
confirmation, and `s3:PutBucketPolicy` call) is deferred until there's
an actual need for it.

**Decision 2 — add an optional key-prefix filter to Browse/Manage
Objects.** Not in DESIGN.md's original Feature 21 text, which lists
every object in the bucket unconditionally. This team's actual S3 usage
(e.g. `sql-backups.library.caltech.edu`, namespaced
`<instance-name>/<filename>` per DECISIONS.md's "Namespace backup
uploads by instance") means a single real bucket can hold many objects
across many per-instance prefixes -- listing everything unconditionally
would be substantially less usable on this team's actual buckets than on
a small test bucket. Feature 21 now prompts `"Filter by key prefix
(blank for all)"` before calling `s3:ListObjectsV2`; blank preserves the
original "list everything" behavior exactly.

**Rationale.**
- Both decisions were surfaced as explicit questions before writing any
  code, per this project's design-then-implement discipline, rather than
  silently decided during implementation.
- Deferring the public-read opt-out avoids building a whole secondary
  confirmation-and-policy-construction path for something DESIGN.md
  itself frames as a secondary escape hatch, before the primary
  (CloudFront) path it's an alternative *to* even exists.
- The prefix filter is a small, backward-compatible addition (default
  behavior unchanged) directly motivated by how this team's own S3
  buckets are actually structured, not a hypothetical future need.

**Rejected alternatives.**
- *Build the public-read opt-out now anyway* — rejected; it would mean
  significant Feature 19 surface area (policy JSON construction, its own
  confirmation tier) serving a path that's explicitly secondary in
  DESIGN.md's own framing, before Phase 21 (CloudFront) even exists to
  make the *primary* path complete.
- *Implement Browse/Manage Objects exactly as spec'd, no filter* —
  rejected; DESIGN.md's own Feature 1 precedent (PickList pagination for
  >50 items) already acknowledges large lists are a real concern for
  this tool, and a bucket with thousands of objects across many prefixes
  is a materially worse experience without a filter than EC2/AMI/key
  pair lists ever are.

**Consequences.** PLAN.md's Phase 20 work items and DESIGN.md Features
19 and 21 are updated to match. When Phase 21 (CloudFront) ships, Feature
19's "hand off to CloudFront" message should be revisited -- either
wired to an actual Create Distribution entry point, or left as
informational if the operator is expected to navigate there manually.

---

