---
id: "0021"
title: "Enhance Create Instance from AMI: cloud-init file input + completion check"
date: "2026-07-01"
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
uuid: "95cb03eb-c8e0-4a4a-bd37-ddff6017a68b"
origin_host: "MACMINI-RD.local"
---

**Context.** Feature 2 (Create EC2 Instance from AMI) already had a
generic, optional "user data" text prompt — a cloud-init YAML could
technically go there today via freehand typing/pasting. Two real gaps
remained: no way to load it from a file instead of typing/pasting
multi-line YAML into a terminal, and no verification that cloud-init
actually *finished successfully* after launch — the existing poll only
waits for the EC2-level `running` state, not for cloud-init's own
completion. An instance can be "running" while its user-data provisioning
silently failed partway through, which directly undermines the "test new
versions/changes with confidence" goal this whole project is for.

**Decision.** Enhance Feature 2 with:
1. The user-data prompt accepts a local file path as an alternative to
   inline text (e.g. pointing at a file from a local clone of
   `cloud-init-examples`) — a plain local file read, no new AWS API
   surface
2. After the instance reaches `running`, if user-data was provided, wait
   for SSM to report `Online` and run `cloud-init status --wait` via SSM
   (bounded timeout — unlike Phase 5's unbounded AMI-creation poll, a
   cloud-init run on launch should finish in a bounded, predictable time,
   so an unbounded wait would just mask a real hang), reporting the
   actual completion status (`done` vs `error`) rather than only EC2's
   `running` state. If SSM never comes online, skip this check cleanly
   (not an error) — not every AMI has SSM configured

**Rationale.**
- File-path loading avoids re-typing/pasting multi-line YAML in a
  terminal prompt, at essentially zero implementation cost
- Verifying cloud-init's actual completion status closes a real
  "looks fine but isn't" gap — exactly the kind of silent failure that
  erodes confidence in a test environment
- Reuses Phase 6's SSM client/poll/bounded-timeout pattern rather than
  inventing a new mechanism

**Rejected alternatives.**
- *Fetch templates directly from `cloud-init-examples` via the GitHub
  API* — deferred for the same reason "Inline diff against
  cloud-init-examples" is deferred (see the Show/Export Cloud-Init
  decision): no clean mapping from this account's `Project` tags to the
  repo's filenames yet. Pointing at a local file (from your own clone)
  gets the practical benefit without that dependency
- *Unbounded wait for cloud-init completion* — rejected; unlike AMI
  creation, which can legitimately run for hours on large volumes, a
  launch-time cloud-init run should complete in a bounded, predictable
  window

**Consequences.**
- `DESIGN.md` Feature 2 is updated with both changes (still Feature 2, no
  renumbering)
- `PLAN.md` Phase 4 gains an explicit dependency on Phase 1 (for the SSM
  client, alongside the EC2 client it already needed) and new work items
  for file-path loading and the completion check

---

