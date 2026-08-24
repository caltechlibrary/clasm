---
id: "0110"
title: "SSM-Capable Instance Profile Enforcement + Retrofit: three scoping decisions"
date: "2026-07-22"
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
uuid: "c2aa05f1-c46e-4105-8423-2fd52a3722a0"
origin_host: "MACMINI-RD.local"
---

**Context.** Live real-AWS verification of "Configurable EBS Root
Volume Size" (PLAN.md Phase 20.31) found that both test instances had
`IamInstanceProfile: null` -- no instance profile at all -- so
`growRootFilesystem`'s SSM-based OS-level growth automation could
never come online. Separately, the same day (and once before, setting
up an InvenioRDM test instance), there was no way to attach an IAM
instance profile to an instance already running, only at launch. Three
scoping decisions were needed before design work could start (see
DESIGN.md, "SSM-Capable Instance Profile Enforcement + Retrofit," for
the full design built on top of them).

**Decision 1: how to verify a role is "SSM-capable."** Check for AWS's
own managed policy, `AmazonSSMManagedInstanceCore`, attached via
`iam:ListAttachedRolePolicies` -- not an inline-policy content check.

**Rejected alternative.** *Parse inline policies
(`iam:ListRolePolicies`/`GetRolePolicy`) for functionally-equivalent
custom permissions* -- rejected: this means interpreting arbitrary IAM
policy JSON to decide whether it grants "enough" SSM access, which is
exactly the kind of guessing Phase 20.31's own "fail loud, don't
guess" convention (`growRootFilesystem`'s device/filesystem detection)
argues against. A role with a custom, non-managed-policy path to
equivalent permissions will be reported as not SSM-capable -- a known,
accepted limitation, not an oversight. clasm still never authors IAM
policies itself (DECISIONS.md, "2026-07-02 -- Support picking or
creating an IAM instance profile from within awsops"), so the fix for
a flagged role is an IAM-console change outside clasm, same boundary
as always.

**Decision 2: the retrofit workflow (associate/replace a profile on a
running instance) is general-purpose, not SSM-specific.** A new
"Associate/replace IAM instance profile" menu entry lets an operator
attach *any* instance profile to a running instance; SSM-capability is
shown but not gated there.

**Rejected alternative.** *A narrower, dedicated "enable SSM"
workflow* that only allows attaching an SSM-capable profile --
rejected because the incident that first surfaced this gap (setting up
an already-running InvenioRDM test instance) needed a profile for S3
access, not SSM at all. Gating the retrofit path to SSM-capable
profiles only would have left that exact original use case unsolved.

**Decision 3: launch-time enforcement checks every profile shown in
the picker, existing and newly-created, not just newly-created
ones.** `promptIAMInstanceProfileOrCreate`'s existing-profile list and
`createInstanceProfileForRole`'s role list both get SSM-capability
annotation/gating, and the `"(none)"` choice is removed entirely --
same posture as IMDSv2's `required` having no `optional` escape hatch.

**Rejected alternative.** *Only verify newly-created profiles*,
trusting whatever's already in the account -- rejected as inconsistent
with "insist on SSM support": an operator picking an existing,
non-SSM-capable profile from the list would hit exactly the same
silent-degradation problem Phase 20.31's live testing just surfaced,
just via a different picker branch.

**Consequences.** An instance profile is now mandatory at launch
(instance creation, cloud-init launch, launch templates all share the
same collection path, so all three gain enforcement together); an
operator without any SSM-capable role in their account is blocked at
launch until one exists, with no clasm-driven remediation path (by
design -- clasm doesn't author policies). The retrofit workflow adds a
new `EC2API` surface (`AssociateIamInstanceProfile`,
`ReplaceIamInstanceProfileAssociation`) and reuses
`promptIAMInstanceProfileOrCreate` rather than inventing a second
profile-picking UI.

---

