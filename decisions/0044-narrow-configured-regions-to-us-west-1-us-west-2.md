---
id: "0044"
title: "Narrow configured regions to us-west-1/us-west-2"
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
uuid: "34f8f8c5-9d50-4196-8981-4428b880c7bb"
origin_host: "MACMINI-RD.local"
---

**Context.** The official-Ubuntu-AMI addition (above) surfaced a real
launch failure (`InvalidKeyPair.NotFound`) precisely because it made
`us-west-1` -- one of the four originally-configured regions this team
doesn't actually run anything in -- selectable as a base-AMI region for
the first time. The account's real resources (key pairs, security
groups, subnets already provisioned for real use) only exist in
`us-west-1`/`us-west-2` in practice; `us-east-1`/`us-east-2` were
configured from the start but never actually used.

**Decision.** `awsclient.Regions` narrowed from
`{us-east-1, us-east-2, us-west-1, us-west-2}` to `{us-west-1,
us-west-2}`. Every region-fanned-out listing, pick list, and lookup
(instances, AMIs, key pairs once Key Management ships, official Ubuntu
AMI lookup, etc.) automatically follows since they all iterate over this
one slice -- no other code changes needed.

**Rationale.** Directly shrinks the blast radius of the class of bug
just found: every region-scoped resource (key pairs, security groups,
subnets) that doesn't exist in a region this team never uses can no
longer surface unexpectedly through a feature (like the official-Ubuntu
lookup) that fans out across every configured region. This doesn't
replace the deeper fix (validating a chosen key pair actually exists in
the target region -- tracked separately) but removes the two regions
most likely to produce this exact surprise with zero cost, since nothing
runs there anyway.

**Rejected alternatives.**
- *Leave all four regions configured, rely solely on per-resource
  validation* -- rejected as not mutually exclusive with this change;
  both are worth doing. Narrowing regions fixes the "AMI in a region we
  don't use" case at the root; validation (separate work) still matters
  for genuine two-region mismatches (e.g. a key pair that only exists in
  `us-west-2` being typed while launching into `us-west-1`).

**Consequences.**
- `internal/awsclient/regions.go`, `regions_test.go` updated.
- `DESIGN.md`/`helptext.go` updated; `awsops.1.md` regenerates from
  `helptext.go` via the existing `cmt`/Makefile pipeline, not edited by
  hand.
- Every existing region-fanned-out feature (instance/AMI listing,
  official Ubuntu AMI lookup) now makes two round-trips instead of four
  per refresh/launch -- strictly less work, no behavior change beyond
  which regions are included.

---

