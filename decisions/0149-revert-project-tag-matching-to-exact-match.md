---
id: "0149"
title: "Revert Project tag matching to exact-match lowercase \"project\" -- the fleet is clean now"
date: "2026-07-29"
status: accepted
kind: correction
trigger: ""
project: clasm
phase: ""
supersedes: ["0148"]
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "36873d01-ae61-4519-842d-564df85c553b"
origin_host: "MACMINI-RD.local"
---

**Context.** The case-insensitive `Project` matching decided earlier the
same day was explicitly a stopgap while the account's tags were
inconsistent. After confirming the real convention with a colleague
(lowercase `project`, predating clasm), the user retagged every
clasm-created resource that used capitalized `Project` -- found via a
live `resourcegroupstaggingapi` query across every region clasm operates
in (not the earlier, merely-historical `--debug`-log survey): one AMI,
two launch templates, and one EC2 instance, all account-wide. Each was
retagged (`project` added with the same value, `Project` removed) and
verified via `describe-tags`. With the fleet now consistently lowercase
everywhere, the user asked to remove the case-folding.

**Decision.** `tagValues` (`internal/inventory/instances.go`) reverted
to exact-match, keyed on lowercase `"project"` (not `"Project"`) --
matching the team's actual standard now that it's uniformly applied.
`Name`/`Environment` were never changed.

**Consequences.** Simpler code, one exact-match case instead of a
`strings.EqualFold` call; the `strings` import was removed as
unused. Every test fixture across `internal/inventory` and
`internal/workflow` that built a `Project` tag for a *derived*-field
assertion (`Instance.Project`, `Image.Project`,
`LaunchTemplateVersionDetail.Project`, `InstanceDetail.Project`,
`ImageDetail.Project`) was updated to lowercase `project` to match; the
several fixtures using `"Project"` merely as an arbitrary example key in
raw-tag-passthrough tests (`TestInstanceFromSDK_CarriesFullTagMap` and
its Image/LaunchTemplate siblings) or in generic Tag Management CRUD
tests (`bucket_tags_test.go`, `manage_tags_test.go`, unrelated to
`tagValues`) were correctly left untouched. New
`TestListInstances_DoesNotRecognizeCapitalizedProjectTag` locks in the
reversion -- confirmed failing against the case-insensitive code before
reverting it, so a future change can't silently reintroduce case-folding
without a visible, deliberate test failure. `go build`/`go vet`/
`go test ./... -race`/`gofmt -l` all clean.

---

