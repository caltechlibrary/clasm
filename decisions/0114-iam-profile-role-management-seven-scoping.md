---
id: "0114"
title: "IAM Profile & Role Management: seven scoping decisions, bundled into v0.0.5"
date: "2026-07-23"
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
uuid: "786b3b61-6648-44b2-8b1d-7b13e0a42201"
origin_host: "MACMINI-RD.local"
---

**Context.** The AWS Console makes two questions hard to answer quickly:
what roles/profiles/policies already exist and where they came from
(Caltech Library DLD's own, ones opened up by central IT for cross-team
tooling such as CrowdStrike, or AWS's own huge managed-policy catalog,
all interleaved in one flat list), and whether an existing one is safe to
reuse or a new one is actually needed. Seven scoping decisions were
needed before design work could start (see DESIGN.md, "IAM Profile &
Role Management Domain," for the full design built on top of them, and
`aim_management_and_support_proposal.md` for the paths considered and
rejected for each). Also decided the same day: this work is bundled into
the still-unreleased v0.0.5 rather than deferred to v0.0.6, holding back
the already-verified Phases 20.33-20.35 until IAM work is also done —
a deliberate trade-off, not an oversight.

**Decision 1: categorize origin via a new `Owner` tag, tag-based going
forward.** Fixed vocabulary (`DLD`/`CentralIT`/absent), matching the
existing `Project`/`Environment` convention style. clasm tags what it
creates from here on; it does not infer category from naming or
maintain a separate curated allow-list.

**Rejected alternatives.** *A curated allow-list in clasm config*
(name→category mapping maintained by hand) — works immediately for
legacy/central-IT resources that can't be tagged, but is a config file
that silently drifts as new resources appear. *Naming-convention
inference* — needs no new tagging, but depends on a consistent
account-wide convention that isn't confirmed to exist. *Hybrid
(tag+fallback list)* — covers both cases but is two mechanisms to keep
in sync, rejected as unnecessary complexity for a v0.0.5-scale first
pass.

**Decision 2: v0.0.5 adds real role/policy creation, reversing the
2026-07-02 "never creates a role" scope** (DECISIONS.md, "Support
picking or creating an IAM instance profile from within awsops") — via
curated per-use-case templates, scoped as parametrized statement sets
(operator supplies ARNs at creation time), not free-form policy
authoring.

**Rejected alternatives.** *Attach-only, no new policy JSON* — lower
risk, but doesn't solve "I need a new role and none of the existing
policies fit," which is exactly the gap the 2026-07-22 granian incident
exposed. *Defer to a later version* — would ship the discovery half
sooner, but the operator-facing problem ("is there a role I can use for
this new service") isn't solved by discovery alone.

**Decision 3: trust principal is EC2 only for now, modeled for
extension.** `TrustPrincipal` is a small enum/type from the start so
Lambda/ECS-task principals can be added later without reshaping the
creation flow.

**Rejected alternative.** *EC2 + Lambda now* — this team isn't making
heavy use of Lambda or ECS today; adding it now would be speculative
scope with no concrete use case yet.

**Decision 4: non-DLD-owned resources are read-only in clasm, always** —
enforced by clasm itself, independent of what the active AWS credentials
would technically permit.

**Rejected alternatives.** *Configurable per-category* (DLD editable,
CentralIT flagged read-only, AWS-managed always read-only) — same idea,
more moving parts, rejected as unneeded granularity for a first pass.
*Rely on IAM permissions only* — simplest, but removes a guardrail that
costs little to keep; a central-IT or AWS-managed resource should never
be one accidental menu selection away from modification by this tool.

**Decision 5: legacy/untagged DLD resources get a dedicated "Tag as
DLD-owned" action**, not a default-to-editable posture.

**Rejected alternatives.** *Untagged defaults to DLD-owned (editable)* —
safer for day-one usability, but weakens Decision 4's guardrail until
backfilling actually happens (an untagged central-IT resource would also
read as editable). *Accept the gap, backfill outside clasm* (AWS
CLI/Console) — no new clasm code, but moves the backfill step outside
the tool this whole effort is about.

**Decision 6: the five per-use-case policy templates are drafted from
scratch** (Static Website, RDM Repository, Bridge Service, Patron-Facing,
Data Processing), not sourced from existing policy documents — none were
available. The three thinnest (Bridge Service, Patron-Facing, Data
Processing) are accepted as v0.0.5 starting points, refined once real
usage surfaces what these services actually need, not held back until
they're fully scoped.

**Decision 7: IAM Policy is a full top-level browsable/taggable kind**,
symmetric with Role and Instance Profile — not secondary, viewed only
from inside a role's detail screen. The "what already exists" question
this whole effort opens with applies to policies just as much as to
roles.

**Rejected alternative.** *Policy as secondary, role-detail-only* — less
to build, but doesn't answer "what customer-managed policies exist
account-wide" as a standalone question, which was one of the two
motivating problems from the start.

**Consequences.** v0.0.5's release is held back until this design is
implemented, tested, and (where practical) real-AWS-verified alongside
Phases 20.33-20.35 — a real schedule cost, accepted deliberately.
`tagManagementKinds` grows from five entries to eight (`IAM Role`,
`IAM Instance Profile`, `IAM Policy` added), reusing Phase 20.30's
generalized `tagApplyFunc`-closure pattern rather than a new tagging
mechanism. A new fifth Domain Picker entry, IAM, is added alongside
Compute/Key Management/S3/Tag Management. clasm's IAM surface grows from
"pick or attach an existing role" to "create a role from a curated
template," a genuine scope expansion that needs its own test coverage
and (per this project's established practice) real-AWS verification
before release, not just unit tests.

---

