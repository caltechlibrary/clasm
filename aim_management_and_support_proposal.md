# IAM Profile & Role Management — Proposal (v0.0.5 candidate)

> **Status: proposal.** This document lays out a problem and the paths
> considered for addressing it in clasm. It is input to a future formal
> design -> plan -> decision -> test -> code pass (per CLAUDE.md), not a
> substitute for it — nothing here is committed scope. Where a path is
> marked "Decided" or "Leaning toward," that reflects a preference
> surfaced in discussion 2026-07-23, offered for review here alongside
> the alternatives, not a closed decision until it lands in DESIGN.md/
> DECISIONS.md/PLAN.md.

## Problem Statement

The AWS Console's IAM UI makes it hard to answer two questions this team
actually needs answered day to day:

1. **What already exists, and where did it come from?** Caltech Library
   DLD has its own roles/profiles, some roles were opened up by IMSS
   (Caltech's central IT organization — its security team is one
   component of IMSS, not a separate category) for cross-team tooling
   (e.g. CrowdStrike), and AWS itself ships a huge catalog of managed
   policies — today these are all interleaved in one flat list with no
   way to tell them apart at a glance, or to see what's been added
   recently.
2. **Is an existing role/profile appropriate to reuse, or do I need a new
   one?** Reusing the wrong role (over-broad permissions, wrong trust
   policy) is a real risk; authoring a new one by hand in the Console is
   slow and error-prone.

Functionally, DLD operates as its own independent group: from a
permissions standpoint, AWS-provided and IMSS-provided roles/profiles are
both equally out of scope for clasm to modify — the meaningful
distinction is DLD-owned vs. not, not a three-way split with different
treatment for IMSS vs. AWS.

clasm already has a narrow slice of IAM support (Phase 20.33: pick-or-create
an instance profile at launch, SSM-capability filtering, associate/replace
on a running instance). This proposal explores generalizing that into
full discovery, categorization, tagging, and — for a curated set of known
use cases — guided creation.

## Constraints / Non-Goals

- **clasm will never create, modify, or delete IAM *users*.** Whatever
  shape this takes, it's scoped to roles, instance profiles, and
  policies — the resources clasm needs in order to stand up
  infrastructure it manages.
- Not a general IAM Console replacement — like the rest of clasm, this
  should stay a curated, opinionated subset of IAM operations relevant to
  this team's infrastructure, not full parity with every IAM API.
- Cross-account roles, SCPs, and permission boundaries are treated as out
  of scope unless a concrete need surfaces.

## Motivating Use Cases

Four to five recurring infrastructure patterns this team stands up
repeatedly, each with a different natural IAM shape:

1. **Static content hosting** — open content published via S3 + CloudFront.
2. **RDM repository instances** — the existing Compute domain's primary
   use case (EC2, SSM, backup archival).
3. **Custom library services that bridge internal systems.**
4. **Custom patron-facing services.**
5. **Data processing services** — metadata extraction, creation,
   retrieval, and curation.

## Decision Points

Each of these was a genuine fork with real trade-offs. Paths not taken
are kept here rather than deleted, since they may become the right
answer if circumstances change.

### A. How to categorize a role/profile/policy's origin (DLD / IMSS / AWS)

| Path | How it works | Trade-off |
|---|---|---|
| General "Origin" tag, config-driven, no hardcoded vocabulary | A new tag clasm treats as a first-class, tool-recognized convention — joining `Project`/`Environment` (Feature 12) as a third one. Both the tag's *key name* and *which value means "DLD-owned"* are configurable (new `~/.clasm` config section, mirroring `regions`/`backup_directories`), left unset by default. | Nothing can be recognized as DLD-owned until the config is actually set — accepted, since the vocabulary itself is still pending feedback from the user's group after a demo |
| Curated allow-list in clasm config | Explicit name→category mapping maintained by hand | Works immediately for legacy/IMSS resources that can't or shouldn't be tagged, but is a config file that silently drifts out of date as new resources appear |
| Naming-convention inference | Infer category from existing name/path prefixes | Needs no new tagging or config, but only works if a consistent convention already exists account-wide (unconfirmed) |

