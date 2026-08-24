---
id: "0125"
title: "Cloud-init picker: a 'q' cancel sentinel, and a second bug found while tracing it (SyncLaunchTemplate exits the whole program on cancel)"
date: "2026-07-28"
status: accepted
kind: decision
trigger: implementation
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "03d72aab-75ff-4765-8a61-e8b854b7c8ad"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md bug report: "When selecting a cloud init, there is
no obvious exit, 'q' is treated as a filename." See DESIGN.md,
"Cloud-Init Picker: Discoverable Cancel + a Second, Related Bug",
PLAN.md Phase 20.45. Tracing where a cancel signal actually goes
surfaced a second, more serious bug in the same code path.

**Decision, part 1 (the reported bug).** `promptCloudInitYAMLFile`
(`userdata.go`) is a free-text `huh.Input` field -- unlike every
Select-based picker in this app, `huh.Input` has no Quit-key binding at
all (confirmed by reading `huh`'s own `InputKeyMap`), so 'q' is simply
typed as literal text. Fix: the label gains an explicit hint
(`"Cloud-init YAML file path (q to cancel)"`), and a bare `"q"` (exact
match, checked *before* the existing `"@"`-prefix stripping) returns
`ui.ErrCancelled` instead of attempting to read a file named "q".

**Decision, part 2 (the bug found while tracing part 1).**
`SyncLaunchTemplate` (`launch_template_sync.go`) is the only one of
`promptCloudInitYAMLFile`'s three call sites that doesn't wrap its
testable core's return with `cancelledIsNil` -- `CreateInstanceFromCloudInit`
and `CreateLaunchTemplateFromCloudInit` both already do
(`return cancelledIsNil(w, err)`). Because of that gap, *any*
cancellation inside `syncLaunchTemplate` -- not just at the cloud-init
file prompt, also at the preceding `promptLaunchTemplateVersion` step --
propagates raw up to `runMainMenu`'s dispatch, where `isExitSignal`
(the fallback for a cancel signal that escaped an inner
`cancelledIsNil`) treats it as "exit the entire clasm program" rather
than "return to the Compute menu." Fix: `SyncLaunchTemplate` now wraps
the same way its siblings do:
`return cancelledIsNil(w, syncLaunchTemplate(ctx, w, clients, lt, nil, nil))`.
This is the identical bug class already found and fixed once before in
this codebase (v0.0.3, `backup_archive.go`'s bucket-picker
cancellation) -- it recurred here because that fix was applied at the
one call site reported at the time, not generalized into a check
across every entry point with the same shape.

**Rejected alternative.** *A generic mechanism (lint rule or shared
wrapper) to catch a missing `cancelledIsNil` at every `MenuActions`
entry point automatically* -- worth wanting given this is the second
occurrence of the same bug class, but auditing all ~30 entries is a
larger, separate sweep than this narrowly-reported bug calls for. Left
as a candidate for a dedicated pass later, not undertaken now.

**Consequences.** A cloud-init YAML file literally named `q` (no
extension) can no longer be referenced bare -- prefixing it with `"@"`
still works. No other behavior change; every other `promptCloudInitYAMLFile`
call site was already correct.

---

