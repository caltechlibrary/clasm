---
id: "0045"
title: "Validate key pair name against the AMI's region"
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
uuid: "8716933b-0057-4c3f-a9b6-8bba22303eb2"
origin_host: "MACMINI-RD.local"
---

**Context.** A real launch failed with AWS's own
`InvalidKeyPair.NotFound: The key pair 'etd-ami-test' does not exist`,
after every prompt in the flow had already been answered and confirmed.
The `-debug` log traced it to the picked AMI resolving to `us-west-1`
(a newly-surfaced official Ubuntu AMI region), while `etd-ami-test` only
exists as a key pair in `us-west-2` -- key pairs are per-region, and the
"Key pair name" prompt has always been unvalidated free text with no way
to know a typed name didn't exist in the target region until this
distant `RunInstances` failure. Narrowing configured regions (above)
reduces how often this specific pairing can occur, but doesn't fix the
underlying gap: two regions this team genuinely uses can still have
different key pairs.

**Decision.** "Key pair name" is now a pick list of key pairs that
actually exist in the AMI's region (`ec2:DescribeKeyPairs`), plus
"Create new key pair". Unlike Security group IDs/Subnet ID, there is no
"Other: type a name" escape hatch -- `ec2:DescribeKeyPairs` is a
complete, small, fully-enumerable list for a region (key pairs, unlike
AMIs or instance types, have no "public"/cross-account concept to escape
to), so a name it doesn't return is guaranteed not to work there. If the
region has zero key pairs, the list is just "Create new key pair" (with
a "No key pairs found in this region." note first) -- not a dead end,
and no ambiguous free-text guess is ever offered as the default path.
Falls back entirely to the original free-text prompt (with its "new"
keyword and the key-file-path auto-detection added earlier this session)
only if `ec2:DescribeKeyPairs` itself errors (e.g. missing permission) --
in which case there's nothing more reliable to offer than free text.

**Rationale.** Matches the pattern already established for Security
group IDs/Subnet ID (and, earlier the same session, the subnet-vs-
instance-type-AZ filtering): once a resource is region-scoped and can be
listed, offering a validated pick list instead of unvalidated free text
turns a distant, confusing AWS error into either a correct pick or an
explicit, guided "create one" step.

**Rejected alternatives.**
- *Validate the typed free-text name after the fact (check it exists,
  re-prompt if not), keeping free text as the primary input* -- rejected
  as strictly more code for the same outcome: a pick list validates by
  construction and additionally shows what's actually available, which
  a post-hoc check alone wouldn't.
- *Add an "Other" escape hatch matching Security group IDs' pattern* --
  rejected specifically for key pairs (see Decision above) since,
  unique among this tool's region-scoped pick lists, there is no
  legitimate case where a name outside `DescribeKeyPairs`' result could
  actually work.

**Consequences.**
- `internal/workflow/resource_lists.go`: `listKeyPairs`.
- `internal/workflow/create_key_pair.go`: `promptKeyPairNameOrCreate`
  rewritten to pick-list-or-create; original free-text logic preserved
  verbatim as `promptKeyPairNameFreeText`, now solely the list-error
  fallback.
- Every existing test exercising the full launch flow with a
  zero-key-pairs fake needed its key-pair input line updated from a bare
  typed name to "1) Create new key pair" + the name, since a bare fake
  with no configured key pairs now shows a 1-item pick list rather than
  accepting free text directly -- a wide but entirely mechanical ripple
  across `launch_instance_test.go`, `launch_from_cloud_init_test.go`,
  `create_instance_from_ami_test.go`, and
  `create_instance_from_cloud_init_test.go`.

---

