---
id: "0031"
title: "Support picking or creating an IAM instance profile from within awsops"
date: "2026-07-02"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "099c05de-6eed-4181-94d4-7a2f4c8966f3"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing of Create EC2 Instance from AMI hit AWS's
own error: `AWS error [InvalidParameterValue]: Value (ec2-invenio-role)
for parameter iamInstanceProfile.name is invalid. Invalid IAM Instance
Profile name`. The "IAM instance profile" prompt was free text whose own
hint pointed at "IAM console > Roles" -- but `ec2:RunInstances`'
`IamInstanceProfile.Name` parameter needs the *instance profile* name,
not the *role* name. The two are identical by convention when a role is
created via the AWS console (which auto-creates a matching instance
profile), but not by requirement -- a role created via Terraform/CLI
without an accompanying instance profile of the same name breaks that
assumption silently, and free text let the mismatch through uncaught
until AWS rejected it. The user asked for "a means to picking a profile
(or creating one)."

**Decision.**
- The "IAM instance profile" prompt becomes a pick list of real instance
  profiles (`iam:ListInstanceProfiles`), each labeled with its attached
  role name(s) for clarity -- eliminating the role-name/profile-name
  mix-up at the source, since only real instance profile names are
  selectable.
- Unlike Security group IDs/Subnet ID's pick lists (which fall back to
  free text when the list is empty, since those fields are required and
  there's nothing else useful to offer), this field's list always
  includes a "(none)" entry (this field is optional) and a "Create new
  instance profile (attach an existing role)" entry, even when zero
  instance profiles currently exist -- because covering "I don't have
  one yet" is the whole point of the "or creating one" half of the
  request, not just a nice-to-have when profiles happen to already
  exist. The prompt falls back to the original free-text prompt only if
  the list call itself errors (e.g. missing `iam:ListInstanceProfiles`
  permission), matching the existing security-group/subnet fallback
  pattern for "can't reliably present anything better."
- "Create new instance profile" is scoped to **attaching an existing IAM
  role**, not also creating a new role: pick a role via `iam:ListRoles`,
  prompt for a new instance profile name (defaulting to the role's own
  name, matching the AWS console's own convention), then
  `iam:CreateInstanceProfile` + `iam:AddRoleToInstanceProfile`. A name
  collision re-prompts for a different name, mirroring Create Key Pair's
  collision handling (2026-07-01 decision, above). If there are zero IAM
  roles in the account, "Create new" prints an explanatory message and
  redisplays the instance-profile picker rather than failing outright.
- The success message notes that a newly created instance profile can
  take a few seconds to propagate before `ec2:RunInstances` will accept
  it (a well-known IAM eventual-consistency behavior) -- so a
  launch-time "instance profile not found" error right after creating
  one reads as "wait a moment and retry," not a new bug.

**Rationale.**
- Fixes the actual reported failure at its root: the field is now always
  populated (when non-blank) with a real instance profile name, not a
  free-text guess that might actually be a role name.
- Scoping "create" to attaching an existing role avoids a much bigger,
  genuinely separate design question -- what trust policy and what
  permissions a brand-new role should get by default -- which is a
  real security-relevant default this project shouldn't make silently.
  An operator who needs a new role can create it via the IAM console (or
  Terraform, matching how these roles are provisioned today) and then
  attach it here.

**Rejected alternatives.**
- *Also support creating a brand-new role* -- rejected for this round;
  raised as an explicit scope question and declined in favor of the
  simpler, no-new-security-defaults "attach an existing role" path.
- *Fall back to free text whenever the list is empty*, matching Security
  group IDs/Subnet ID exactly -- rejected because it would leave
  "creating one" unreachable in the (arguably common) case of a fresh
  account or a team that has never made an instance profile through this
  tool before, defeating the point of the feature request.

**Consequences.**
- New AWS SDK dependency: `github.com/aws/aws-sdk-go-v2/service/iam`.
- New IAM permissions required: `iam:ListInstanceProfiles`,
  `iam:ListRoles`, `iam:CreateInstanceProfile`,
  `iam:AddRoleToInstanceProfile` (see `DESIGN.md`, "Assumptions").
- `CollectLaunchInstanceParams`/`CollectLaunchInstanceParamsFromAMI`/
  `CreateInstanceFromAMI`/`CreateInstanceFromCloudInit` all gained an
  `awsclient.IAMAPI` parameter (a single global client, like STS/S3 --
  IAM is account-wide, not region-scoped, so it doesn't need the
  per-region client maps EC2/SSM use).
- `-debug`'s JSONL log now covers IAM calls too
  (`internal/awsclient/logging_iam.go`, `WrapIAM`), via the same shared
  generic logging helper the other wrappers use -- no special redaction
  needed here, unlike `CreateKeyPair`'s private-key material.

---

