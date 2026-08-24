---
id: "0113"
title: "ARM64/Ubuntu 26.04: filter the instance-type list by AMI architecture, no new pre-flight check"
date: "2026-07-22"
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
uuid: "d7ffc93a-437e-4f54-933f-afe34b45a7cc"
origin_host: "MACMINI-RD.local"
---

**Context.** Adding arm64 (Graviton) support to the curated AMI and
instance-type lists raised the same question the IAM-profile picker
just answered: how to keep an operator from picking an
architecture-incompatible combination. The initial design proposed a
new pre-flight check mirroring `ensureInstanceTypeENACompatible` --
query, then offer "change instance type or abort" if the picked
instance type doesn't match the AMI's architecture.

**Decision.** Simplified: filter the instance-type picker's own choice
list by the already-picked AMI's architecture, the same approach just
adopted for the IAM-profile/role picker (see "Filter non-SSM-capable
profiles/roles from the picker, don't just annotate them," above) --
don't offer an instance type that would just be wrong, rather than
offering it and rejecting the pick afterward. `promptInstanceType`
gains an `arch string` parameter (`""` = no filter); the two top-level
launch-param collection functions pass the picked AMI's architecture,
the two ENA/AZ-incompatibility remediation call sites pass `""`
(unfiltered, matching their current behavior unchanged).

**Rejected alternative.** *A new architecture-compatibility pre-flight
check*, structurally cloning the ENA check -- rejected once the
IAM-profile picker's own live-testing feedback (same day) established
that filtering beats "show everything, reject on pick" whenever
there's no legitimate reason to show the invalid option in the first
place. There's no case where picking an arm64 instance type for an
x86_64 AMI (or vice versa) is ever valid, exactly the same shape of
argument that justified filtering there.

**Consequences.** Simpler than the rejected alternative: no new
`incompatibilityChoice` variant, no new remediation loop, no new tests
for a reject-then-retry flow. The two remediation call sites
(ENA/AZ "change instance type") deliberately stay unfiltered rather
than threading the AMI's architecture further through
`ensureInstanceTypeENACompatible`/the AZ check's own signatures --
accepted as a real but very unlikely gap (a non-ENA-required AMI old
enough to need that remediation path predates Graviton's existence in
practice).

---

