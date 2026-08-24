---
id: "0147"
title: "RDM Backup & Restore moved before Configuration in the domain picker"
date: "2026-07-29"
status: accepted
kind: refinement
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
uuid: "0ece16a4-1c09-41bc-8eff-3e798d879ca7"
origin_host: "MACMINI-RD.local"
---

**Context.** The domain picker's order had `Configuration` (added
2026-07-24) immediately before `RDMBackupRestore` (added 2026-07-29,
chronologically the seventh domain), simply because each new domain was
historically appended to the end of `domainItems`.

**Decision.** The user's explicit call: reorder so `RDMBackupRestore`
comes right after `IAM` and before `Configuration` -- an operational
domain used routinely belongs ahead of clasm's own settings menu, which
is used rarely by comparison. `domainItems` and `DomainActions`' field
order both updated to match; `DomainActions`' own doc comments no longer
claim an ordinal ("sixth domain"/"seventh domain") for the reordered
entries, since that would conflict with this file's own dated addenda
describing chronological addition order, not current picker position.

**Consequences.** `domain_menu_test.go`'s two index-based dispatch tests
(`TestRunDomainPicker_DispatchesToConfiguration`/
`...ToRDMBackupRestore`) swapped their `"6\n"`/`"7\n"` picks accordingly.
No other test's numeric indices were affected (`TestDomainItems_
NoExitEntry`'s count of 7 is unchanged -- only order moved, not count).

---