**Decided:** a general, config-driven `Origin` tag, not an IAM-specific
mechanism and not a hardcoded `Owner=DLD/IMSS` enum. The tag's *presence
or absence* is itself informative — an untagged resource signals
"nobody's made a call on this yet," which is more useful than silently
defaulting it one way or the other. clasm reads/writes `Origin` exactly
like any other tag; Tag Management (Phase 20.37) already handles
arbitrary key/value pairs, so no new tag-entry mechanism is needed. What
is new is only that clasm's config can optionally name which *value* of
this tag means "DLD's" — deliberately left unset until the vocabulary is
actually decided.

### B. How much creation capability to add

| Path | How it works | Trade-off |
|---|---|---|
| Curated per-use-case templates | Built-in policy templates for the 5 use cases; user picks one, clasm creates the role + policy + attaches | Fastest path from "I need a role" to a working one, but requires drafting and maintaining least-privilege policy content for each use case |
| Attach-only, no new policy JSON | clasm creates the role/trust policy but only attaches existing managed/customer policies, never authors new ones | Lower risk (no new policy documents to get wrong), but doesn't solve "I need a new role and none of the existing policies fit" |
| Defer — discovery/attach-existing only | This release stays browse + attach; role/policy authoring deferred to a later version | Ships the (arguably higher-value) discovery half sooner, defers the part with the most design risk |

**Leaning toward:** curated per-use-case templates, but scoped as
**parametrized statement sets** (operator supplies specific ARNs — a
bucket name, a distribution ID, a secret name — at creation time) rather
than fully free-form policy authoring, keeping the risk closer to the
"attach-only" path's while still solving the "none of the existing
policies fit" gap. If the config's Origin-match value (from A) is set at
the time a template creates a role, clasm tags it `Origin=<that value>`
automatically; if unset, the new role is simply left untagged, same as
anything else clasm hasn't been told how to categorize yet.

### C. Trust principal scope for created roles

| Path | Trade-off |
|---|---|
| EC2 only | Matches clasm's current scope; doesn't cover a future serverless bridge/data-processing service |
| EC2 + Lambda now | Covers more ground immediately, but Lambda isn't in heavy use today — speculative scope |
| EC2 now, modeled for extension | Ships only what's needed today; requires the trust-principal representation to be a proper enum/type from the start rather than a hardcoded string, so Lambda/ECS can be added later without reshaping the creation flow |

**Leaning toward:** EC2 now, modeled for extension — this team isn't
making heavy use of Lambda or ECS today, but "bridge services" and "data
processing" are plausible future serverless candidates.

### D. Read-only enforcement for non-DLD-owned resources

| Path | Trade-off |
|---|---|
| Read-only always for non-DLD-owned, tagging exempted | clasm blocks policy-content mutations (attach/detach a managed policy, edit a trust policy, delete) on anything not recognized as DLD's, regardless of what the AWS credentials in use would technically allow — but tagging itself is always allowed, since DLD needs to record support-contact/provenance info on IMSS- and AWS-owned resources too | An extra guardrail independent of IAM itself, but one that doesn't get in the way of the actual reason DLD needs to touch these resources at all (recording who to contact) |
| Read-only for everything, including tags | Simpler rule (one gate, no exception) | Blocks the specific, stated need — recording support-contact info on resources DLD doesn't own — that motivated allowing any interaction with IMSS/AWS resources in the first place |
| Rely on IAM permissions only | No clasm-side restriction; if the credentials can modify it, clasm allows it | Simplest, but removes a safety net that costs little to keep |

**Decided:** read-only for policy-content mutations on anything not
recognized as DLD's, with **tagging explicitly exempted** from the
guard. A central-IT-adjacent (IMSS) or AWS-managed resource should never
be one accidental menu selection away from having its actual permissions
changed by this tool — but DLD must still be able to tag *any* role/
profile/policy (Origin, support contact, or anything else) regardless of
who owns it, since that's the concrete need that motivated touching
these resources in the first place.

