---
id: "0040"
title: "Auto-detect a bare existing-file path in User data / Cloud-init YAML input"
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
uuid: "24f13261-de87-4ea4-b4f1-fe5e054b7464"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-world use: at "Cloud-init YAML (inline text or @file
path)", the operator typed `newt-machine.yaml` (a real file in the
current directory) without the required `@` prefix. `loadUserData`
correctly followed its documented contract -- no `@`, so treat it as
literal inline text -- and the flow moved straight on to picking a base
AMI, with the *filename itself* silently captured as the instance's
user-data. Nothing was technically wrong per the existing contract, but
the outcome (an instance launched with `newt-machine.yaml` as its literal
user-data, not the file's contents) is never what an operator actually
wants -- a bare filename is not valid cloud-init YAML or any other
sensible literal user-data.

**Decision.** `loadUserData` (shared by Features 2 and 3's User data /
Cloud-init YAML prompts) now checks, when given input with no `@`
prefix, whether a file actually exists at that exact path (relative to
the current directory, or absolute). If one does, it's loaded anyway,
with an on-screen note explaining what happened and reminding the
operator to prefix with `@` next time. If no such file exists, the
input is used as literal inline text exactly as before -- this is
additive, not a behavior change for genuine inline text (e.g.
`#cloud-config...`, which never coincides with a real file on disk).

**Rationale.** Same reasoning as the key-pair-filename fix (2026-07-02,
above): when a value can only plausibly be a mistake for a file
reference -- here, "this string is byte-for-byte the name of a real
file, and is not itself valid YAML" -- silently accepting it as literal
text produces a working-looking launch with silently wrong data, which
is worse than either rejecting it or (as chosen) just doing what the
operator almost certainly meant.

**Rejected alternatives.**
- *Require `@` strictly, reject anything else that looks like a bare
  filename* -- rejected: rejecting a value that unambiguously
  corresponds to a real, readable file just because of a missing prefix
  character is unhelpful friction for a case this tool can resolve with
  total confidence.
- *Warn but don't auto-load, forcing a re-prompt* -- rejected as an
  unnecessary extra round-trip when the file both exists and is
  immediately loadable; the printed note already tells the operator
  what happened and how to be explicit next time, without making them
  retype anything.

**Consequences.**
- `loadUserData`'s signature gained a `*termlib.Terminal` parameter (for
  the explanatory note); both call sites (`launch_instance.go`,
  `launch_from_cloud_init.go`) already had `t` in scope.
- No new AWS permissions or calls -- purely local filesystem/string
  handling around a value already being collected.

---

