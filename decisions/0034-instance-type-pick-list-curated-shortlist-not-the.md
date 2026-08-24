---
id: "0034"
title: "Instance type pick list: curated shortlist, not the full AWS catalog"
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
uuid: "c08e4a32-7938-4a0c-afe0-174ff3b536d2"
origin_host: "MACMINI-RD.local"
---

**Context.** The "Instance type" prompt was free text with only a
suggested default (`t3.micro`). Asked whether it could become a pick
list like Security group IDs/Subnet ID. AWS offers 600+ instance types
per region -- listing them all (even paginated) would reproduce, at a
much larger scale, the exact "flat list of every key pair in the
account... was noise, not help" problem already found and rejected for
key pairs at just 16 entries (2026-07-01 decision, "Support creating a
new key pair from within awsops"). A full list would also need
architecture filtering (x86_64 vs. arm64) against the picked AMI to
avoid creating a *new* incompatibility class right after fixing three
others this session (key pair name, IAM instance profile, instance-
type/AZ) -- `inventory.Image` doesn't carry AMI architecture today.

**Decision.** `promptInstanceType` offers a short, hand-picked list of
~9 types relevant to this team's actual usage (t3 family for testing/
small Invenio RDM instances, m5/c5/r5 for steady-state/compute/memory-
optimized needs), each labeled with vCPU/memory, plus "Other" to type
any value not listed. No AWS call is made to build this list -- it's
static. The instance-type-vs-AZ and instance-type-vs-ENA pre-flight
checks (this file, both entries above) are what actually validate
whatever value is chosen (curated or typed) against AWS, so the list
itself doesn't need to be exhaustive or live to be safe.

**Rationale.** Matches this project's established preference (key
pairs) for a short, curated list plus an escape hatch over an
exhaustive one; the real safety net against picking an incompatible
type is the two pre-flight checks, not an exhaustive picker.

**Rejected alternatives.**
- *Full list filtered by region + AMI architecture* -- rejected for
  now: still likely 100-300+ entries even filtered, and requires adding
  Architecture to `inventory.Image` and a new filtering call. Worth
  reconsidering if the curated list proves too restrictive in practice.
- *Full list, region-only, no architecture filter* -- rejected as the
  most noise for the least benefit: hundreds of entries, some of which
  wouldn't even work with the picked AMI.

**Consequences.**
- `internal/workflow/launch_prompts.go` gained `promptInstanceType`,
  `curatedInstanceTypes`, `instanceTypeChoice`/`instanceTypeChoiceLabel`.
  No new AWS permissions -- the list is static.
- The "Change instance type" recovery step in both pre-flight checks
  (above) now goes through this same function, so a corrected value
  also comes from the curated list + "Other", not a separate free-text
  prompt.

---

