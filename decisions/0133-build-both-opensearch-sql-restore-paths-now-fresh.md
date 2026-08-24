---
id: "0133"
title: "Build both OpenSearch/SQL restore paths now: fresh-instance and already-populated-instance overwrite"
date: "2026-07-28"
status: accepted
kind: decision
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "b36c24ed-2e6e-4362-9a8a-62b55ab81f4b"
origin_host: "MACMINI-RD.local"
---

**Context.** DESIGN.md's Restore workflows needed to decide their scope
for v1 -- restoring only onto a fresh, empty instance is the user's
immediate need, but restoring onto an already-running instance to
replace its data is a known future need too.

**Decision (user's explicit call).** Design and build both paths in the
same pass, not defer the already-populated case to a later version --
"I know I'll need it, just don't know if it is tomorrow or six months
from now." Restoring over live data is accepted as destructive and
gated behind clasm's existing `ConfirmDestructive` (type-to-confirm),
the same tier already used for Terminate Instance, Remove AMI, and
IAM's Delete Role -- no new confirmation mechanism invented for this.

**Consequences.** Both Restore SQL Backup and Restore OpenSearch
Snapshot must detect whether the target already has conflicting data/
indices before proceeding, not just assume an empty target. For
OpenSearch specifically, restoring over existing indices requires
closing or deleting them first (OpenSearch's restore API can't overwrite
an open index) -- whether restore closes or deletes them is flagged as
still undecided (DESIGN.md, "Not decided yet").

---

