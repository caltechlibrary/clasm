---
id: "0129"
title: "A seventh Domain Picker entry, RDM Backup & Restore, consolidating archive and restore for both SQL and OpenSearch; relocates Feature 11 out of Compute"
date: "2026-07-28"
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
uuid: "ad1a715d-873b-4ca8-89d6-b0f203ec37fa"
origin_host: "MACMINI-RD.local"
---

**Context.** Adding OpenSearch archive plus SQL/OpenSearch restore
(three new operations) alongside Feature 11 (Backup Archive & Trim,
currently item 11 of 12 in Compute's own menu) would grow an already-
large, otherwise routine-EC2-lifecycle menu, and restore is a
meaningfully more dangerous class of operation than anything else
Compute currently holds.

**Decision.** A new top-level domain, alongside Compute/Key Management/
S3/Tag Management/IAM/Configuration, grouping all four RDM backup/
restore operations together. Feature 11 relocates into it unchanged
(Compute's menu shrinks from 12 items to 11) rather than being
duplicated or cross-listed in both places.

**Consequences.** `DomainActions`/`domainItems` (`domain_menu.go`) gain
a seventh field/entry (PLAN.md Phase 20.48); a new `rdm_menu.go` follows
the same loop-until-'q' shape as `configure_menu.go`/`tagmgmt_menu.go`;
DESIGN.md's Compute-domain menu ASCII box needs its own stale-doc
cleanup as part of the same pass.

---

