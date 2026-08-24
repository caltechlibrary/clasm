---
id: "0030"
title: "CloudFront + OAC by default for static websites, not public-read buckets"
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
uuid: "dfcf1fe5-6865-437b-b561-04eaf00d0634"
origin_host: "MACMINI-RD.local"
---

**Context.** Scoping the new S3 domain's static-website primitive
(Feature 19/24 in `DESIGN.md`) raised a real security-relevant default:
the classic "S3 static website hosting" pattern most tutorials show
makes the bucket world-readable via a public-read bucket policy. This
team already treats public-AMI/public-exposure as something to warn
about explicitly (`DESIGN.md` Security Considerations #4), and a tool
that makes "public S3 bucket" the path of least resistance for every new
static site works against that stance.

**Decision.** Feature 18 (Create Bucket) enables
`s3:PutPublicAccessBlock` (all four settings) by default on every new
bucket. Feature 19 (Configure Static Website Hosting) only sets the
bucket's website document config; it does not open public access.
Standing up an actual public-facing site is Feature 24 (Create
Distribution): CloudFront + a per-distribution Origin Access Control,
with the bucket policy scoped to that distribution's ARN
(`s3:PutBucketPolicy` restricted by `AWS:SourceArn`) so the bucket stays
private and only that CloudFront distribution can read it. A public-read
bucket policy remains available as an explicit opt-out inside Feature
19, gated by its own separate confirmation that plainly states the
bucket becomes world-readable directly — never the default, never
reachable by just accepting defaults through the flow.

**Rationale.**
- Matches this tool's existing posture toward exposure (dry-run/warn
  before anything that broadens what's publicly reachable).
- CloudFront in front of a private bucket is also just better practice
  independent of security — caching, HTTPS, and a real CDN domain name
  come for free, not just access control.
- Keeping the public-read path available (not removed) avoids blocking a
  legitimate simple-case use if someone genuinely wants it, while making
  sure it's never the accidental default.

**Rejected alternatives.**
- *Public-read bucket policy as an equal, unranked option* — considered
  and rejected in this session's design discussion; the concern was that
  presenting both paths as equivalent choices, with no recommended
  default, makes it too easy to pick the less safe one out of habit or
  unfamiliarity with OAC.

**Consequences.**
- Feature 24 (Create Distribution) needs write access to the bucket
  policy (`s3:PutBucketPolicy`) in addition to CloudFront permissions —
  see `DESIGN.md` Assumptions.
- Standing up a fully working static site now requires walking through
  two features (19 then 24) rather than one; `DESIGN.md` notes the
  handoff between them explicitly so the flow doesn't feel like two
  disconnected tasks.
- ACM certificate provisioning for a custom domain name on the
  distribution is out of scope (see `DESIGN.md`, "Deferred to a Later
  Version") — Feature 24 assumes the certificate already exists.

---

