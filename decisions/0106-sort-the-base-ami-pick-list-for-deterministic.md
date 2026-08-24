---
id: "0106"
title: "Sort the base-AMI pick list for deterministic ordering"
date: "2026-07-21"
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
uuid: "f7066c78-19d5-4254-a94d-b5a5f36049d5"
origin_host: "MACMINI-RD.local"
---

**Context.** Building two launch templates for comparable systems back
to back, the operator noticed the Ubuntu LTS entries in the base-AMI
pick list (Feature 2/3, Create Launch Template from Cloud-Init YAML)
came up in a different order each run, making it easy to pick the
wrong release by muscle memory. Root cause: `inventory.ListImages`
aggregates owned AMIs across regions via concurrent per-region
goroutines feeding a channel, and `listOfficialUbuntuAMIs`
(`official_ubuntu_amis.go`) iterates `clients`, a Go map -- both orders
are randomized by the language, not by AWS, so the same AMI could land
in a different list position every time the picker opened.

**Decision.** `imagesWithOfficialUbuntu` (the shared function feeding
`pickImage` in every one of the four launch flows) now sorts the
combined list by Region then Name before returning it, using the same
`sort.Slice` approach `inventory.ListBuckets` already established for
the same class of problem (deterministic order after a concurrent/map
aggregation). No new dependency, no change to what's offered -- only
the order is now stable across runs.

---

