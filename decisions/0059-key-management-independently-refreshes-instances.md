---
id: "0059"
title: "Key Management independently refreshes instances for Delete Key Pair's dependency check"
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
uuid: "cb0b87c6-ea1a-45dc-86f5-2999979678a0"
origin_host: "MACMINI-RD.local"
---

**Context.** Phase 19 (Key Management domain) implements Delete Key Pair
(DESIGN.md Feature 16), which warns about instances launched with the
key pair being deleted -- the same dependency-check pattern Remove AMI
(Phase 11) already uses, filtering an already-fetched `ListInstances`
result rather than making a fresh AWS call. But Compute's `state.instances`
in `cmd/awsops/main.go` is only populated once the operator has entered
the Compute domain at least once in the current run -- if Key Management
is entered first, that slice is nil, and Delete Key Pair's warning would
silently under-report (or entirely miss) real dependents.

**Decision.** Key Management's own `refreshKeyMgmt` closure in `main.go`
independently calls `inventory.ListInstances` (not just `ListKeyPairs`)
every time the domain is entered or its listing is refreshed, purely to
keep the dependency check correct -- the fetched instances aren't
displayed by Key Management, only used internally.

**Rationale.**
- Correctness of a safety-tier warning matters more than saving one
  cheap, two-region `DescribeInstances` call -- consistent with this
  project's existing bias (e.g. Compute already refreshes its own
  listing on every domain entry, not just once at startup, specifically
  so displayed/used data is never stale).
- The alternative -- sharing a single `state.instances` across both
  domains, refreshed only by whichever domain is entered -- would make
  Key Management's correctness depend on navigation order, which is a
  subtle, easy-to-miss bug class (works fine in every manual test that
  happens to visit Compute first).

**Rejected alternatives.**
- *Share Compute's `state.instances` unconditionally* — rejected for the
  navigation-order fragility above.
- *Only fetch instances lazily inside `DeleteKeyPair` itself, not on every
  Key Management refresh* — rejected; it would mean the Show Resource
  Lists refresh and the Delete Key Pair action could see different data
  if instances changed in between, and it complicates `DeleteKeyPair`'s
  signature (already takes `instances []inventory.Instance` like
  `RemoveAMI` does for images) for no real benefit over fetching once per
  Key Management refresh.

**Consequences.** One extra `ec2:DescribeInstances` fan-out (across
configured regions) on every Key Management domain entry and every
"Show resource lists" refresh within it. Negligible cost; Key Management
still displays only its own key pair listing, not instances.

---

