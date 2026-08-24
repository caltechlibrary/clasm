---
id: "0041"
title: "Create EC2 Instance from Cloud-Init YAML always reads from a file"
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
uuid: "311e5c44-bef2-4179-91dc-b158fe305e0e"
origin_host: "MACMINI-RD.local"
---

**Context.** Immediately after fixing the bare-filename-without-"@" bug
(above) for the shared `loadUserData` path, follow-up feedback: for this
specific workflow, the "inline text or @file path" duality is itself the
wrong shape. Feature 3's whole premise is that the cloud-init YAML is
the primary input, not an optional add-on -- and a real cloud-init YAML
document is realistically always authored as a file (e.g. from
`cloud-init-examples`), never typed inline at a terminal prompt. The
`@`-prefix convention exists specifically to disambiguate inline text
from a file reference within one prompt; if inline text was never a
realistic input for this prompt in the first place, the convention (and
the exact mistake it enabled) doesn't need to exist here at all.

**Decision.** `CollectLaunchInstanceParamsFromCloudInit`'s cloud-init
prompt no longer shares `loadUserData` with Feature 2. It now calls a
dedicated `promptCloudInitYAMLFile`, which always treats the input as a
file path (an optional leading `@` is stripped if present, for muscle
memory, but not required) and re-prompts with a clear "cannot read"
message on a missing/unreadable file, instead of ever falling back to
using the raw input as literal text. Feature 2's separate, optional
"User data" field is unchanged -- it still supports genuine inline text
via `loadUserData`, since an ad hoc one-line script typed directly is a
realistic input there.

**Rationale.** Removes the entire failure mode (forgetting `@`) at its
root for this specific prompt, rather than just detecting and recovering
from the one shape of mistake found in real use (a bare filename that
happens to match a real file). A missing or unreadable file now fails
clearly and immediately, with a chance to retry, instead of either the
old silent-literal-text behavior or the newer auto-detection's narrower
"only if a file happens to exist at that exact string" coverage.

**Rejected alternatives.**
- *Keep sharing `loadUserData`, rely on the auto-detection fix alone* --
  rejected: that fix only helps when the mistyped value happens to
  match a real file; a typo'd filename (or a path relative to the wrong
  directory) would still silently become literal garbage user-data,
  since `loadUserData` has no way to know this particular prompt never
  wants inline text.
- *Also require file-only input for Feature 2's "User data" field* --
  out of scope for this decision: that field is optional and genuinely
  sometimes holds a short ad hoc script typed directly, unlike Feature
  3's mandatory, always-a-real-document cloud-init YAML.

**Consequences.**
- `internal/workflow/userdata.go`: new `promptCloudInitYAMLFile`.
- `launch_from_cloud_init.go` no longer calls `loadUserData` at all --
  only `launch_instance.go`'s optional "User data" field does now.
- Existing tests exercising this prompt with inline `"#cloud-config"`
  text were rewritten to use real temp-file fixtures
  (`writeCloudInitFixture` helper), since inline text is no longer a
  supported input here.

---

