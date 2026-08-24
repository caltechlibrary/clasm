---
id: "0029"
title: "Redesign navigation as a domain picker; add Key Management, S3, and CloudFront domains"
date: "2026-07-02"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "867b407b-f927-4f92-a0c9-3a4112cb49d2"
origin_host: "MACMINI-RD.local"
---

**Context.** With Compute (EC2/AMI) at 12 features and real-AWS
verification (Phase 16) underway, the user raised that `awsops`'s actual
job spans more than EC2/AMI: this team's AWS footprint is really "deploy
and operate Invenio RDM" (Compute, plus the SSH key pairs instances
launch with) and "publish static websites" (S3 + CloudFront) — and the
existing single flat main menu doesn't make that structure visible, nor
does the current feature set actually cover the S3/CloudFront side of
the job at all (S3 today is only ever a write-destination inside Backup
Archive & Trim, never a managed resource in its own right). Growing a
single menu to cover all of this was rejected outright as unusable long
before reaching a final feature count.

**Decision.**
- Replace the single flat main menu with a domain picker: **Compute
  (EC2 & AMI)**, **Key Management**, **S3 (Buckets & Static Websites)**,
  **CloudFront**, **Exit**. Each domain has its own resource listing and
  its own numbered menu underneath (same shape Compute's menu already
  has today), reached via the picker and returned to via a "Back to
  domain picker" entry in every domain menu.
- **EC2 and AMI stay one domain ("Compute"), not two.** They're already
  deeply interleaved — "Create Instance *from* AMI," "Create AMI *from*
  Instance," and both Manage Tags and Show/Export Cloud-Init operate on
  "an instance or an AMI" as a single pick — splitting them would force
  those cross-cutting workflows to pick an arbitrary home or be
  duplicated across two menus.
- **Key Management becomes a first-class domain**, not just a label:
  key pairs get their own List/Create/Import/Delete primitives
  (`DESIGN.md` Features 13-16), not only the inline "type `new`" launch
  shortcut that already existed (2026-07-01 decision above) — that
  shortcut now calls the same standalone Create Key Pair primitive.
- **S3 gets full static-website scope**, not just a backup destination:
  bucket listing/creation, static website hosting configuration, local
  directory sync, and object browsing (`DESIGN.md` Features 17-21) — see
  the paired 2026-07-02 "CloudFront + OAC by default" decision above for
  the specific access-pattern default.
- **CloudFront gets core lifecycle scope**: list, show detail, create (S3
  origin + OAC), and invalidate (`DESIGN.md` Features 22-25) — not just
  read-only listing, since creating and refreshing a distribution are
  routine parts of standing up and updating a static site, not rare
  one-time console tasks.
- This redesign runs **alongside**, not blocking, Phase 16's real-AWS
  verification of Compute — the domain picker is a navigation refactor
  around Compute's existing, already-tested workflows, not a rewrite of
  them; see `PLAN.md` for how the new phases are sequenced relative to
  Phase 16/17.

**Rationale.**
- A two-level menu keeps each screen's numbered choices in the
  single-digit-to-low-teens range the interactive picker pattern
  (2026-06-30 decision, "Use numbered list selection...") was designed
  for, instead of scaling that pattern past where it stays usable.
- Merging EC2/AMI avoids fragmenting workflows that are, in AWS's own
  model, already cross-cutting between the two resource types.
- Scoping Key Management/S3/CloudFront generously now (rather than
  shipping thin listing-only versions and expanding later) matches this
  project's stated goal — reduce manual, undocumented AWS console work
  for this team's actual two use cases — instead of just adding a
  smaller surface that still leaves most of that work in the console.

**Rejected alternatives.**
- *Five separate top-level domains (EC2, AMI, Key Management, S3,
  CloudFront)* — matches the user's original phrasing most literally,
  but splits Compute's interleaved workflows for no real navigational
  benefit, since EC2 and AMI together are still a small enough menu on
  their own.
- *Key Management as a label only, no new primitives* — rejected because
  it leaves key pairs exactly as under-managed as today (create-only,
  buried inside instance launch), which doesn't actually close the gap
  that motivated calling it out as its own domain.
- *S3 scoped to backup-only, static website deferred* — rejected because
  static website hosting is one of the two concrete, named use cases
  driving this whole redesign, not a hypothetical future one.
- *CloudFront read-only for v1* — rejected because creating a
  distribution and invalidating its cache after a content update are
  both routine, not rare, once a site is live; deferring creation would
  leave the tool unable to actually finish standing up a site it just
  helped populate via S3 sync.
- *Pause Phase 16 to do this redesign first* — rejected; the two are
  independent (navigation refactor vs. verifying already-implemented
  Compute workflows against real AWS), so serializing them would waste
  time for no coordination benefit.

**Consequences.**
- `internal/ui` gains a `domainmenu.go` shared loop that Compute's
  existing menu code is refactored to use, rather than owning its own
  bespoke top-level loop (see `DESIGN.md` Architecture).
- Three new `internal/inventory` listers (`keypairs.go`, `buckets.go`,
  `distributions.go`) and a new `internal/awsclient/cloudfront.go` client
  are needed; `internal/awsclient/s3.go` is broadened well beyond
  Feature 11's original HeadObject-only scope.
- New IAM permissions are required beyond what's listed in `DESIGN.md`
  Assumptions as of 2026-07-01 — see that section's 2026-07-02 additions
  for the full list per domain.
- `Environment=production`'s extra safety-gate warning (today gating
  Compute's Terminate/Remove AMI) is *not* extended to the new domains'
  destructive operations in this round — an open item, not an oversight
  (see `DESIGN.md` Feature 26 and "Deferred to a Later Version").
- `PLAN.md` needs new phases for Key Management, S3, and CloudFront,
  sequenced after Phase 15 but not blocking Phase 16/17's completion of
  Compute's real-AWS verification and Bash retirement.

---

