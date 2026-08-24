---
id: "0016"
title: "Add Show/Export Cloud-Init as a v1 primitive"
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
uuid: "a4ebd711-c0f4-487f-8a53-83adadb7d1ab"
origin_host: "MACMINI-RD.local"
---

**Context.** This team maintains a separate repository,
`caltechlibrary/cloud-init-examples`, of hand-authored cloud-init YAML
templates (e.g. `invenio-rdm.yaml`) used both for local Multipass
development VMs and as the source of the `--user-data` passed when
launching real EC2 instances. A live check found that `ec2:
DescribeInstanceAttribute` (attribute `userData`) returns the exact
base64-encoded cloud-init that launched a given instance, and that
`newauthors`'s actual deployed cloud-init has already drifted from
`cloud-init-examples`' `invenio-rdm.yaml` template (missing packages, no
`write_files` onboarding scripts, a different `runcmd` approach). This is
a real, live instance of the "accurate test environments" risk this whole
project is meant to reduce.

**Decision.** Add "Show/export cloud-init" as a fifth v1 primitive
(alongside create-instance, create-AMI, remove-AMI, and the tagging
convention), not deferred:
- **Instance path**: `ec2:DescribeInstanceAttribute` — read-only, free,
  instant, works for any existing instance
- **AMI path**: also supported in v1. Since an AMI has no user-data
  attribute of its own, extraction launches a temporary, disposable
  instance from the AMI, waits for SSM to come online (reusing the same
  SSM pattern already planned for the fstrim step), runs an SSM command to
  read `/var/lib/cloud/instance/user-data.txt` off disk, and *always*
  terminates the temporary instance afterward — including on failure or
  timeout. This path costs real AWS time/money (a running instance for
  several minutes) and requires an explicit confirmation before
  proceeding, unlike every other read in this tool
- **Export**: decoded YAML can be saved to a local file path for manual
  diffing against a local clone of `cloud-init-examples`. No inline
  fetch-and-diff against the GitHub repo in v1 — see rejected alternatives

**Rationale.**
- Directly serves the stated project goal: this is the concrete mechanism
  for detecting drift between what's actually deployed and the team's
  canonical cloud-init templates
- The instance path is essentially free to build (one more typed SDK call,
  already-planned dependencies) — there's no reason to defer it
- The AMI path reuses the SSM client and "poll with bounded timeout,
  always clean up" pattern already needed elsewhere, rather than
  introducing a new mechanism (e.g. mounting the AMI's snapshot on a
  helper instance, which would need an existing helper instance in every
  region and is more moving parts for the same result)

**Rejected alternatives.**
- *Instances only, defer AMI extraction* — was the initial recommendation
  (cost/complexity), but the user explicitly wants AMI coverage in v1
  since some of the AMIs this team manages have already outlived the
  instance they were created from
- *Fetch + diff inline against `cloud-init-examples`* — would directly
  answer "has this drifted?" without leaving the CLI, but requires solving
  a non-trivial file-mapping problem first (the repo's files don't map
  1:1 to this account's `Project` tag values today, e.g. there's no
  `caltechauthors-init.yaml`) and adds a runtime network dependency on
  GitHub. Deferred — see `DESIGN.md`/`PLAN.md` "Deferred to a Later
  Version"
- *Extract via snapshot-mount on a helper instance instead of launch+SSM*
  — avoids booting the AMI's OS at all, but requires a pre-existing helper
  instance per region and more novel mechanics for the same outcome

**Consequences.**
- `DESIGN.md`'s IAM permission list gains `ec2:DescribeInstanceAttribute`,
  `ec2:TerminateInstances`, and `ssm:GetCommandInvocation` (SendCommand and
  DescribeInstanceInformation were already listed for the fstrim step)
- The AMI path needs its own explicit confirmation prompt (cost/time, not
  free like every other v1 read) and a cleanup guarantee — tests must
  verify the temporary instance is terminated even when the SSM
  command fails or times out
- `PLAN.md` gets a new Phase 6 ("Show/Export Cloud-Init"); Phases 6-10 in
  the prior draft are renumbered to 7-11 accordingly

---

