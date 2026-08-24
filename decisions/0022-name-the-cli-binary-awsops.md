---
id: "0022"
title: "Name the CLI binary `awsops`"
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
uuid: "e0b06e1a-e0ea-4554-951e-784b45760a74"
origin_host: "MACMINI-RD.local"
---

**Context.** The Go CLI had been referred to as `ec2-ami-manager`
throughout the Architecture sections, a name inherited from the original
Bash script's narrow scope. That name undersells what v1 now covers
(instance/AMI lifecycle, cloud-init inspection, backup hygiene, tag
management) and, worse, ties the tool's identity to a single operation.
The candidate `rdmctl` was also considered, but the user wants the name
to make two things clear: it's about AWS resource operational hygiene in
general, not explicitly tied to the Invenio RDM project specifically —
even though RDM instances are its primary use case today.

**Decision.** Name the CLI binary `awsops` (`cmd/awsops/`). The repository
itself stays named `awstools` (already fixed by the CMTools scaffold —
`codemeta.json`, `Makefile`'s `PROJECT = awstools`); the user may revisit
the repository name separately later, but that's not part of this
decision.

**Rationale.**
- Communicates general AWS operational hygiene rather than a single
  narrow operation (`ec2-ami-manager`) or a project-specific name
  (`rdmctl`) that would tie the tool's identity to Invenio RDM even though
  its mechanisms (tagging, backup archival, cloud-init inspection) aren't
  actually RDM-specific
- Leaves room for the tool to be useful for non-RDM AWS resources later
  without a confusing name

**Rejected alternatives.**
- *`rdmctl`* — clearer about the current primary use case, but locks the
  name to a project the tool isn't actually coupled to at the
  implementation level
- *`awstools` (reuse the repo name for the binary too)* — simplest, but
  the user prefers a distinct binary name in case the repo hosts more
  than one tool later

**Consequences.**
- `DESIGN.md`/`PLAN.md` Architecture sections and doc titles updated from
  `ec2-ami-manager` to `awsops` throughout
- `ec2_ami_manager.bash` (the Bash file itself) is unaffected — it's a
  filename, not the Go binary name, and stays as-is until retirement per
  the existing retire-after-verify plan

---

