---
id: "0042"
title: "Offer official Ubuntu LTS AMIs alongside owned AMIs when picking a base AMI"
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
uuid: "879e0d60-ec6b-4b7d-9f47-0a5af99f3389"
origin_host: "MACMINI-RD.local"
---

**Context.** Follow-up to clarifying the Create-from-Cloud-Init-YAML
workflow: the user's actual goal was launching an entirely new machine
from a stock base image + cloud-init, not from one of the account's
existing, already-application-specific AMIs (`plots-backup`,
`newauthers-clone-2026-06-25`, `authors-2024-03-07` -- all pre-existing
snapshots, not generic OS images). Since the AMI pick list is scoped to
AMIs the account owns (a deliberate existing decision -- otherwise the
list would include every public AMI in existence), a stock Ubuntu AMI
never appeared as an option. The user's own framing: keep it simple,
cover the likely common case (official Ubuntu images, plus what's
already owned); if something more exotic is needed, copying the
specific public AMI into the account first (already-documented guidance)
remains the answer.

**Decision.** The "Select an AMI"/"Select a base AMI" pick list (Feature
2/3) now also includes a small, curated list of official Ubuntu LTS
releases -- currently 24.04 (Noble Numbat) and 22.04 (Jammy Jellyfish),
amd64/x86_64 only -- resolved via `ec2:DescribeImages` against
Canonical's well-known, publicly documented AWS account ID
(`099720109477`), picking the single most recently published AMI per
release per region. This lookup happens once, on demand, right before
the AMI pick list is shown (not as part of the general resource-listing
refresh, which stays owned-AMIs-only, unchanged) -- launching an
instance is an infrequent, deliberate action, so a handful of extra
`DescribeImages` calls at that moment is not the same cost concern it
would be if it ran on every screen refresh. Best-effort: if the lookup
itself errors, the picker silently falls back to owned AMIs only, same
as this tool's other best-effort diagnostics.

**Rationale.**
- Matches the explicit scope given: "pretty simple," "cover the likely
  bases," with anything more exotic staying a manual (already-documented)
  copy-the-public-AMI-in step -- not a general public-AMI browser.
- amd64-only matches the curated instance-type list's architecture
  (2026-07-02, "Instance type pick list: curated shortlist, not the full
  AWS catalog") -- none of the curated instance types are Graviton/arm64,
  so offering arm64 Ubuntu AMIs would create options that don't actually
  pair with anything in the other curated list.
- Carrying `EnaSupport` through from the real `DescribeImages` response
  (not defaulting it) matters here specifically: official Ubuntu AMIs
  are modern and genuinely ENA-enabled, so without this the instance-
  type-vs-AMI-ENA-support pre-flight check (2026-07-02, above) would
  wrongly flag every one of them as incompatible with the curated
  instance types that actually work fine with them.

**Rejected alternatives.**
- *A general public-AMI browser/search* -- explicitly declined by the
  user in favor of a small curated set; a full public-AMI search is a
  much bigger feature (arbitrary owner IDs, name search, architecture
  filtering UI) that isn't needed for the stated common case.
- *Include arm64/Graviton variants now* -- deferred: no curated instance
  type could launch one today; revisit if/when Graviton types are added
  to the curated instance-type list.
- *Fetch these as part of the general resource-listing refresh* --
  rejected: that listing is specifically scoped to "what does this
  account own" (an oversight/inventory view); Canonical's AMIs aren't
  owned by the account and aren't something this team needs to track,
  only something useful at the moment of picking a base image.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go` (new): `latestUbuntuAMI`,
  `listOfficialUbuntuAMIsInRegion`, `listOfficialUbuntuAMIs`,
  `imagesWithOfficialUbuntu`.
- `launch_instance.go`/`launch_from_cloud_init.go`: both AMI pick lists
  now go through `imagesWithOfficialUbuntu` before display.
- No new AWS permissions -- `ec2:DescribeImages` is already required
  (DESIGN.md, Assumptions), and querying it against a different `Owners`
  value needs no additional IAM grant.

---