### E. Handling legacy/untagged DLD resources under path D

**Decided: no dedicated backfill action.** Since `Origin` is an ordinary
tag (per A), setting it on a legacy DLD resource (e.g.
`ec2-granian-test-role`, roles behind current launch templates) is just
a normal Tag Management edit (Phase 20.37) — add `Origin=<value>` the
same way any other tag gets added. A dedicated one-click "tag as
DLD-owned" action was considered (see rejected alternative below) but
became redundant once Origin stopped being a fixed `Owner=DLD/CentralIT`
enum and became a general, freeform tag with no clasm-hardcoded values.

**Rejected alternative.** *A dedicated "Tag as DLD-owned" action* —
made sense when the tag scheme was assumed to be a fixed `Owner=DLD`
value clasm itself defined; once the actual vocabulary moved to "TBD,
decided by the user's group," a bespoke action for one specific,
not-yet-known value stopped making sense. General Tag Management already
covers "set any tag to any value" without needing a special case.

### F. Source of per-use-case template policy content

| Path | Trade-off |
|---|---|
| Draft strawman policies from scratch | Fastest to start, but based on descriptions of the use cases rather than verified existing configurations — needs review before trusting |
| Use existing policy documents as reference | More grounded, but requires those documents to be gathered and supplied first |
| Mixed | Some use cases (e.g. static site S3+CloudFront) may have precedent to draw on; others (e.g. data processing) likely don't |

**Leaning toward:** draft strawman policies from scratch, reviewed before
any implementation — see the draft table below. The three thinnest
templates (Bridge Service, Patron-Facing, Data Processing) are
acknowledged as starting points, not finished least-privilege policies,
since no existing reference documents were available for them.

### G. Should IAM Policy be a top-level browsable/taggable kind?

| Path | Trade-off |
|---|---|
| Top-level kind, symmetric with Role/Instance Profile | Consistent, but is one more list/tagging surface to build and maintain |
| Secondary — viewed only from inside a role's detail screen | Less to build, but doesn't answer "what customer-managed policies exist account-wide" as a standalone question |

