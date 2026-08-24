---
id: "0122"
title: "Configure clasm domain: explicit Save, region changes deferred to next launch"
date: "2026-07-24"
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
uuid: "e70afab3-b20a-4a74-9112-5ff8bc59512a"
origin_host: "MACMINI-RD.local"
---

**Context.** `~/.clasm`'s `regions`/`backup_directories`/`origin_tag`
settings are hand-edited YAML only today. The user requested a top-level
menu item to view/create/update this config file from within clasm,
bundled into v0.0.5 alongside the Instance/AMI Detail Views below.

**Decision 1: edits happen against an in-memory working copy; nothing
touches `~/.clasm` until an explicit "Save" action.** 'q' with unsaved
changes pending warns before discarding.

**Rejected alternative.** *Write-immediately per edit* (add a region,
save instantly) — fewer steps, but removes the ability to back out of a
half-finished edit sequence, and doesn't match this project's general
caution around persisting state without a visible, explicit action.

**Decision 2: region add/remove is free-text, not validated against
AWS's actual published region list.** A typo'd region simply won't
resolve to a usable client on next launch — the same failure mode as
hand-editing the YAML today, not a regression.

**Rejected alternative.** *Hardcode a list of valid AWS region names to
validate against* — would catch typos immediately, but needs its own
maintenance as AWS adds regions, and there's no "list all regions" API
call that doesn't itself require an already-configured client. Not worth
the upkeep for a first pass.

**Decision 3: region changes take effect on next launch, not live.**
Surfaced directly in the UI when saving, rather than left as a silent
gap. `cmd/clasm/main.go` builds the region-to-client map once at
startup from the config loaded at that time; retrofitting the running
client map to react to a mid-session config edit was judged unnecessary
complexity for how rarely regions actually change.

**Decision 4: new `config.Save` writes with `0644`, not the `0600`
private-key convention.** No secrets live in `~/.clasm`, unlike the SSH
private keys `create_key_pair.go` writes.

