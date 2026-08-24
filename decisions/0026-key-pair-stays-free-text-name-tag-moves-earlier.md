---
id: "0026"
title: "Key pair stays free text; Name tag moves earlier"
date: "2026-07-01"
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
uuid: "1b54e368-d38f-4113-8ba5-5f58264a8612"
origin_host: "MACMINI-RD.local"
---

**Context.** A prior fix (this same day) added pick lists for key pair
name, security group IDs, and subnet ID, fetched from the AMI's region,
after real-AWS testing surfaced a typo-prone free-text UX (typing a
security group *name* instead of an ID caused a real AWS
`InvalidParameterCombination` error). Further real-AWS testing then
showed the key pair pick list didn't help: unlike opaque `sg-xxxx`/
`subnet-xxxx` IDs, key pair names are already human-readable, and a flat,
unsorted list of every key pair across the account (16+ entries in one
real test run, most irrelevant to the AMI being launched) added noise
without solving a real problem. Separately, the user noted the Name tag
prompt landed too late in the flow — after all of instance type/key
pair/security groups/subnet/IAM profile/user data — when naming the
instance is a natural first step once the AMI is picked.

**Decision.**
- Revert the key pair prompt to free text (`ui.Prompt` with
  `requireNonEmpty`), removing `listKeyPairNames` and the pick-list
  wrapper. Security group IDs and subnet ID keep their pick lists —
  those IDs are the genuinely opaque case the original gap was about.
- Move the Name tag prompt to immediately after the AMI pick (and, for
  the cloud-init-first workflow, immediately after its own AMI pick),
  before Instance type and everything after it.

**Rationale.** Pick lists help when the identifier is opaque and the
account/region has more of them than a person can remember (security
groups, subnets). They don't help when the identifier is already a
name the user chose and knows — showing every key pair in the account
regardless of relevance is worse than just typing the name. Naming the
instance right after picking its AMI matches how a person actually
thinks through the workflow (what is this, then how is it configured).

**Trade-off.** A key pair pick list would still catch a typo'd key pair
name before `RunInstances` runs; free text defers that to AWS's own
error. Accepted, since `ec2:RunInstances` rejects an unknown key pair
name immediately and unambiguously, unlike the security-group-name-vs-ID
confusion this was originally meant to prevent.

---