**Leaning toward:** top-level kind, symmetric with Role and Instance
Profile — the discovery problem this proposal opens with ("what already
exists") applies to policies just as much as to roles.

### H. What the IAM domain's browse list shows for provenance

| Path | Trade-off |
|---|---|
| Show the Origin tag's actual value, or an explicit "(unset)" | Matches reality exactly — no guessing at a category clasm can't yet back up with real data; filterable via the existing List-tier filter ("/") on whatever values actually exist in the account |
| A fixed multi-way category (DLD/IMSS/AWS/Unknown) | Reads cleaner as a label, but requires clasm to guess at an IMSS-vs-AWS-managed distinction it has no reliable way to make before the vocabulary is settled |
| Collapse to binary editable/read-only | Simplest to scan, but throws away the "nobody's decided this yet" signal that makes an unset Origin tag useful to the group in the first place |

**Decided:** show the Origin tag's actual value (or "(unset)"),
filterable — not a fixed category enum, not collapsed to binary. This is
**IAM-domain-only for v0.0.5.** The same Origin column could extend to
the other five taggable kinds (instances, AMIs, launch templates, key
pairs, buckets) once the vocabulary's actually been proven out here and
reacted to by the group, but that's a deliberate later extension, not
part of this release — those resources don't have the same "is this
even ours to touch" ambiguity IAM roles do, so the need there is weaker
today.

## Draft Template Table (path B/F, needs review)

| Template | Draft least-privilege shape |
|---|---|
| **Static Website (S3 + CloudFront)** | `s3:GetObject`/`s3:ListBucket` on one bucket ARN; optionally `s3:PutObject`/`s3:DeleteObject` + `cloudfront:CreateInvalidation` scoped to one distribution ARN, if the role is for a publish process rather than read-only serving |
| **RDM Repository Instance** | `AmazonSSMManagedInstanceCore` (already enforced at launch, Phase 20.33) + scoped S3 read/write on one backup-bucket ARN — directly closes the gap that caused the 2026-07-22 granian-testing incident (a running instance needing S3 access with no instance-role option available, falling back to a plain IAM user access key) |
| **Bridge Service** (internal systems) | Baseline only: SSM + CloudWatch Logs. Too varied across actual services to template further; would need to be flagged in any picker UI as "starting point, review before use" |
| **Patron-Facing Service** | SSM + CloudWatch Logs + optional Secrets Manager read (`secretsmanager:GetSecretValue`) scoped to one secret ARN + optional S3 read on one bucket |
| **Data Processing** (metadata extraction/curation) | SSM + CloudWatch Logs + S3 read/write on one data-bucket ARN |

No additional AWS service (queues, topics, AI services, database IAM
auth) has been identified yet as commonly needed across today's actual
Bridge/Patron-Facing/Data Processing services — the thin baseline above
is offered as a starting point to refine once real usage surfaces what's
actually needed, not as a final scope.

## Shape of a Solution (if the decided/leaning-toward paths above hold)

- A 5th top-level domain, **IAM**, alongside Compute / Key Management /
  S3 / Tag Management, with three List-tier sub-views: Roles, Instance
  Profiles, Policies — each sortable by `CreateDate`, each row showing
  its `Origin` tag's actual value (or "(unset)") and, for roles,
  SSM-capability.
- A new, general config section (mirroring `regions`/
  `backup_directories`) naming the `Origin` tag's key (default
  `Origin`) and, once decided, which value means DLD-owned — left
  unset until the user's group has reacted to a demo of this feature.
  Not IAM-specific as a mechanism, even though only the IAM domain
  surfaces it in v0.0.5.
- A detail view per role/profile: trust policy, attached + inline
  policies, tags, SSM-capability, and a best-effort cross-reference to
  instance profiles/running instances currently using it.
- `tagManagementKinds` (currently `Instance`/`AMI`/`Launch Template`/
  `Key Pair`/`S3 Bucket`) gains `IAM Role`, `IAM Instance Profile`,
  `IAM Policy` — full add/update/remove/show-all-tags via the same
  generalized `tagApplyFunc`-closure pattern the S3 Bucket slice
  established (Phase 20.30). `Origin` is set/edited through this same
  general mechanism — no dedicated action.
- A read-only guard on policy-content mutations (attach/detach, edit
  trust policy, delete) for anything not recognized as DLD-owned via
  the Origin tag — tagging itself is exempt from this guard.
- A guided creation flow offering the five templates above, EC2 trust
  principal only, parametrized by ARNs the operator supplies.

## Open Items for the Design Pass

- Exact IAM API surface additions needed on `IAMAPI` (`TagRole`,
  `TagInstanceProfile`, `TagPolicy`, `ListRoleTags`,
  `ListInstanceProfileTags`, `ListPolicyTags`, `CreateRole`,
  `CreatePolicy`, `AttachRolePolicy`, `ListPolicies`, `GetRole`,
  `GetPolicy`/`GetPolicyVersion`, etc.)
- Whether `Origin` is a sensible default tag-key name to propose, given
  the actual vocabulary (key and values) is still pending group
  feedback — easy to change later since it's a config default, not
  hardcoded, but worth sanity-checking before it shows up in a demo
- Whether the three thin templates should visually warn differently from
  the two more fully-scoped ones (Static Website, RDM Repository) in the
  picker UI
- Whether policy documents are shown as raw JSON or a summarized/
  human-readable rendering in the detail view
- How deletion of a role/profile/policy should be gated (this project's
  existing `Environment=production` + type-to-confirm tier, or a new
  gate specific to IAM given the blast radius of a bad delete)
