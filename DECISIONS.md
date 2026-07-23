# AWS Tools — Architecture & UX Decision Log

This file records significant architectural and UX decisions for the interactive EC2/AMI manager, their rationale, and known trade-offs. New decisions are added at the top.

---

## 2026-07-23 — Phase 20.39 templates collect resource names/IDs, not ARNs

**Context.** Found via live usage while testing the Static Website
template: typing a full ARN (`arn:aws:s3:::my-bucket`) for an S3 bucket
forces a cognitive reformatting step the operator wouldn't otherwise
need -- they know the bucket *name*, not its ARN shape. Worse for
CloudFront: finding a distribution's ARN requires either the AWS
Console or a separate CLI lookup (`aws cloudfront list-distributions`),
breaking the interactive workflow entirely. Both are avoidable: clasm
already resolves the account ID once at startup
(`sts:GetCallerIdentity`, `awsclient.CheckCredentials`) and already
knows the region, which is all that's needed to construct these ARNs
from the operator's plain name/ID.

**Decision.** `IAMRoleTemplateParam` gains a `BuildARN(accountID,
region, value string) string` field. Each of the five templates'
params now prompts for a plain name/ID (an S3 bucket name, a CloudFront
distribution ID, a CloudWatch log group name, a Secrets Manager secret
name) instead of a full ARN; `createIAMRoleFromTemplate` calls
`BuildARN` (when set and the answer is non-blank) right after collecting
each answer, storing the *constructed* ARN under the same key
`BuildPolicy` already expects -- so the `BuildPolicy` functions
themselves (`staticWebsiteStatements`, `rdmRepositoryStatements`, etc.)
needed no changes at all, only the collection step and the prompts'
wording. `CreateIAMRoleFromTemplate`/`createIAMRoleFromTemplate` gained
`accountID, region string` parameters, threaded from `main.go`'s
existing `account` (from `CheckCredentials`, already resolved at
startup) and `cfg.Regions[0]`.

For Secrets Manager specifically, the constructed value is a *pattern*
with a trailing wildcard (`arn:...:secret:name-*`), not an exact ARN --
Secrets Manager appends a random 6-character suffix to every secret's
real ARN that the operator can't know in advance, so a trailing
wildcard is the standard, idiomatic way IAM policies scope access to a
secret by name.

**Rejected alternative.** *Keep collecting full ARNs, improve the
prompt wording only* -- rejected: the CloudFront case specifically
can't be fixed by better wording alone, since finding the ARN still
requires leaving the tool. Building the ARN from a shorter, more
memorable identifier removes the friction at its source rather than
just describing it better.

**Consequences.** CloudFront and Secrets Manager ARNs are global/
region-and-account-scoped in ways that don't always match `cfg.
Regions[0]` exactly (e.g. a secret could live in a different region than
the account's primary configured one) -- accepted as a reasonable
default for this phase, matching how the rest of clasm already treats
`cfg.Regions[0]` as the primary region for singleton, not-really-per-
region concerns. All five templates' `BuildPolicy` functions and their
existing unit tests were unaffected -- only `createIAMRoleFromTemplate`'s
tests changed, from typing literal ARN strings as prompt answers to
typing plain names, with an added assertion confirming the constructed
ARN appears correctly in the resulting policy document.

---

## 2026-07-23 — Parallelize per-resource IAM tag fetches

**Context.** Found via live usage while testing Phase 20.37 (Tag
Management's IAM extension): "Manage tags -> IAM Role" took several
seconds just to open the resource picker, in an account with dozens of
roles (several AWS-service-linked, e.g. SageMaker/CloudTrail roles).
Root cause: `ListIAMRoleSummaries`/`ListIAMInstanceProfileSummaries`/
`ListIAMPolicySummaries` (Phase 20.36) fetch each resource's tags one at
a time, sequentially, via `ListRoleTags`/`ListInstanceProfileTags`/
`ListPolicyTags` (required per-resource calls -- see "ListRoles/
ListInstanceProfiles/ListPolicies don't return tags inline," above) --
with N roles/profiles/policies, that's N sequential network round-trips
before any of Show Roles, Show Instance Profiles, Show Policies, Manage
Tags, or Show All Tags can render anything.

**Decision.** Parallelize the per-resource tag fetch with a bounded
worker pool (new `fetchTagsConcurrently` in `internal/inventory/iam.go`,
capped at `iamTagFetchConcurrency = 10` in flight at once), mirroring
`inventory.ListImages`' own concurrent per-region fan-out pattern
(`images.go`) -- generalized here to fan out over resource index rather
than region. All three `ListIAM*Summaries` functions now call this
shared helper instead of looping sequentially.

**Rejected alternative.** *Unbounded concurrency* (fire all N requests
at once, no cap) -- rejected: an account can plausibly have 100+ roles
between service-linked roles and Lambda/SageMaker-created ones (observed
directly in the account this was tested against), and firing that many
concurrent IAM API calls risks throttling. 10 in flight at once is a
conservative, not-tuned starting value -- worth revisiting if it proves
too slow or too aggressive in practice.

**Consequences.** Wall-clock time drops from roughly N x per-call
latency to roughly (N / 10) x per-call latency (plus the fixed cost of
the initial `ListRoles`/`ListInstanceProfiles`/`ListPolicies` call) --
not measured precisely, but expected to turn a multi-second delay into
something close to imperceptible for typical account sizes. Error
handling changes subtly: with sequential fetches, the *first* resource
(in `ListRoles`' own return order) to error would stop the loop
immediately; with concurrent fetches, the first error *observed* (not
necessarily the first resource in list order) is returned, only after
every in-flight fetch has completed -- behaviorally equivalent for
callers (an error is an error), but worth noting if debug-log call
ordering ever looks unexpected. All existing correctness tests
(Origin resolution, sorting, tag-map retention, error propagation) were
kept green through the refactor, and `go test -race` confirms no data
race was introduced.

---

## 2026-07-23 — Real bug: ListRoles/ListInstanceProfiles/ListPolicies don't return tags inline

**Context.** Found via live usage, not a test: the user tagged the real
`air-sampling` role `Origin=DLD` (values actually cased `origin`/`dld`,
matching their own `~/.clasm` `origin_tag` config exactly) from the AWS
Console, restarted clasm, and Show Roles still displayed `(unset)`.
Config loading itself was verified correct in isolation (a throwaway
`go run` confirmed `config.Load` resolved `OriginTag.Key="origin"`,
`OriginTag.DLDValue="dld"` from `~/.clasm` exactly as expected) --
ruling out the config layer entirely. The actual cause: Phase 20.36's
design (DESIGN.md, "IAM Profile & Role Management Domain") assumed
`iam:ListRoles`/`iam:ListInstanceProfiles`/`iam:ListPolicies` return each
resource's `Tags` inline, based on the vendored SDK's `Role`/
`InstanceProfile`/`Policy` response structs all declaring a `Tags []Tag`
field. Confirmed live against the real account that this assumption was
wrong for all three: `aws iam list-roles`, `aws iam
list-instance-profiles`, and `aws iam list-policies --scope Local` all
omit `Tags` entirely from their JSON output, even for resources
confirmed (via `aws iam list-role-tags`) to actually have tags. The SDK's
shared `Tags` field is populated by other operations (`GetRole`,
`CreateRole`, etc.), not these three List calls -- reusing one response
struct across multiple API operations doesn't mean every operation
populates every field.

**This is the same class of mistake this project has hit before**
(DECISIONS.md, "Offer official Ubuntu LTS AMIs..." -- getting AMI name
patterns wrong when not checked against real AWS) and the ARM64 addendum
explicitly checked live *because* of that history. This time the
equivalent live check wasn't done before writing the design -- worth
re-flagging as a standing lesson: an SDK type's field existing is not
evidence a *specific operation* populates it; when a design leans on a
list response including something beyond the obviously-basic fields
(here, tags), verify against a real, already-tagged resource before
writing the design, not just by reading vendored struct definitions.

**Decision.** Add a per-resource tag fetch after each list call: new
`IAMAPI` methods `ListRoleTags`, `ListInstanceProfileTags`,
`ListPolicyTags` (mirrored into `logging_iam.go`), called once per
role/profile/policy returned by `ListRoles`/`ListInstanceProfiles`/
`ListPolicies` respectively, inside `internal/inventory/iam.go`'s three
`ListIAM*Summaries` functions. Every existing "resolves Origin" test in
`internal/inventory/iam_test.go` was rewritten first to supply tags via
the fake's new per-name/per-ARN tag maps (matching the real API shape)
instead of via the list-response structs' `Tags` field, confirmed
failing against the pre-fix code, then the fix made them pass -- per
[[feedback-test-before-fix]].

**Rejected alternative.** *Keep relying on the list response's `Tags`
field* -- not actually an alternative, just the bug; there was never a
real code path where this could have worked, since AWS itself doesn't
return the data.

**Consequences.** Discovery for each of Roles/Instance Profiles/Policies
now costs N+1 IAM calls (one list + one tag-fetch per resource) instead
of 1 -- accepted, since IAM is a low-volume, non-rate-limited-in-practice
control-plane API for an account this size, and there's no way to get
per-resource tags in fewer calls via these APIs. `iamTagsToMap` and
`ResolveOrigin`/`IsDLDOwned` themselves needed no changes -- the bug was
entirely in what was fed to them, not in the tag-matching logic itself.

---

## 2026-07-23 — IAM Profile & Role Management: Origin tag revision (IMSS naming, no hardcoded vocabulary, tagging exempt from the read-only guard)

**Context.** Same-day follow-up to "IAM Profile & Role Management: seven
scoping decisions, bundled into v0.0.5" (below), after further
discussion. Three corrections surfaced: (1) "central IT" was the wrong
name -- Caltech's central IT organization is IMSS, and the security team
is one component *within* IMSS, not a separate category; (2) DLD
operates as an independent group, so functionally AWS-provided and
IMSS-provided resources get identical treatment (both "not DLD's"), not
a three-way split with different rules; (3) the actual tag vocabulary
(key name and which value means "DLD-owned") isn't decided yet -- it's
pending a demo of this feature and feedback from the user's group -- so
nothing about it should be hardcoded in clasm's source.

**Decision 1 (revises the prior entry's Decision 1): `Owner` with a
fixed `DLD`/`CentralIT` vocabulary is replaced by a general, config-driven
`Origin` tag.** Both the tag's key name and which value means "DLD-owned"
move to a new `origin_tag` config section (`~/.clasm`, mirroring
`regions`/`backup_directories`), left unset by default (see DESIGN.md,
"New Configuration: `origin_tag`"). Until the user's group settles on
real values and the config is updated, nothing is recognized as
DLD-owned via tag -- accepted, since guessing at a vocabulary that isn't
decided would just create rework once it is.

**Rejected alternative.** *Keep `Owner` as a fixed, clasm-hardcoded
enum* (the prior entry's original decision) -- rejected once it became
clear the actual vocabulary is still an open question for the user's
group, not something clasm should presume to answer for them.

**Decision 2 (revises the prior entry's Decision 4): the read-only guard
exempts tagging.** The guard still blocks anything that changes a role's/
profile's/policy's actual permissions (attach/detach a managed policy,
edit a trust policy, delete) on a resource not recognized as DLD-owned --
but tagging itself is never gated. DLD needs to record who to contact
for support on IMSS- and AWS-owned resources too; blocking that would
defeat the reason for touching those resources in the first place.

**Rejected alternative.** *Read-only for everything, including tags* --
simpler (one gate, no exception), but directly blocks the concrete,
stated need (support-contact recording) that motivated allowing any
interaction with non-DLD resources at all.

**Decision 3 (revises the prior entry's Decision 5): drop the dedicated
"Tag as DLD-owned" action.** It made sense when `Owner=DLD` was a
clasm-hardcoded value; once the vocabulary moved to "TBD, decided by the
user's group," a bespoke shortcut for one specific, not-yet-known value
no longer made sense. Setting `Origin` on a legacy resource is now just
an ordinary Tag Management edit (Phase 20.37) -- no special-cased action.

**Decision 4 (new): the browse list shows `Origin`'s literal value, or
an explicit "(unset)" -- not a fixed multi-way category, not collapsed to
a simple editable/read-only label.** An unset `Origin` tag is itself
useful information -- it tells the group "this one still needs a call
made on it" -- which a binary or pre-categorized display would hide.
Filterable via the existing List-tier filter ("/").

**Rejected alternatives.** *A fixed DLD/IMSS/AWS/Unknown category label*
-- rejected because clasm has no reliable way to distinguish IMSS from
AWS-managed before the vocabulary exists; the label would be a guess
dressed up as a category. *Collapse to binary editable/read-only* --
considered and initially favored earlier the same day, but reversed once
it was clear this throws away the "nobody's decided yet" signal that
makes an unset tag valuable to surface at all.

**Decision 5 (new): this is a general mechanism, but IAM-only in
display for v0.0.5.** `Origin` is designed as a tool-wide convention
(joining `Project`/`Environment`), not IAM-specific, so it costs little
extra to build generally now. But the column is only added to the IAM
domain's three list views this release -- not to the five existing
taggable kinds (instances, AMIs, launch templates, key pairs, buckets).

**Rejected alternative.** *Surface `Origin` everywhere immediately* --
rejected for this release: those five resource kinds don't share IAM's
"is this even ours to touch" ambiguity, so the need there is weaker, and
proving the convention out in one place before a group demo is safer
than rolling it out unproven everywhere at once.

**Consequences.** The IAM-domain implementation phases (PLAN.md Phases
20.36/20.37) gain a config-loading dependency (`internal/config` needs
an `OriginTag` struct with `Key`/`DLDValue` fields, both defaulting to
their documented values) that the prior pass hadn't scoped; the
previously-planned "Tag as DLD-owned" menu action is removed from Phase
20.36's work items, since Decision 3 makes it redundant with Phase
20.37's general tagging. No change to Decisions 2, 3, 6, 7 from the
prior entry (creation-capability scope, trust-principal scope, template
source, Policy-as-top-level-kind) -- those stand as originally recorded.

---

## 2026-07-23 — IAM Profile & Role Management: seven scoping decisions, bundled into v0.0.5

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

## 2026-07-22 — ARM64/Ubuntu 26.04: filter the instance-type list by AMI architecture, no new pre-flight check

**Context.** Adding arm64 (Graviton) support to the curated AMI and
instance-type lists raised the same question the IAM-profile picker
just answered: how to keep an operator from picking an
architecture-incompatible combination. The initial design proposed a
new pre-flight check mirroring `ensureInstanceTypeENACompatible` --
query, then offer "change instance type or abort" if the picked
instance type doesn't match the AMI's architecture.

**Decision.** Simplified: filter the instance-type picker's own choice
list by the already-picked AMI's architecture, the same approach just
adopted for the IAM-profile/role picker (see "Filter non-SSM-capable
profiles/roles from the picker, don't just annotate them," above) --
don't offer an instance type that would just be wrong, rather than
offering it and rejecting the pick afterward. `promptInstanceType`
gains an `arch string` parameter (`""` = no filter); the two top-level
launch-param collection functions pass the picked AMI's architecture,
the two ENA/AZ-incompatibility remediation call sites pass `""`
(unfiltered, matching their current behavior unchanged).

**Rejected alternative.** *A new architecture-compatibility pre-flight
check*, structurally cloning the ENA check -- rejected once the
IAM-profile picker's own live-testing feedback (same day) established
that filtering beats "show everything, reject on pick" whenever
there's no legitimate reason to show the invalid option in the first
place. There's no case where picking an arm64 instance type for an
x86_64 AMI (or vice versa) is ever valid, exactly the same shape of
argument that justified filtering there.

**Consequences.** Simpler than the rejected alternative: no new
`incompatibilityChoice` variant, no new remediation loop, no new tests
for a reject-then-retry flow. The two remediation call sites
(ENA/AZ "change instance type") deliberately stay unfiltered rather
than threading the AMI's architecture further through
`ensureInstanceTypeENACompatible`/the AZ check's own signatures --
accepted as a real but very unlikely gap (a non-ENA-required AMI old
enough to need that remediation path predates Graviton's existence in
practice).

---

## 2026-07-22 — Always gzip-compress user-data before base64-encoding it

**Context.** Live testing hit `InvalidUserData.Malformed: User data is
limited to 16384 bytes` creating a launch template from
`invenio-rdm-13-granian-init.yaml` (16976 bytes raw, already over the
limit before clasm even touches it -- `invenio-rdm-13-gunicorn-init.yaml`
is in the same boat at 16996 bytes). clasm currently just
base64-encodes the raw cloud-init text as-is at every write site
(`Launch`, `buildRequestLaunchTemplateData`,
`createLaunchTemplateVersion`) with no compression. cloud-init itself
auto-detects gzip-compressed user-data (checks the gzip magic bytes)
and transparently decompresses it before running -- a standard,
documented AWS/cloud-init pattern, not a hack. Gzipping the actual
16976-byte file (confirmed via plain `gzip -c | wc -c`, not assumed)
brings it to 5628 bytes, comfortably under the limit.

**Decision.** Two new shared helpers (`userdata_gzip.go`):
`encodeUserData(plainText string) string` gzip-compresses then
base64-encodes, used at all three write sites; `decodeUserData(encoded
string) (string, error)` base64-decodes then checks for the gzip magic
bytes (`0x1f 0x8b`) -- gunzips if present, returns the raw bytes
as-is otherwise -- used at all four read sites
(`ShowCloudInitFromInstance`, `syncLaunchTemplate`'s existing-version
read, `show_launch_template.go`'s two-version diff). The as-is fallback
is what keeps this backward compatible with every already-existing
instance/template whose user-data was written before this change, in
plain (non-gzip) form -- both old and new content read correctly
without needing to know which one a given resource has.

**Rejected alternative.** *Only gzip when the raw content is close to
or over the limit* -- rejected in favor of always gzipping: cloud-init
handles both forms identically, so there's no behavioral reason to
special-case small files, and a size-threshold decision is one more
thing to get wrong (and test) for no benefit. The only cost is a minor
readability regression for someone manually inspecting raw user-data
outside clasm (`aws ec2 describe-instance-attribute` returns gzip'd
bytes now, not readable YAML directly) -- accepted, since clasm's own
"Show/export cloud-init" already exists specifically to make this
readable again through the tool.

**Consequences.** `encodeUserData` has no error return -- gzip-writing
to an in-memory `bytes.Buffer` cannot fail in practice, so there's
nothing meaningful to propagate (avoids threading an error path for a
scenario that can't happen). `decodeUserData` does return an error
(malformed base64 or a corrupt/truncated gzip stream both remain
genuinely possible on read).

---

## 2026-07-22 — Filter non-SSM-capable profiles/roles from the picker, don't just annotate them

**Context.** Live testing of Phase 20.33 Part 2 (SSM-capable instance
profile enforcement): creating a launch template from cloud-init YAML
for the Granian test instance, the operator hit "Create new instance
profile" and was shown the account's full IAM role list, annotated per
role with SSM-capability -- but with real accounts holding many roles
for unrelated services (Lambda execution roles, service-linked roles,
...), a long annotated list that's mostly "cannot be selected" entries
was harder to use than no annotation at all, not easier. Since SSM
support is now a hard, unconditional requirement (no opt-out, same as
IMDSv2), there's no scenario where picking a non-capable entry is ever
valid -- showing it at all just adds noise to scan past.

**Decision.** `buildInstanceProfileChoices`/`buildRoleChoices`
(`create_instance_profile.go`) now filter out non-capable profiles/roles
entirely rather than including them with a `" -- NOT SSM-capable..."`
label suffix. The `ssmCapable` field and the post-pick rejection branch
in `promptIAMInstanceProfileOrCreate`/`createInstanceProfileInteractive`
are removed as dead code -- filtering guarantees every remaining choice
is already capable, so there's nothing left to reject. If filtering
empties the role list entirely, `createInstanceProfileInteractive`
reports it the same way it already reports "no roles at all in this
account" (a clear message, `created=false`, not an error) -- same shape,
new reason.

**Rejected alternative (superseded).** *Show every profile/role,
annotated, reject on selection* -- this session's original Part 2
design. Chosen at the time because DESIGN.md's own "Not decided yet"
note explicitly left "shown-but-blocked vs. ... " open; live usage
answered it: for a list of any real size, annotation-without-filtering
just makes the operator read past irrelevant entries to find the ones
that matter, providing no benefit over not showing them at all (there's
no "pick anyway" override to explain, since SSM support isn't
optional).

**Consequences.** Simpler code (fewer fields, fewer branches) and a
shorter, more usable list matching what the enforcement itself already
requires. If an account has zero SSM-capable roles, the operator now
sees a single clear "none found" message instead of a long list of
entries none of which can be selected.

---

## 2026-07-22 — SSM-Capable Instance Profile Enforcement + Retrofit: three scoping decisions

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

## 2026-07-22 — Widen "pause for acknowledgment" to every action, not just errors

**Context.** Live real-AWS testing of the same day's "Pause for
acknowledgment before every menu-loop redraw" fix (below) found a
third instance of the underlying bug, in a place that fix didn't
cover. `runLaunch` (`launch_execute.go`, shared by "Create EC2 instance
from AMI" and "from cloud-init YAML") calls `checkCloudInitCompletion`
after a launch; when cloud-init itself errors on the instance, that's
deliberately reported as a *result value*
(`CloudInitCheckResult{Status: "error"}`), not a Go error -- the
instance did launch successfully, so this isn't a workflow failure,
just a status worth telling the operator about. `runLaunch` prints
`"cloud-init reported an error -- check the instance before using
it."`, displays connection info, and returns **nil**. Because the
action succeeded (no error), the menu loop's dispatch takes the
success path, which -- after the first pass at this fix -- only paused
in the one specific case hand-patched into `resizeInstanceRootVolume`.
Live-tested: the operator saw the launch confirmation and connection
info flash by with no error and no pause, landing back on the menu
with no visible indication anything but a clean launch had happened.

This generalizes: any of the ~20 dispatched actions across all four
domains can print multi-line success-path status (launch
confirmations, connection info, "cloud-init completed successfully,"
etc.), and every one of those prints was still exposed to the same
redraw-wipes-the-screen problem this session's earlier fix only closed
for the error/refresh-error prints plus one hand-picked success case.
Hunting down and patching each individual success-path print site
one at a time (the way `resizeInstanceRootVolume` was patched) would
mean re-discovering this bug roughly once per action, the same
diminishing-returns pattern that already justified fixing all four
menu loops at once instead of just the one found live.

**Decision.** Add one new pause call, on the *success* path: right
after `choice.action(actions, ctx)` returns nil, before calling
`actions.Refresh(ctx)`, in all four domain menu loops. The pause must
still come *after* whatever text needs reading and *before* the next
redraw -- so this doesn't collapse into a single unconditional call
placed before the `err` check (that would pause before the error
text even prints, on the failure path). The two pauses from earlier
the same day stay exactly as they were: after the `"Error: ..."` print
(failure path) and after the `"Error refreshing listings: ..."` print.
Net result, three pause call sites per loop (was two): the action's
own output (success or failure) and the refresh error are each
guaranteed one pause before anything else redraws. The one-off pause
inside `resizeInstanceRootVolume` is removed as redundant now that the
loop itself always pauses after dispatching a successful action.

**Rejected alternatives.**
- *Patch `runLaunch`'s cloud-init-error branch specifically*, matching
  the original per-call-site approach -- rejected for the same reason
  the four-menu-loop audit was: it fixes the one instance found live
  and leaves every other action's own success-path prints (there are
  many, across four domains) equally exposed and un-discovered.

**Consequences.** Every single dispatched action now costs one extra
Enter keypress before the menu reappears, not just the ones that
error or explicitly opted into a pause -- a bigger UX tax than the
original narrower fix, accepted in exchange for closing the entire bug
class in one place instead of iteratively rediscovering it. Existing
per-domain `*_ActionErrorDoesNotCrashLoop`/`*_RefreshesAfterASuccessfulAction`/
`*_DispatchesToTheChosenAction`-style tests that dispatch more than one
action in sequence need a blank input line inserted after *every*
dispatch now, not just after the ones that errored.

---

## 2026-07-22 — Pause for acknowledgment before every menu-loop redraw

**Context.** Live real-AWS testing of Resize Instance's Root Volume
(PLAN.md Phase 20.31) found that its printed output -- the resize
confirmation, "Volume resize is usable," and (when SSM never comes
online, as it didn't for either test instance) `growRootFilesystem`'s
manual `growpart`/`resize2fs` fallback instructions -- flashed by too
fast to read. Root cause: `resizeInstanceRootVolume` returns
immediately after printing, and the Compute domain's menu loop
(`runMainMenu`, `menu.go`) redraws its full-height `huh.Select` on the
very next iteration, which (per `TUI_REFERENCE.md` §1) always repaints
the entire terminal -- exactly the "silent-scroll" bug class already
found and fixed once before, for Tag Management's "Show tags"
(PLAN.md Phase 20.29/20.30), just recurring at a new call site nobody
had exercised live until now.

Separately, in the same session, a live typo during instance cleanup
reproduced the identical symptom on the *error* path: `runMainMenu`
prints `"Error: %s\n"` after a failed action (or `"Error refreshing
listings: %s\n"` after a failed `Refresh`) and then loops straight
back into `pickMainMenuItem`'s full-height `Select` -- wiping the
error before it can be read. Auditing the other three domain menu
loops (`s3_menu.go`, `keymgmt_menu.go`, `tagmgmt_menu.go`) found the
exact same two print-then-redraw sites duplicated in each, verbatim --
this is a systemic gap in the shared menu-loop shape, not one
workflow's bug.

**Decision.** A single shared helper, `pauseForAcknowledgment`
(`menu.go`), blocks on a plain `ui.Prompt` ("Press Enter to continue")
until the operator explicitly dismisses it. Per `TUI_REFERENCE.md` §5,
plain prompts are deliberately content-sized, not full-height, so they
don't themselves wipe anything already on screen -- the same property
that makes them safe to insert between "something was printed" and
"the next full-height Select renders." Called **unconditionally**,
every time, at all of:
- both print sites (`"Error: ..."`, `"Error refreshing listings:
  ..."`) in all four domain menu loops (`menu.go`, `s3_menu.go`,
  `keymgmt_menu.go`, `tagmgmt_menu.go`) -- 8 call sites total
- the end of `resizeInstanceRootVolume` (`resize_volume.go`), after
  `growRootFilesystem` returns, whether or not automated growth
  succeeded

Unconditional rather than "only pause if something was printed":
simpler to implement and verify, and every one of the 9 call sites
above always prints something immediately beforehand anyway, so the
distinction is moot in practice.

**Rejected alternatives.**
- *Embed the message in the next Select's `Description`*, matching the
  Tag Management fix -- doesn't fit here. That fix worked because
  "Show tags" had one static, current-state snapshot to redisplay.
  These call sites print a *sequence* of status lines building up over
  time (resize progress, growth fallback instructions, an arbitrary
  error string) with no single "current state" to re-embed.
- *Fix only the two call sites found live* (resize's success path,
  the typo's error path) -- rejected once the audit showed the same
  two-print pattern duplicated identically across all four menu loops;
  fixing one and leaving the other three would just mean re-discovering
  this bug three more times, once per domain.

**Consequences.** Every dispatched action's error, every refresh
error, and Resize Instance's Root Volume's own output now require an
explicit Enter to dismiss before the menu reappears -- one extra
keypress per error/resize, in exchange for actually being able to read
what happened. `huh.Input`'s accessible-mode path
(`accessibility.PromptString`) never errors, even on EOF (it returns
the field's default value instead) -- confirmed by reading huh's own
vendored source rather than assumed, given this project's standing
"check vendored source, don't trust memory" rule for huh/bubbletea
behavior -- so the pause is safe to add inside an existing
accessible-mode-tested loop without risking the EOF-hangs-forever
`Select`/`PointerAccessor` gotcha from Phase 20.29. Existing pipe-driven
tests that continue a menu loop past an error (`*_ActionErrorDoesNotCrashLoop`,
one per domain) need one extra blank input line inserted between the
two picks, to account for the pause now consuming a line of input.

---

## 2026-07-21 — Root filesystem growth: detect-in-Go then act, not a self-detecting bash script

**Context.** Implementing Part 2 of "Configurable EBS root volume
size" (PLAN.md Phase 20.31): once `ec2:ModifyVolume` grows the EBS
volume itself, the OS-level partition and filesystem still need to
grow to use the extra space (`growpart` + `resize2fs`/`xfs_growfs`) --
the exact manual step the operator had to do by hand for the
production incident this phase closes. The open design question was
*where* the "is this a layout we can safely automate, or should we
back off" decision gets made: entirely inside one bash script sent via
SSM, or split into a detect step (bash) plus a decide step (Go).

**Decision.** Two separate SSM round-trips, both through the existing
`WaitForSSMOnline`/`RunShellCommand` primitives (`ssm.go`, already used
by `checkCloudInitCompletion` for the cloud-init-status check -- no new
SSM plumbing needed). First, `findmnt -no SOURCE,FSTYPE /` reports the
root partition's device path and filesystem type; its output is parsed
in Go (`splitDiskAndPartition`, `parseFindmntOutput`, `ssm_grow.go`),
not in bash. Only if that parse succeeds -- a single partition directly
on a whole disk, NVMe- or Xen/legacy-named, ext2/3/4 or xfs -- does a
second command actually run `growpart`/`resize2fs`/`xfs_growfs`.
Anything else (an LVM logical volume such as
`/dev/mapper/ubuntu--vg-ubuntu--lv`, a device-mapper node, an
unsupported filesystem) falls back to printing the same manual
commands the operator already ran by hand, rather than growing
anything. Rationale: PLAN.md's own work-item language for this phase
called for "fixture-driven unit tests for the `findmnt`-output-parsing
logic, independent of any live SSM round-trip" -- only achievable if
that parsing is a plain Go function, not logic buried inside a bash
string this project's tests can't introspect. It also keeps the
one genuinely destructive step (the actual `growpart` call) gated
behind Go-side validation of the parsed device, rather than trusting a
single monolithic script to both detect its own layout and correctly
bail out if it doesn't recognize it -- "fail loud, don't guess" applies
to the detection logic itself, not just its outcome.

**Bug caught by this phase's own tests.** `growRootFilesystem` and
`resizeInstanceRootVolume` initially hardcoded the package's production
`Default*` SSM timeouts (2 minutes online, 10 minutes per command)
directly, rather than taking them as parameters the way
`checkCloudInitCompletion` already does. The first test run of
`TestGrowRootFilesystem_SSMNotOnline_PrintsManualInstructions` actually
took 120 real seconds to pass -- it was genuinely waiting out the
production timeout, not simulating it. Fixed by threading
`onlineTimeout`/`commandTimeout`/`pollInterval` through
`growRootFilesystem` explicitly (production call site in
`resize_volume.go` passes the real `Default*` constants; tests pass
millisecond-scale ones), and by configuring
`resizeInstanceRootVolume`'s own end-to-end test's fake SSM client to
resolve immediately rather than shrinking the timeout further --
matching `checkCloudInitCompletion`'s existing shape, which this phase
should have followed from the start.

---

## 2026-07-21 — Sort the base-AMI pick list for deterministic ordering

**Context.** Building two launch templates for comparable systems back
to back, the operator noticed the Ubuntu LTS entries in the base-AMI
pick list (Feature 2/3, Create Launch Template from Cloud-Init YAML)
came up in a different order each run, making it easy to pick the
wrong release by muscle memory. Root cause: `inventory.ListImages`
aggregates owned AMIs across regions via concurrent per-region
goroutines feeding a channel, and `listOfficialUbuntuAMIs`
(`official_ubuntu_amis.go`) iterates `clients`, a Go map -- both orders
are randomized by the language, not by AWS, so the same AMI could land
in a different list position every time the picker opened.

**Decision.** `imagesWithOfficialUbuntu` (the shared function feeding
`pickImage` in every one of the four launch flows) now sorts the
combined list by Region then Name before returning it, using the same
`sort.Slice` approach `inventory.ListBuckets` already established for
the same class of problem (deterministic order after a concurrent/map
aggregation). No new dependency, no change to what's offered -- only
the order is now stable across runs.

---

## 2026-07-21 — Configurable EBS root volume size: scope, flow coverage, and resize automation depth

**Context.** TODO.md's "Bug (confirmed in production use, 2026-07-22)"
entry: a launch template built for a 250GB InvenioRDM comparison
instance instead produced an 8GB root volume (the stock Ubuntu 24.04
AMI default), because neither `RunInstances` (`launch_execute.go`) nor
`CreateLaunchTemplate` (`launch_template_create.go`) has ever set
`BlockDeviceMappings` -- the Launch Templates addendum
(DESIGN.md, 2026-07-20) explicitly deferred the entire block-device-
mapping surface as out of scope for the curated field set. This
reopens that specific piece of it. Three scope questions were put to
the operator directly rather than assumed, since each has a real
implementation-cost/flexibility trade-off:

**Decision 1 -- root volume size only, not a general block-device-
mapping editor.** The confirmed real need is "the default is too
small, I sometimes need 250-500GB," not additional data volumes or
per-volume type/IOPS control. Matches the project's existing curated-
field-set restraint (Launch Templates addendum) rather than reopening
the full AWS struct.

**Decision 2 -- every instance-creation flow and template creation, not
just templates.** `collectLaunchInstanceParams` and
`collectLaunchInstanceParamsFromCloudInit` are the two shared
parameter-collection cores behind Feature 2 (AMI), Feature 3
(cloud-init), and Create Launch Template from Cloud-Init YAML (which
already reuses the cloud-init core, per DESIGN.md's Launch Templates
addendum) -- one change to each core covers all three creation paths
in one pass, rather than fixing only the path that happened to trigger
the production incident and leaving the other two with the same latent
gap. Create EC2 Instance from Launch Template stays untouched: the
template already bakes in its own size, and this project already
resolved (DESIGN.md, "Launch Templates," decision A3) that this path is
"just another way to create an instance," not a hybrid template-plus-
override wizard -- reopening that here would undo a settled decision.

**Decision 3 -- automate the OS-side growth via SSM, not just the AWS-
side `ModifyVolume` call.** The operator's own real-world workaround
for the production incident was exactly `aws ec2 modify-volume` +
manual `growpart`/`resize2fs` over SSH -- automating both halves closes
the gap the same way the operator already closed it by hand, rather
than leaving half the workaround still manual. Accepted trade-off: this
is clasm's first workflow that executes shell commands inside a live
instance's OS (every prior use of `SSMAPI.SendCommand` only *checks*
cloud-init status, never changes instance state), so the design commits
to "detect a supported single-partition layout and act, or abort with
the same manual instructions rather than guess" for any layout the
`findmnt`/`lsblk` probe doesn't recognize (e.g. LVM) -- see DESIGN.md,
"Configurable EBS Root Volume Size," Part 2.

---

## 2026-07-20 — Generalize applyOneTagChange for S3's read-modify-write tag semantics

**Context.** With the four EC2-backed kinds of Tag Management done and
confirmed against real AWS, S3 Bucket was the one remaining kind (see
DECISIONS.md, "Tag Management: a fourth domain...", which anticipated
this exact generalization but deliberately deferred it until a real
second apply-shape existed -- PLAN.md Phase 20.30's "Design note").
`s3:PutBucketTagging` replaces a bucket's *entire* tag set and has no
fine-grained add/remove-one-tag call the way EC2's
`CreateTags`/`DeleteTags` do, so S3 needed a genuinely different apply
path, not just new fetch/wiring like Launch Template/Key Pair did.

**Decision.** `applyOneTagChange`/`manageTagsForResource`
(`manage_tags.go`) now take a `tagApplyFunc` (`func(ctx
context.Context, params TagChangeParams) error`) instead of a
hardcoded `awsclient.EC2API` client. Every existing call site
(Compute's `manageTags`, and `manageResourceTags`'s four EC2-backed
cases) builds `func(ctx, params) error { return ApplyTagChange(ctx,
client, params) }` once, at the point `client` is already resolved --
mechanically identical behavior, just the client wrapped in a closure
instead of passed raw. New `internal/workflow/bucket_tags.go` adds the
S3 side: `fetchBucketTags` (a full-tag-set `GetBucketTagging`, treating
`NoSuchTagSet` as empty, same convention as `bucketPurpose`) and
`applyBucketTagChange` (the S3 `tagApplyFunc`) -- fetch current tags,
apply the one collected Add/Update/Remove locally, then write the
whole set back. If that leaves zero tags (removing the bucket's last
one), it calls the newly-added `s3:DeleteBucketTagging` instead of
`PutBucketTagging` with an empty `TagSet` -- proactively matching
`ManageBucketLifecyclePolicies`' own `DeleteBucketLifecycle` precedent
for the same "replace the whole set" operation shape (real-AWS
verification there found `PutBucketLifecycleConfiguration` rejects an
empty `Rules` list client-side). Checked (not assumed) whether the same
applies to `PutBucketTaggingInput`: the SDK's generated
`validateOpPutBucketTaggingInput`/`validateTagging` only require
`TagSet` to be non-nil, not non-empty, so an empty-but-non-nil
`PutBucketTagging` call might in fact succeed client-side (and
possibly server-side too) -- `DeleteBucketTagging` was still chosen
out of caution, matching the established precedent, since this hasn't
been confirmed against real AWS either way yet.

**Rationale.** Generalizing only when a real second apply-shape
appeared (S3) rather than up front (when the EC2-backed slice was
built) avoided speculative complexity in Phase 20.30's first slice --
every EC2-backed call site needed no behavior change at all, just a
closure wrapper, confirming the deferral was the right call. Rejecting
`PutBucketTagging` with an empty set (in favor of
`DeleteBucketTagging`) even though the client-side validator would
accept it errs toward the same caution the lifecycle-rules case
already established, rather than assuming AWS's server-side behavior
matches what the client-side SDK validator merely permits.

**Rejected alternatives.**
- *A second, parallel tag-editing workflow just for S3* -- rejected:
  would duplicate `manageTagsForResource`'s entire loop/action-picker/
  confirm/Show-tags shape for no reason, the exact outcome the
  pluggable-apply-closure generalization was designed to avoid.
- *Skip `DeleteBucketTagging` and always call `PutBucketTagging`,
  since the client-side validator accepts an empty `TagSet`* --
  rejected: client-side acceptance doesn't confirm server-side
  acceptance, and the lifecycle-rules case already showed AWS can
  reject an empty "whole set" write that the SDK itself doesn't block
  locally; safer to match that precedent than assume this operation
  differs, pending real-AWS confirmation.

**Consequences.** `manage_tags_test.go` gained a small `ec2Apply(client)`
test helper so existing direct calls to
`applyOneTagChange`/`manageTagsForResource` keep working unchanged.
New `statefulTagsFakeS3Client` (`bucket_tags_test.go`, mirroring
`statefulTagsFakeEC2Client`) proves the S3 read-modify-write round
trip and the `DeleteBucketTagging`-on-empty branch specifically.
`awsclient.S3API` gained `DeleteBucketTagging` (+ logging wrapper +
shared `fakeS3Client` method). See PLAN.md Phase 20.30, "Work Items
(S3 Bucket)".

---

## 2026-07-20 — Manage Tags: embed current tags in the action Select's own Description, not just a separate print above it

**Context.** Found via real-terminal testing of the new Tag Management
domain (Phase 20.30): confirmed Add/Update/Remove all worked correctly
for both launch templates and instances, but choosing "Show tags" from
the action menu appeared to do nothing -- the screen looked unchanged.
Confirmed directly with the operator (not assumed): the screen, not the
underlying data, was the problem.

**Decision.** `manageTagsForResource`'s loop (Phase 20.29) already
re-displays current tags via a plain `displayTags(w, ...)` print at the
top of every iteration, immediately followed by the "Choose an action"
huh.Select. Root cause: that Select is a Menu-tier field, pinned to the
full terminal height on every render (DESIGN.md, "Full-height Menu
Tier", Phase 20.26) -- so the instant it renders, it fills the entire
visible terminal and scrolls whatever was printed just before it (here,
displayTags' output) out of view. The data was always current; the
*screen* just never showed it by the time the operator could read it.
Fixed by adding `actionMenuDescription(label, tags)` and passing it as
the Select's own `Description` -- embedding the same tag listing inside
the full-height chrome that's guaranteed to be on screen, instead of
relying on separately-scrolled-away plain output. `displayTags` itself
is kept alongside it, unchanged: huh.Select's accessible-mode
`RunAccessible` only ever prints a field's Title and options, never its
Description (confirmed by reading huh v1.0.0's `field_select.go`), so
every existing accessible-mode test asserting tag content in output
still depends on `displayTags`' plain print and needed no changes.

**Rationale.** This is the same class of bug the Full-height Menu Tier
work (Phase 20.26) already flagged as a risk in principle -- a
full-height render can silently evict whatever was on screen just
before it -- but this specific instance wasn't caught until real
interactive use, since `manage_tags_test.go`'s coverage only asserts
buffer *content*, not what remains visibly on screen after a
full-height render. No other Menu-tier call site in this package prints
data immediately before a full-height Select the way this one does, so
this fix is scoped to `manageTagsForResource` rather than a broader
change to `runMenuField`/`quitKeyGuard`.

**Rejected alternatives.**
- *Shrink the action Select below full terminal height so the printed
  tags stay visible above it* -- rejected: reverses Phase 20.26's own
  full-height decision for one call site, reintroducing the
  inconsistent-compact-submenu problem that phase deliberately fixed.
- *Remove "Show tags" as a menu choice, since the loop already
  redisplays tags on every iteration regardless* -- rejected: the
  redisplay-on-every-iteration behavior is exactly what was invisible;
  removing the choice wouldn't fix the underlying visibility bug for
  the post-Add/Update/Remove redisplay either.

**Consequences.** `actionMenuDescription` (`manage_tags.go`), tested
directly (`TestActionMenuDescription_ListsCurrentTags`/`_NoTags`).
Applies uniformly to both Compute's "Manage tags for an instance or
AMI" (Phase 20.29) and Tag Management's "Manage tags" (Phase 20.30),
since both call the same `manageTagsForResource`.

---

## 2026-07-20 — Tag Management: a fourth domain, generalizing the Manage Tags loop across five resource types

**Context.** Requested directly (`notes-from-tom.txt`, TODO.md: "a top
level menu item for managing tags across resources (EC2, AMI, S3,
etc)"), explicitly alongside keeping the existing per-resource entry
points ("continue to support tag management at the point we are
working with individual resources"). Phase 20.29 (Manage Tags: loop
until 'q', Show tags choice, refresh-after-change) is unaffected and
stays scoped to Compute's Instance/AMI entry point.

**Decision.**
- A fourth `DomainActions`/`domainItems` entry, "Tag Management,"
  alongside Compute/Key Management/S3 -- not a menu item nested in any
  one of them, since it's the only screen that needs to reach across
  all three existing domains' resources. Runs its own `refresh`
  (fetching all five taggable resource types, across all regions) on
  every entry, matching the other three domains' convention exactly.
- **Five resource types in v1:** EC2 Instance, AMI, Launch Template,
  Key Pair, S3 Bucket -- confirmed against the actual AWS APIs, not
  assumed: the first four via the generic `ec2:CreateTags`/`DeleteTags`
  (already working for Instance/AMI; new wiring for Launch
  Template/Key Pair, though the API itself is identical); S3 Bucket via
  the different `s3:GetBucketTagging`/`PutBucketTagging` shape.
- **Launch template tags target the template resource's own tags**
  (live, no new version needed) -- not the `TagSpecifications` baked
  into a version's `UserData` for instances launched from it, which is
  a version-creation concept Sync already covers (Phase 20.27/20.28).
- **S3 bucket Add/Update/Remove is a transparent read-modify-write**:
  fetch the bucket's current full tag set (`GetBucketTagging`), change
  one entry, `PutBucketTagging` the whole set back -- necessary because
  `PutBucketTagging` replaces the entire set, it has no fine-grained
  add/remove-one-tag call the way EC2's `CreateTags`/`DeleteTags` do.
  The operator still experiences "add/update/remove one tag," same as
  every other resource type; the read-modify-write is invisible.
  Accepted risk: a concurrent external change to the bucket's tags
  could be silently overwritten, consistent with this tool not doing
  concurrency control anywhere else either.
- **Key pair tags are new ground, not just new wiring**: confirmed
  `types.KeyPairInfo` has its own `Tags` field and the generic EC2
  tagging API applies, but clasm has never fetched, displayed, or set a
  key pair's tags before. Scoped to add/update/remove only for this
  phase -- extending Key Management's existing "Show resource lists"
  display with Project/Environment columns (matching Instance/Image's
  own convention) is a separate, smaller follow-on, not bundled in.
- **`applyOneTagChange` (Phase 20.29) is generalized to take a
  pluggable *apply* closure**, alongside the `fetchTags` closure it
  already takes -- currently hardcoded to `ApplyTagChange`'s EC2-only
  `CreateTags`/`DeleteTags` calls. The same loop/action-picker/confirm/
  Show-tags-choice UI then serves all five resource types uniformly;
  only the fetch/apply closures differ per kind, avoiding a second,
  parallel tag-editing UI just for S3.
- **"Show all tags" is scoped to one resource type at a time**, not one
  combined table across all five: pick a resource type (same picker as
  editing), then a List-tier table of every resource of that type with
  a flattened "Tags" column (every key=value pair, not just
  Project/Environment) -- the same shape as Compute's existing "Show
  instances/AMIs/launch templates" listings. Deliberately not one table
  spanning all five types: they don't share a natural row shape, and
  tag *key sets* vary per resource regardless, so fixed columns don't
  work either way -- five type-scoped listings read better than one
  forced-together table. Costs no new AWS call for the four EC2-backed
  types (their existing list calls already return full tags inline,
  currently decoded down to Project/Environment only); for S3 it's one
  `GetBucketTagging` call per bucket, generalizing `bucketPurpose`'s
  existing single-tag-filtered pattern.

**Rationale.**
- Reusing Phase 20.29's loop (rather than a new UI) is the direct
  payoff of having just built it as a standalone, generalizable
  function -- the alternative (a bespoke S3-tag-editing screen) would
  duplicate the entire action-picker/confirm/loop shape for no reason.
- Scoping "Show all tags" per-type sidesteps the cross-API-shape
  problem entirely (raised directly: "not sure how to do this across
  the different resource types if they use different API calls") by
  never needing a single call/response shape that covers all five --
  each type's own listing uses whatever API that type already needs.

**Rejected alternatives.**
- *Nest a tag-management menu item inside each existing domain*
  (Compute gets Instance/AMI/Launch Template, S3 gets Bucket, etc.) --
  considered opposite the "top level" framing of the actual ask, and
  would leave no single place that spans everything; also awkward for
  Key Management, which would need a menu entry for a resource type
  (buckets) it has nothing to do with.
- *One combined "all tags, all resource types" table* -- rejected for
  "Show all tags" specifically: no natural shared row shape, and
  arbitrary tag key sets make fixed columns unworkable regardless of
  how many resource types are forced into one table.
- *A compliance/audit report (which resources are missing tags
  entirely, or missing Project/Environment specifically)* -- raised as
  a likely future ask, explicitly deferred (TODO.md, someday/maybe): a
  different query shape than "Show all tags" (which shows what each
  resource *has*, not what it lacks), needing its own design pass (does
  "missing" mean zero tags, or missing the two convention tags
  specifically?).

**Consequences.** New `domainItems` entry + `TagMgmtActions` (or
similar) bundle; `applyOneTagChange` gains a pluggable apply closure
alongside its existing fetch closure; new `pickKeyPair` (Picker tier,
matching `pickInstance`/`pickImage`/`pickLaunchTemplate`); S3 bucket
tag read-modify-write helpers; a per-type "Show all tags" List-tier
listing, decoding full tag maps (`tagsToMap`, already used by
`fetchInstanceTags`/`fetchImageTags`) rather than the
Project/Environment-only fields the existing inventory structs
currently expose. See `PLAN.md` Phase 20.30.

---

## 2026-07-20 — Manage Tags: loop until 'q', always show current tags, add a Show tags choice

**Context.** TODO.md bug, reported directly: Manage Tags is missing a
"show tags" menu option, and "the tags shown at the top of the screen
don't update on change." The existing flow displayed tags once, applied
exactly one Add/Update/Remove, and exited -- so a second look at the
same resource's tags required leaving and re-entering the whole
workflow, and there was no way to just look without also being forced
into picking Add/Update/Remove.

**Decision.** `manageTagsForResource` becomes a loop: display current
tags (freshly fetched, not the original snapshot) -> pick an action
(now four: Show tags/Add/Update/Remove) -> act -> loop, until the
operator cancels. "Show tags" is deliberately close to a no-op (tags
are already re-shown every iteration) -- it exists because the operator
asked for it by name, not because the display was otherwise hidden.
The single-change logic (`applyOneTagChange`) is extracted from the
loop so it stays directly unit-testable on its own.

**Rationale.**
- Looping until 'q' matches this codebase's own established convention
  for action menus (`RunMainMenu`, `RunS3Menu`, `RunKeyMgmtMenu` all
  loop the same way) rather than introducing a one-off exception for
  Manage Tags.
- Re-fetching tags after every change (not reusing the pre-change
  snapshot) is the actual content of the bug fix -- looping alone
  wouldn't have helped if the redisplayed data was stale.

**A real, non-obvious finding worth recording.** huh's own
accessible-mode `Select` (used throughout this package's tests) cannot
signal "the input pipe is exhausted" as an error. Confirmed by reading
`internal/accessibility.PromptString` (huh v1.0.0) directly, not
assumed: on `scanner.Scan()` returning false, it silently falls back to
the field's default value and returns nil -- the package's own comment
there says as much ("no way to bubble up errors or signal
cancellation... but the program is probably not continuing if stdin
sent EOF"), an assumption that doesn't hold for a *looping* workflow
re-entering the same accessible-mode prompt more than once. A first
attempt at this loop relied on that exhaustion to end a test (matching
this package's usual `cancelledIsNil`/`io.EOF` convention) and instead
spun forever, silently re-selecting "Show tags" (option 1, the
resulting default) and reconstructing a `huh.Form` on every iteration --
caught via `go test -timeout` plus a goroutine dump showing the loop
"runnable" (CPU-bound in form construction), not blocked on I/O, not
assumed from reading the code alone.

**Fix for the above:** a `ctx.Err()` check at the top of the loop,
matching `runMainMenu`'s own convention in `menu.go` -- and, unlike that
precedent, actually load-bearing here rather than just stylistic
consistency. Tests cancel `ctx` explicitly at the exact point they want
the loop to end (`cancelAfterNFetches`, adapting `menu_test.go`'s own
`cancelingAction` pattern to trigger from a data-fetch closure instead
of a dispatched menu action), rather than relying on scripted input
running out.

**Rejected alternatives.**
- *Keep the one-shot behavior, just refresh tags before displaying them
  next time the operator re-enters Manage Tags* -- technically closes
  the letter of the bug (a fresh entry would show current data) but not
  the spirit of it: the operator's own report describes wanting to see
  the result without leaving the screen.
- *Rely on exhausted test input to end the loop* -- the initial
  approach; abandoned once shown to hang indefinitely rather than
  error, per the finding above.

**Consequences.** `manageTags` now builds and threads a per-kind
`fetchTags` closure into `manageTagsForResource`. `isCancellation`
extracted from `cancelledIsNil`'s existing check and widened to include
`io.EOF`, matching `isExitSignal`'s (menu.go) already-broader
definition -- a small, harmless generalization, though not what
actually fixed the hang (the ctx.Err() check did). New
`statefulTagsFakeEC2Client` (manage_tags_test.go) -- unlike the shared
`fakeEC2Client`, it actually tracks tag state across `CreateTags`/
`DescribeInstances` calls, needed to prove the refresh-after-change
behavior in a test. See `PLAN.md` Phase 20.29.

---

## 2026-07-20 — Launch Template version history, scrollable diffs, and split Show resource lists

**Context.** First real-AWS pass over Phase 20.27's launch template
support surfaced three UX gaps, all from actual use rather than design
review: (1) Show Launch Template only reports "there's another
version," not what changed in it; (2) Sync's confirmation diff is a
raw `fmt.Fprintln` dump that can scroll off screen with no way to page
back through it; (3) Compute's "Show resource lists" pages through
Instances -> AMIs -> Launch Templates as one combined flow, which felt
awkward when the operator only wanted one of the three.

**Decision.**
- Show Launch Template gains a sub-choice after picking a template:
  show one version's detail (existing behavior), list every version
  (number/creation time/default flag, via new
  `inventory.ListLaunchTemplateVersions`), or diff any two versions'
  decoded cloud-init content (reusing Sync's own diff mechanism,
  read-only -- never creates a version).
- Both Sync's diff and the new version-diff render through the shared
  List-tier component (`tui.RunListView`) in real interactive use, via
  new `displayRows`/`displayDiff` helpers -- scrollable, consistent
  chrome with every other resource listing in the app, rather than a
  second, purpose-built diff viewer. Accessible/test mode (no real
  bubbletea loop available) falls back to the same plain dump Sync
  already printed, so no existing test needed rewriting.
- Compute's single "Show resource lists" becomes three menu entries:
  "Show instances," "Show AMIs," "Show launch templates." S3 and Key
  Management are deliberately left alone -- each has exactly one
  resource type, so there's no paging-through-others problem to fix
  there.

**Rationale.**
- Listing versions and diffing two of them are different questions
  ("what versions exist" vs. "what changed") -- collapsing them into
  one action would either overload a single screen or force the
  operator through a diff they didn't ask for just to see a version
  list.
- Reusing the List-tier component for diffs (rather than a bespoke
  viewer) keeps the diff-in-a-scrollable-box mechanism identical
  everywhere it's needed (Sync's confirmation step and Show's
  version-diff), and matches this project's general preference for
  reusing existing chrome over inventing new UI per feature.
- Splitting Show resource lists only where the reported problem
  actually exists (Compute's three resource types) avoids restructuring
  S3/Key Management for a problem they don't have.

**Rejected alternatives.**
- *A single "list versions with diff" screen* -- combines two distinct
  questions into one, and would need to diff by default or require an
  extra step to opt out, neither of which is simpler than two separate
  choices.
- *A dedicated diff viewer component* (syntax-aware, side-by-side) --
  more capable, more to build than this need calls for; the List-tier's
  existing scroll/filter chrome is sufficient for plain-text unified
  diffs.
- *Keep Show resource lists combined, let `q` advance instead of exit*
  -- considered as a lighter-touch fix, but three separate, directly
  reachable menu entries is more discoverable than a "press q to see
  the next one" convention the operator would have to learn.

**Consequences.** New `inventory.ListLaunchTemplateVersions` +
`LaunchTemplateVersionSummary`; `show_launch_template.go` restructured
around a template-level sub-menu (existing tests updated for the new
leading choice-prompt); `MenuActions.ShowResourceLists` replaced by
`ShowInstances`/`ShowAMIs`/`ShowLaunchTemplates`; `mainMenuItems` grows
from 18 to 20, requiring the same hardcoded-index maintenance in
`menu_test.go` every prior menu-ordering change in this project has
needed. See `PLAN.md` Phase 20.28.

---

## 2026-07-20 — Accept "v"-prefixed launch template versions

**Context.** Found via the debug log from the operator's first
real-AWS pass over Phase 20.27
(`clasm-debug-20260720-132204.jsonl`): typing `v1` at a version prompt
-- a natural thing to type, since `launchTemplateLabel`'s own display
format is "default v2" -- caused a hard AWS rejection at two call
sites: `DescribeLaunchTemplateVersions` ("Invalid launch template
version: either '$Default', '$Latest', or a numeric version are
allowed") and `ModifyLaunchTemplate` ("A launch template version must
be specified..."). The latter is why Promote appeared to silently do
nothing -- it had actually failed outright, not succeeded-without-
refreshing as first suspected.

**Decision.** New `normalizeVersionSelector(s string) string` strips a
leading `v`/`V` from a plain version number (`"v1"` -> `"1"`) before it
reaches any AWS call. `"$Default"`/`"$Latest"` and anything not of the
exact form `v<digits>` pass through unchanged. Applied at all four
places a version selector is entered: the shared
`promptLaunchTemplateVersion`/`promptLaunchTemplateVersionLabeled`
(Show, Create-from-template, Sync's compare-against version, Show's
two version-diff prompts), Promote's version prompt, and Delete
Version(s)'s comma-separated list.

**Rationale.**
- The mismatch is self-inflicted: this project's own display convention
  ("v2", "v3" in `launchTemplateLabel`) primes the operator to type
  what they see elsewhere in the same tool, and AWS's API has no
  tolerance for that format at all -- normalizing at the boundary is
  more correct than asking every operator to remember an
  AWS-vs-clasm formatting distinction.
- A single shared normalization function, applied everywhere a version
  selector is entered, avoids fixing the same bug three more times as
  new version-entry points get added later.

**Rejected alternatives.**
- *Change `launchTemplateLabel`'s display format instead* (drop the
  "v" prefix) -- fixes the display/input mismatch from the other
  direction, but "v2"/"v3" is a reasonable, common way to label
  versions for a human reader; normalizing the input is less invasive
  than changing an already-shipped display convention.
- *Validate and reject `"v1"` with a clear error message, re-prompt* --
  considered, but silently accepting the obviously-intended value is
  friendlier than making the operator retype it correctly, and there's
  no ambiguity in what `v<digits>` means.

**Consequences.** `internal/workflow/show_launch_template.go` gains
`normalizeVersionSelector`; `launch_template_manage.go`'s Promote and
Delete Version(s) prompts both call it. Test-first: reproduced the
exact `"v1"` -> AWS-rejects-it failure before fixing it. See `PLAN.md`
Phase 20.28.

---

## 2026-07-20 — Launch templates: build directly from cloud-init YAML, diff-then-new-version sync, fold in IMDSv2

**Context.** Requested directly (`notes-from-tom.txt`) and confirmed as
v0.0.2's headline feature, 2026-07-20 -- clasm's Compute domain
currently has no concept of EC2 launch templates at all, and the
operator's work group uses them to encapsulate what a running instance
needs (RDM's software requirements, primarily), evolving that
definition over time as requirements change. TODO.md separately carried
an IMDSv2 bug (new instances launched by clasm set no
`MetadataOptions`, triggering security warnings) that turned out to
share the exact same AWS concept as the new work.

**Decision.** Build launch templates directly from cloud-init YAML,
never derived from an existing running instance: `CreateLaunchTemplate`/
`CreateLaunchTemplateVersion` take a `LaunchTemplateData` struct
constructed by clasm itself (reusing Feature 3's existing AMI/
instance-type/subnet/security-group/IAM-profile/tag prompts, with the
YAML's content as `UserData`), not `GetLaunchTemplateData`'s
instance-derived path. New template versions are created only after a
diff: decode the target version's `UserData`, compare against the
local YAML file, skip entirely if identical ("no changes -- nothing to
sync"), otherwise show a plain-text unified diff and require explicit
confirmation before `CreateLaunchTemplateVersion`. Promoting a version
to `$Default` is always a separate, explicit action
(`ModifyLaunchTemplate`), never a side effect of syncing. "Create EC2
Instance from Launch Template" is a third, parallel entry point
alongside Create-from-AMI and Create-from-Cloud-Init -- not a hybrid
wizard that also lets the operator override individual template
fields. IMDSv2 (`HttpTokens: required`) is folded into this same pass:
enforced unconditionally on every new template and on the existing
plain `RunInstances` launch paths (closing the pre-existing TODO.md
bug), and flagged passively (not auto-fixed) on any existing template
found without it.

**Rationale.**
- The operator has never used launch templates before and, in an
  earlier round of this design conversation, initially framed the
  sync question as "which YAML file is this template tied to" --
  clarified directly to "is there a mechanism to create a template
  without an existing instance," confirming the build-from-YAML-
  directly model rather than a persistent file↔template association
  clasm would need to track as new state.
- Diff-before-version avoids the AWS default of tools that always bump
  a version number regardless of content -- Tom's own framing of "does
  this actually require a new version" is exactly this no-op check,
  and it keeps a template's version history meaningful (one version
  per actual content change) rather than accumulating no-op versions
  from repeated syncs of unchanged YAML.
- Explicit promote-to-default (never automatic) matches the operator's
  own stated expectation: "I can see people experimenting with launch
  templates during the development process" -- an in-progress sync
  shouldn't change what a plain "Create from Launch Template" launch
  picks up by default.
- Folding IMDSv2 into this pass rather than deferring it with the
  tags-screen/backup-bucket-default/top-level-tag-management items
  (also open in TODO.md) is justified narrowly by shared surface area
  (`MetadataOptions`/`InstanceMetadataOptionsRequest` touches the same
  `RunInstances`/`RequestLaunchTemplateData` code this phase already
  changes) -- not a general precedent for bundling unrelated bug fixes
  into feature work.
- The plain-text diff (`github.com/aymanbagabas/go-udiff`, already
  present in `go.sum` as an indirect dependency via
  `charmbracelet/x/exp/teatest`, used by `internal/filemanager`'s own
  tests) means no new third-party dependency is actually introduced --
  only promoted from indirect to direct, the same move Phase 20.24
  already made for `x/ansi`.

**Rejected alternatives.**
- *Derive templates from an existing instance's live config*
  (`GetLaunchTemplateData`) -- doesn't fit the operator's actual flow
  (YAML authored first, template built from it), and would require an
  instance to already exist before a template could, backwards from
  "create a template without an existing EC2 instance."
- *Always create a new version on sync, no diff check* -- simpler, but
  produces meaningless version history (every sync bumps a version
  even with no actual content change) and doesn't answer Tom's own
  question about whether a sync is even needed.
- *Auto-promote a new version to default after sync* -- more
  convenient, but means an unreviewed, possibly-experimental version
  silently becomes what the next plain launch picks up.
- *Hybrid "launch from template but override individual fields"
  wizard* -- an earlier framing in this same design conversation,
  dropped once the operator clarified the actual ask was a third
  parallel entry point, matching Create-from-AMI/Create-from-Cloud-
  Init's existing shape.
- *A dedicated "audit all templates for IMDSv2" action* -- more
  visible, but more to build than the operator actually asked for
  ("recommended for existing templates if missing"); passive flagging
  on the existing Show/List screens covers that without a new
  top-level action.
- *Defer IMDSv2 alongside the tags-screen/backup-bucket-default/
  top-level-tag-management items* -- considered, since those are also
  open TODO.md items being deliberately deferred past this phase, but
  rejected specifically because IMDSv2's `MetadataOptions` surface
  overlaps directly with code this phase already touches, unlike the
  other three.

**Consequences.** New `internal/inventory.LaunchTemplate` type and
per-version detail type; `EC2API` gains 7 methods; six new Compute-menu
actions; `github.com/aymanbagabas/go-udiff` promoted to a direct
dependency; `launch_execute.go`'s `RunInstances` call gains
`MetadataOptions` (previously absent). No change to any existing
v0.0.1 workflow's behavior otherwise -- v0.0.1 is already piloting in
production, so this is additive. See `PLAN.md` Phase 20.27.

---

## 2026-07-20 — Full-height Menu tier via live WindowSizeMsg tracking, applied at every depth

**Context.** Phase 20.24 (2026-07-13) cleared the screen at startup but
deliberately left the "full height" half of that request unimplemented
-- the domain picker (and every Menu-tier `huh.Select`) stayed a
compact, content-sized box, pending clarification of what "full
height" meant for a component with no built-in notion of it. Clarified
directly, 2026-07-20: the wrapping TUI chrome should carry a real
terminal height, driving how many rows the `huh.Select` shows; when a
menu has fewer options than that, the chrome should still indicate the
full screen rather than shrinking to content.

**Decision.** Use `huh.Select.Height(n)`/`huh.Form.WithHeight(n)`
directly -- confirmed by reading `huh` v1.0.0's source rather than
assumed: it already subtracts title/description height before sizing
the options viewport, and renders through `lipgloss.Style.Height`,
which pads short content with blank lines to reach `n`. Get `n` by
intercepting `tea.WindowSizeMsg` live (the same pattern
`internal/tui/picker.go`/`listview.go` already use for the Picker/List
tier) rather than a one-shot `x/term.GetSize` read before the form
starts, so a mid-session terminal resize is picked up the same way it
already is everywhere else in the app. Apply this at `runMenuField`,
the single shared entry point every Menu-tier `huh.Select` already
runs through -- so the root domain picker and every submenu (S3, EC2,
Key Management) become full-height together, not just the root picker
alone.

**Rationale.**
- huh's own `WindowSizeMsg` handling only shrinks a group to fit
  (`min(neededHeight, msg.Height)`) when `f.height == 0`; it never
  grows short content to fill unused space, so `WithHeight` must be
  called explicitly with a real value -- there's no simpler built-in
  toggle for this.
- Live tracking via `WindowSizeMsg` reuses an already-proven pattern in
  this codebase instead of introducing a second, weaker mechanism
  (`x/term.GetSize`) that would go stale on resize.
- Fixing this once in `runMenuField` avoids the exact inconsistency
  Phase 20.24 refused to introduce: only the root picker being
  full-height while every submenu stayed compact, undoing the
  chrome-consistency work of Phases 20.17-20.25.

**Rejected alternatives.**
- *One-shot `x/term.GetSize` before `Run()`* -- simpler, but blind to a
  terminal resize mid-menu, unlike every other screen in the app.
- *Full-height only at the root domain picker* -- explicitly the
  option Phase 20.24 already declined, for the inconsistency reason
  above.
- *Redesign the whole Menu tier onto a full-height bubbletea component,
  retiring `huh.Select` for navigation menus* -- the "bigger
  alternative" flagged in the 2026-07-14 hand-off as unscoped; not
  needed now that `huh.Select.Height`/`Form.WithHeight` turned out to
  already support this directly.

**Consequences.** `runMenuField`'s `quitKeyGuard` wrapper (currently
used only to guard the Quit keybinding while a field is filtering)
gains `WindowSizeMsg` interception and is extended to wrap the
non-filtering `form.Run()` path too, so both paths go through one
`tea.Model`. The `"(q to go back)"` hint `runMenuField` prints outside
the form's own box must be accounted for in the reserved-line budget
passed to `WithHeight`, or combined output overflows the terminal by
one line. See `PLAN.md` Phase 20.26.

---

## 2026-07-13 — Bucket picker for Backup Archive & Trim

**Context.** Backup Archive & Trim's S3 bucket prompt was pure free
text -- no memory aid for an operator who has to recall the exact
bucket name from scratch every run, unlike every other bucket-selection
call site in the app (`pickBucket`, Phase 20.4), which already offers a
filterable pick list. Requested directly, with an explicit requirement
that typing a bucket name (not just picking from a list) must remain
possible.

**Decision.** New `promptBackupBucket`: fetches this account's S3
buckets (`inventory.ListBuckets`, the same call `refreshS3` already
uses) and offers them as a filterable pick list (`'/'` to filter,
matching every other filterable screen in this app), plus an "Other
(type a bucket name)" entry that falls through to the original
free-text prompt. Falls back to the free-text prompt directly (no
picker at all) if the listing fails or comes back empty -- there's
nothing more reliable, or for an empty account nothing useful, to offer
instead (mirrors `promptKeyPairNameOrCreate`'s own precedent for the
identical reason). Deliberately built as a Menu-tier `huh.Select`
(via the existing `pickComparable` helper) rather than a Picker-tier
`tui.RunPicker` (unlike every other bucket-selection call site) --
`promptBackupBucket` must stay embedded inside `backupArchiveAndTrim`'s
own pipe-testable prompt sequence (directory, then bucket, then age
threshold, per "Reorder Backup Archive & Trim's prompts" below), and a
real bubbletea Program can't be driven by a test's pipe input the way
`pickBucket`'s callers already accept -- huh's own built-in `'/'`
filtering (confirmed: same default keybinding as this project's
Picker/List-tier filter convention) covers the "let me narrow this down
by typing" need without needing the untestable Picker-tier component at
all.

**Rationale.** huh.Select's accessible-mode pipe-testing path expects a
row *number*, not free text, so existing tests (which never populate
`fakeS3Client.buckets`) naturally exercise the empty-list fallback
branch unchanged -- zero existing tests needed rewriting. New tests
cover the populated-list path (picking a known bucket by number),
the "Other" escape hatch, and the listing-error fallback, each
asserting on the resulting bucket name via the upload command's
`s3://<bucket>/...` destination (the same verification technique
`TestBackupArchiveAndTrim_UntaggedInstanceUsesIDAsKeyPrefix` already
established).

**Consequences.** New `bucketChoice` type and `promptBackupBucket`
function in `internal/workflow/backup_archive.go`; no signature changes
to `BackupArchiveAndTrim`/`backupArchiveAndTrim` (bucket resolution
still happens in the same place in the same testable core, just via a
different prompt function).

---

## 2026-07-13 — Clear the screen at startup

**Context.** clasm printed its first line ("clasm x.x -- authenticated
as AWS account ...") directly into whatever the terminal already had on
screen -- old shell history, a previous command's output, etc. --
unlike a typical full-screen terminal application, which starts from a
clean slate. Requested directly, alongside a request to have the
startup screen use the terminal's full height -- addressed here only
partially; see the note in PLAN.md's corresponding phase entry about
what was deliberately not changed and why.

**Decision.** New `ui.ClearScreen(w io.Writer)`, sending the same two
escape sequences bubbletea's own `tea.ClearScreen` command sends
(`ansi.EraseEntireScreen` + `ansi.CursorHomePosition`, from
`github.com/charmbracelet/x/ansi` -- already a transitive dependency
via `bubbletea`, now promoted to direct), rather than a hand-rolled
escape string. Called once in `main()`, after the `-help`/`-license`/
`-version` early-exits (which must stay script/pipe-friendly, so they
must not inject terminal control codes) but before any other output,
including error paths (config load failures, AWS client construction
failures, etc.) -- the whole interactive session starts from a clean
terminal, not just the happy path.

**Rationale.** Reusing bubbletea's own escape sequences (via the
`x/ansi` package it's already built on) keeps this consistent with how
every List/Picker/Manager screen already clears itself on `Init()`,
rather than inventing a second, potentially-different way to clear a
terminal.

**Consequences.** New `internal/ui/clear.go` (+ test);
`github.com/charmbracelet/x/ansi` promoted from indirect to direct in
`go.mod`. `cmd/clasm/main.go` calls `ui.ClearScreen(out)` once, right
after the flag-based early exits.

---

## 2026-07-13 — huh fields get a full box border to match tui's chrome

**Context.** Phase 20.17 gave `huh` fields and `internal/tui`'s boxes the
same indigo accent color, but not the same *shape*: `huh.ThemeBase()`'s
`Focused.Base` draws only a thick bar down the left side of a field
(`lipgloss.ThickBorder().BorderLeft(true)`), while `tui/box.go` draws a
full `┌─┐│ │└─┘` rectangle. Matching color without matching shape left
a Menu-tier `huh.Select` and a Picker/List/Manager screen still reading
as two different visual languages -- raised when reviewing chrome
consistency alongside adding contextual description text (below).

**Decision.** `tui.Theme()`'s `Focused.Base`/`Focused.Card` now call
`.Border(lipgloss.NormalBorder())` (the same box-drawing characters
`box.go` uses) instead of inheriting `ThemeBase`'s left-only
`ThickBorder`, still colored in the shared accent. `Padding(0, 1)`
replaces `ThemeBase`'s `PaddingLeft(1)` (which existed only to clear
the single left bar) with balanced left/right breathing room, matching
`box.go`'s `BoxLine`'s own "│ content │" convention. `Blurred.Base`
still hides its border via `lipgloss.HiddenBorder()`, now reserving the
same four-sided footprint rather than a one-sided one.

**Rationale.** Every clasm form is a single field in a single group (no
multi-field forms exist in this codebase -- confirmed when `Theme()`
was first written), so a full box reads as a small dialog card rather
than a form-with-a-sidebar-accent -- the same "boxed window" shape a
List/Picker/Manager screen already has. Verified via a throwaway test
rendering `Theme().Focused.Base.Render(...)` directly with a forced
true-color profile and inspecting the raw ANSI output (the same
technique used to verify Phase 20.17's border styling), rather than
driving a real interactive terminal.

**Consequences.** No signature changes -- `Theme()`'s return type and
every call site are unaffected; this is a pure style-value change
inside the function.

---

## 2026-07-13 — Contextual description text on Menu/Picker-tier screens

**Context.** The domain picker (and every other Menu-tier `huh.Select`
and Picker-tier `tui.RunPicker` screen) showed only a bare title -- no
explanation of what the choice means or what happens next. Raised
alongside the border-matching decision above, as part of the same
"make the chrome and the screens themselves more consistent and
informative" pass.

**Decision.** Every Menu-tier `huh.Select` gains a `.Description(...)`
call (huh's own built-in field, previously unused everywhere in this
codebase) with one or two sentences of context. `tui.PickerConfig`
gains a new `Description string` field, rendered as its own line
(matching `Header`'s existing shape: the line itself plus a `Divider`)
directly below the top border, above any `Header`/rows;
`filterableWindowHeight` accounts for its two extra chrome rows the
same way it already does for `Header`. `pickString`/`pickComparable`
(the shared Menu-tier helpers) and `pickImage`/`pickBucket`/
`pickInstance`/`pickInstanceDefaulted` (Picker-tier functions called
from more than one call site with meaningfully different context) all
gained a `description string` parameter threaded from their own
callers; Picker-tier functions with exactly one caller
(`pickInstanceProfileChoice`, `pickRole`, `pickSubnet`,
`pickKeyPairChoice`, `pickKeyPairForDeletion`, `pickLifecycleRule`) got
a single description written directly into the function instead, since
there was no real per-call-site variation to preserve. List-tier
screens (`ListViewConfig`/`ListViewModel` -- the tabular "Show resource
lists" displays) deliberately did NOT get a `Description` field: they
aren't "just a pick list," and their tabular column headers already
carry the relevant context.

**Rationale.** huh's `.Description()` is a stable, well-established
part of its own API -- adding text there is essentially free and
carries no risk of a rendering bug on our part. The Picker-tier
`Description` field mirrors `Header`'s existing chrome shape exactly
(same two-row cost, same position, same `Divider` beneath it), so it
reuses an already-proven layout instead of inventing a new one.
Threading a parameter only where callers actually differ (rather than
everywhere) avoids padding every call site with an unused, always-""
argument.

**Consequences.** `pickString`, `pickComparable`, `pickImage`,
`pickBucket`, `pickInstance`, `pickInstanceDefaulted` all gained a
`description string` parameter -- every call site across
`internal/workflow` was updated. `internal/tui/filter.go`'s
`filterableWindowHeight` gained a `hasDescription bool` parameter
(`ListViewModel`'s call site passes `false` explicitly, since List-tier
doesn't use this). New `internal/tui/filter_test.go` and additions to
`picker_test.go` cover the height math and rendering position.

---

## 2026-07-13 — Recall Backup Archive & Trim's instance/directory choices per-instance

**Context.** Backup Archive & Trim always started from a blank slate:
pick an instance from the full list every time, and (absent a
`backupDirRules` Name-pattern match) type the backup directory from
scratch every time -- even for an operator who runs this same
instance/directory combination repeatedly. Requested directly: recall
what was used last time as the default for next time.

**Decision.** A new `internal/state` package persists a small
`~/.clasm_state` YAML file -- deliberately NOT folded into `~/.clasm`
(config.Config), which is exclusively user-hand-edited today (`Load()`
only, no `Save()` exists): auto-writing into a file the operator
maintains by hand risked silently reformatting or stripping their own
edits. `~/.clasm_state` is exclusively app-managed, safe to delete, and
never meant for hand-editing. History is keyed per-instance
(`map[instanceID]directory`), not a single global "last used" value,
since different instances plausibly back up different directories.
`internal/workflow.BackupHistory` is the narrow interface `internal/
workflow` sees (`LastInstanceID`, `LastDirectoryByInstance`, and a
`Save(instanceID, directory string) error` callback) -- this package
never imports `internal/state` or knows about YAML/file paths;
`cmd/clasm/main.go` owns the actual on-disk format and wires the
callback. The recalled directory takes priority over `backupDirRules`'
Name-pattern-based default (it reflects what was actually typed for
this exact instance most recently, not a generic pattern match). The
instance picker gained a new `tui.PickerConfig.InitialCursor int` field
(pre-positions the cursor on a specific row instead of always starting
at 0) so the recalled instance is pre-selected, not just pre-filled as
text -- `pickInstance`/`pickInstanceDefaulted` (`power_state.go`) split
so every other caller of `pickInstance` (start/stop/terminate/create-
AMI/etc.) is unaffected, while Backup Archive & Trim's own call site
passes `hist.LastInstanceID`.

**Rationale.** Separating "settings" (hand-edited, read-only to the
app) from "history" (app-managed, disposable) avoids ever surprising an
operator by rewriting a file they maintain themselves. Per-instance
keying costs one extra map versus a single string field, for
meaningfully better behavior once more than one instance is in
rotation. `Save`'s error is reported to `w` as a warning, not returned
as fatal -- history is a convenience, not core to the backup itself; a
disk-write failure here shouldn't abort an otherwise-successful backup
run.

**Consequences.** `BackupArchiveAndTrim`'s signature gained a
`hist BackupHistory` parameter (every call site, including all
existing tests, updated -- the zero value disables all of this,
matching pre-existing behavior exactly). `tui.PickerConfig` gained
`InitialCursor`; out-of-range values (including the zero value, when
there's no prior choice) fall back to row 0, so every other
`PickerConfig` caller is unaffected. New `internal/state` package (+
tests) and new tests in `internal/workflow/backup_archive_test.go`
(history takes priority over the Name-pattern rule; `Save` is called
with the right instance/directory; a `Save` error is a warning, not
fatal) and `internal/tui/picker_test.go` (`InitialCursor` positions the
cursor; out-of-range falls back to 0).

---

## 2026-07-13 — Reorder Backup Archive & Trim's prompts

**Context.** The workflow asked its four questions in an order that
didn't match how an operator actually thinks about the task: instance,
backup directory, age threshold (days), S3 bucket -- age threshold sat
between "where are the files" (instance, directory) and "where are
they going" (bucket), which reads oddly since the threshold is more
naturally understood once both endpoints are already known. Requested
directly, with the exact desired order confirmed: instance, directory,
bucket, then age threshold.

**Decision.** Moved the S3 bucket prompt (and its immediately-following
`BucketRegion`/`newS3Client`/`CheckS3BucketAccess` pre-flight sequence)
to run directly after the backup directory prompt, ahead of the age
threshold prompt, which now runs last, immediately before the dry-run
listing.

**Rationale.** "Of the files in that directory, which are old enough to
move to that bucket" reads as one coherent question once both the
source directory and destination bucket are already fixed, rather than
asking "how old" before the destination is even known.

**Consequences.** No parameter or return-type changes -- purely a
reordering of existing prompt calls within `backupArchiveAndTrim`.
Every existing test's input string (four `\n`-joined answers in read
order) was updated to match the new order; assertions were otherwise
unchanged.

---

## 2026-07-13 — Chrome standardization: one shared indigo accent via lipgloss

**Context.** With termlib fully removed (below), every screen in clasm
is either a `huh` field or a `bubbletea` component, but they render as
two unrelated visual languages: `huh`'s default `ThemeCharm` is a
colorful indigo/fuchsia/cream card with a thick colored border;
`internal/tui`'s List/Picker/Manager chrome is plain, uncolored ASCII
box-drawing. The user asked to standardize chrome as the next pass
after termlib removal, once it was clear the two tiers had never been
visually reconciled.

**Decision.** Adopt a single shared accent color — the adaptive indigo
`ThemeCharm` already uses (`#5A56E0` light / `#7571F9` dark) — applied
two ways: a new `tui.Theme() *huh.Theme` (built from `huh.ThemeBase()`,
adding only the indigo accent to focused titles/borders/selection,
omitting `ThemeCharm`'s fuchsia/cream/green/red extras) wired into
every `huh.NewForm(...)` call site (traced: exactly five in the whole
app); and the same indigo + bold applied to `internal/tui/box.go`'s
shared border/title-rendering functions, which `ListViewModel`,
`PickerModel`, and `internal/filemanager` all call directly — one
change re-skins all three tiers. Cursor-row reverse-video and
instance-state green/red/yellow are left alone (selection/data
semantics, not branding). Also folded into this pass: `progress_ticker.
go`'s printed line becomes a real `bubbles/spinner`-based component
(explicitly deferred out of the termlib removal for exactly this
reason), and `object_browser.go`'s one remaining bare `huh.Select`
bucket picker moves onto the shared `pickBucket`/`PickerModel` — the
only bucket-selection call site that hadn't already converted in Phase
20.4.

**Rationale.**
- Reusing `ThemeCharm`'s own indigo (rather than inventing a new color)
  keeps continuity with what's already been on screen since Phase
  20.2, and it's already adaptive to light/dark terminal backgrounds —
  no new color-theory work needed.
- A single accent, not `ThemeCharm`'s full five-color set, matches an
  internal ops tool's understated register better than a colorful
  consumer-CLI look (DESIGN.md's own framing throughout: "internal tool
  for Library staff... not public-facing").
- `internal/tui/box.go` is the one place style changes apply to every
  tier at once, confirmed by checking that `internal/filemanager` calls
  the exact same exported functions `ListViewModel`/`PickerModel` call
  internally — no per-tier copy to keep in sync.
- `lipgloss` needs no new NO_COLOR/non-TTY plumbing — it already
  detects both via `termenv` and no-ops its own ANSI codes, so this
  doesn't duplicate or conflict with the existing manual
  `ui.ColorEnabled()` gate (which stays scoped to the STATE column it
  already governs).
- `bubbles/spinner` and `lipgloss` are not new dependencies in
  substance: `bubbles` is already a direct dependency (its `key`
  sub-package is used throughout the Menu tier), and `lipgloss` is
  already an indirect one via `huh`/`bubbletea` — this is drawing on
  the charm ecosystem this project already standardized on, not
  introducing a new library.

**Rejected alternatives.**
- *Restyle `huh` to match `tui`'s plain/monochrome look* — considered
  and explicitly not chosen (asked directly): would mean fighting
  `huh`'s own default rendering to strip color back out, rather than
  giving the plainer tier a real style. Bringing `tui` up to meet `huh`
  reuses more of what's already proven on screen.
- *Adopt `ThemeCharm` verbatim, no custom theme* — rejected: its
  fuchsia/cream/green/red extras elsewhere in the theme would still
  leave `tui`'s boxes with no equivalent color to match against for
  everything but the accent; a custom subset theme is barely more work
  and gives `tui` one clear color to mirror.

**Consequences.**
- A new `internal/tui` file defines the shared `huh.Theme` and the
  lipgloss styles `box.go` uses — `internal/tui` gains a `huh` import
  it didn't previously need (still no import cycle: `tui` imports
  nothing from `ui` or `workflow`).
- Five `huh.NewForm(...)` call sites gain `.WithTheme(tui.Theme())`.
- `progress_ticker.go`'s public shape (`startProgressTicker`) is
  replaced by a `bubbletea`-based equivalent; callers (`create_ami_
  from_instance.go`, `show_cloud_init.go`, `backup_archive.go`) update
  to the new call shape. Its `interval time.Duration` parameter is
  dropped rather than carried over: under the old `fmt.Fprintf`-per-
  tick design it meant "how often a new status line prints" (all three
  callers passed the same `30*time.Second`); under a real animated
  spinner that value would mean "how often the glyph advances," and
  30s is far too slow to read as an animation, so keeping the argument
  would leave it dead or silently wrong everywhere it's called. A
  package constant, `DefaultSpinnerInterval = 120*time.Millisecond`,
  replaces it.
- `object_browser.go` drops its bespoke `selectBucket` construction in
  favor of `pickBucket`; `huhCancelledIsNil` stays in use for
  `confirmLink`/the local-directory `huh.Input` in the same function.

---

## 2026-07-13 — Remove termlib entirely: input via huh, output via io.Writer

**Context.** The Menu/Picker/List tier conversion (Phase 20.2-20.14) is
done, but `termlib` itself is still in `go.mod` with ~44 files importing
it — every action wizard's free-text/confirm prompts, plus
`internal/ui`'s lower-level helpers (`color.go`/`display.go`/
`picklist.go`/`prompt.go`) and `internal/workflow/confirm.go`. Per
DESIGN.md's "Not decided yet" note, the plan for this remaining surface
was deliberately left open ("may evolve as we work through the
transition"). Before converting anything, we audited every remaining
`termlib` symbol's actual call sites (not just its imports) to find out
exactly what needs replacing — see DESIGN.md, "Removing termlib: Action
Wizards and Output," for the full table.

**Decision.** Remove `termlib` from `go.mod` entirely. Its two real
roles split cleanly:
1. **Interactive input** (`ui.Prompt`, `Confirm`, `ConfirmDestructive`,
   built on `termlib.LineEditor.Prompt`) → rebuilt on `huh.Input`/
   `huh.Confirm`, using the same split-into-testable-core pattern
   already established for the Menu tier.
2. **Plain status/error output** (`termlib.Terminal.Printf`/`Println`/
   `Refresh`, used for things like "Exiting.", error text after a
   failed action, the progress ticker's periodic elapsed-time line) →
   a plain `io.Writer`, since none of these call sites use anything
   `termlib.Terminal` offers beyond buffered printing, and the
   buffering itself (`Refresh`) is unneeded for straight-line
   sequential text.

Two adjacent, smaller pieces:
- `internal/ui.PickList` and its test are deleted outright — dead code,
  every real call site was already converted in the Menu/Picker/List
  punch list; only comments still mention it.
- `termlib.PadRight`/`Truncate`/`Bold`/`Reset`/`Green`/`Red`/`Yellow`
  (column formatting + ANSI constants, used only in `internal/ui`) and
  `termlib.FormatDuration` (used only in `progress_ticker.go`) are
  reimplemented locally — each is small (10-20 lines) and has no other
  caller once traced.

**Rationale.**
- The surface audit found that of `termlib.Terminal`'s and
  `LineEditor`'s full APIs, only `Printf`/`Println`/`Refresh` and
  `Prompt` are ever called anywhere in this codebase — history, tab
  completion, `$EDITOR` composition, multi-line input, cursor movement,
  and color state are all unused. Replacing termlib with huh (for
  input) and a bare `io.Writer` (for output) removes a dependency
  without losing any feature this codebase actually exercises.
- Matches the project's already-stated direction (DESIGN.md, "Terminal
  UI Architecture," 2026-07-10): "`termlib` ... is being removed
  entirely before 0.0.2 in favor of `huh` and `bubbletea` exclusively."
  This decision is the concrete plan for the one part of that statement
  that hadn't been scoped yet — the action-wizard/output call sites.
- Keeping this pass scoped to *removal* (not restyling) matches the
  user's own explicit split: migrate off termlib first, standardize
  chrome (color libraries, spinner components, etc.) as a separate,
  later pass. Two considered pieces were deliberately kept mechanical
  rather than upgraded here:
  - The progress ticker (`progress_ticker.go`) is the one place with a
    plausible case for a real `bubbletea` spinner component instead of
    a periodic printed line. Decided to keep it a plain `io.Writer` for
    this pass and defer any spinner to the chrome-improvement pass.
  - `internal/ui`'s color/format helpers could adopt `lipgloss` (already
    a transitive dependency via `huh`/`bubbletea`) instead of local ANSI
    constants. Decided to keep local constants for this pass and defer
    a `lipgloss` migration to the chrome-improvement pass, so this
    removal doesn't grow a second, unrelated goal.

**Rejected alternatives.**
- *Keep termlib for LineEditor's readline features, drop only
  Terminal* — rejected because those features (history, completion,
  `$EDITOR`) are entirely unused; keeping the dependency around for
  capabilities nothing calls has no benefit.
- *Introduce a thin backwards-compatible `Terminal`/`LineEditor`
  shim inside clasm itself* — rejected as an unnecessary
  compatibility layer for a one-time internal refactor with no external
  consumers of these types.
- *Convert file-by-file, keeping termlib in go.mod until the last call
  site is gone* — the Menu/Picker/List tiers could do this because each
  call site's own picker/menu was independently swappable without
  touching its caller's signature. Here, `t`/`le` are threaded through
  nearly every `internal/workflow` function's signature, so Go's
  whole-module compilation means this has to land as one coordinated
  change (ordered into PLAN.md Phase 20.15/20.16), not 40 independent
  single-file diffs.

**Consequences.**
- `le *termlib.LineEditor` disappears from every function signature in
  the codebase — it was never called directly outside `ui.Prompt`,
  `ui.PickList` (deleted), and `Confirm`/`ConfirmDestructive`.
- `t *termlib.Terminal` becomes `io.Writer` wherever a function prints
  status/error text directly; drops out of the ~9 files where it was
  pure pass-through with no direct use.
- Every test that constructs `termlib.New(&buf)` to capture output is
  rewritten to use the `bytes.Buffer` directly as an `io.Writer`, or to
  drive the new huh-based prompt/confirm cores via the established
  accessible-mode pipe pattern.
- `go.mod`'s `github.com/rsdoiel/termlib` entry is removed once the last
  call site is converted.

---

## 2026-07-10 — Give ListView the same filter as Picker, via a shared filterState

**Context.** Right after confirming the `tea.ClearScreen` scrolling fix
("Scrolling is much improved"), the user asked whether List-tier
filtering was still planned, recalling an earlier discussion. Checking
DESIGN.md confirmed it: the keybinding conventions table has listed
`/` = Filter for "Menus, pickers, lists, managers" since Phase 20.8,
but `ListViewModel` (`internal/tui/listview.go`) had no filter code at
all -- a real, previously-documented gap, not a misremembering.

**Decision.** Add filtering to `ListViewModel`, matching `PickerModel`
exactly (case-insensitive substring match, `/` to start typing, `Enter`
commits, `Esc` clears, content-height pinned to the unfiltered row
count while typing). Rather than copy `PickerModel`'s filter fields and
methods a second time, extract them into a shared `filterState` type
(`internal/tui/filter.go`) that both models embed: `visible []int`,
`cursor int`, `filtering bool`, `filter string`, plus `apply`,
`moveCursor`, `handleIdleKey`, `handleFilterKey`, `statusLine`. Each
model's `Update` still owns its own quit/select semantics (List just
quits on `q`; Picker also selects on `Enter`) and delegates everything
else to the shared type.

While unifying the two models' box-height math to accommodate the new
filter status line, also folded in `PickerModel`'s existing (optional)
header handling into `ListViewModel` (previously always rendered, even
blank) and replaced both models' separate `windowHeight()` bodies with
one shared `filterableWindowHeight(height, hasHeader bool)` helper --
which also fixed a minor pre-existing off-by-one in `PickerModel`'s own
chrome arithmetic (it subtracted a flat, imprecise `-1` for the filter
line instead of counting the filter line's own divider row).

**Rejected alternative.** Duplicate Picker's filter implementation
directly into `ListViewModel`. Rejected because the user's stated goal
across this whole follow-up ("we want to have the chrome more
consistent") is exactly what a second hand-copy would undermine --
consistency by convention (two implementations that happen to match
today) drifts the moment either one is touched later. A shared type
keeps them identical by construction.

**Consequences.** `ListViewModel` and `PickerModel` are now guaranteed
to filter, scroll, and size identically; any future filter change lands
in one place. `internal/tui/listview_test.go` gained direct mirrors of
`picker_test.go`'s filter tests (minus selection, which List doesn't
have). No behavior change for any existing `ListView`/`Picker` caller:
all currently supply a non-empty `Header`, so the now-conditional
header line renders exactly as before.

## 2026-07-10 — Clear the screen on entry for every inline bubbletea screen

**Context.** After the List tier's conversion (Phase 20.13), the user
reported that the List view "doesn't take advantage of the window
height so a significant number of lines aren't visible much of the
time," and separately wanted "the chrome more consistent" across
screens so switching between them isn't jarring. Follow-up narrowed the
first report to: the box *does* size to the real terminal height, but
paging scrolls content out of view -- and the desired consistency is
Picker/ListView/file-manager behaving identically to each other, not
adopting `tea.WithAltScreen()` (which `huh`, used for every Menu-tier
prompt, has no equivalent for at all -- adopting alt-screen for only
some screens would make transitions *more* jarring, not less).

**Root cause.** `windowHeight()` in each of `ListViewModel`/
`PickerModel`/filemanager's `Model` sizes its box to (terminal height −
a small fixed chrome overhead) -- nearly the *entire* terminal. None of
the three clear the screen on entry (DESIGN.md's own note: "Renders
inline, no `tea.WithAltScreen`, matching every other screen in
clasm"), so each one starts rendering wherever the cursor already sits
-- e.g. below a previous menu's prints. If that near-full-height box
doesn't fit in the rows remaining below the cursor, the terminal
scrolls to accommodate it, and bubbletea's redraw-in-place bookkeeping
(how many lines to move the cursor up by, to redraw the same frame in
place) goes stale relative to what the terminal actually did --
pushing the top of the box (title, header, and however many rows above
the scroll point) out of the visible viewport. This is exactly the
"significant number of lines aren't visible much of the time" report:
not a sizing bug, a scroll-desync bug, and it gets worse the fuller the
terminal already is when the screen launches.

**Decision.** Every inline bubbletea screen (`ListViewModel.Init`,
`PickerModel.Init`, filemanager's `Model.Init`) now returns
`tea.ClearScreen` (bubbletea's own built-in command for exactly this
situation -- its doc comment: "can be used to move the cursor to the
top left of the screen and clear visual clutter when the alt screen is
not in use") as (part of) its initial command, guaranteeing every one
of these screens always starts rendering from row 0. This makes the
already-correct `windowHeight` sizing reliable (the box always fits,
since there's nothing above the cursor to compete for terminal rows
with it) and, as a side effect, gives Picker/ListView/file-manager one
more point of behavioral consistency: each always wipes whatever was on
screen and starts crisp, rather than accumulating underneath the
previous screen's leftover output.

**Rejected alternatives.**
- *Shrink `windowHeight` to leave a safety margin below whatever's
  already on screen* -- rejected: there's no reliable way to know how
  many rows are already "used" above the cursor (that would require
  querying the terminal's actual cursor position, which bubbletea
  doesn't expose), so any fixed margin is either too conservative
  (wastes screen space the user explicitly wants used) or still
  breaks in a sufficiently full terminal.
- *Switch Picker/ListView/file-manager to `tea.WithAltScreen()`* --
  rejected per the user's own stated preference: `huh` (every
  Menu-tier prompt) has no alt-screen equivalent, so only some screens
  taking over the full terminal while others render inline beside
  whatever's already there would be a *new* inconsistency, not a fix
  for the existing one.

**Consequences.** `tea.ClearScreen` clears the primary screen buffer,
not the alternate one -- it doesn't touch terminal scrollback the way
real alt-screen entry/exit does, so this is fully compatible with the
existing "inline, no alt-screen" design decision; it complements it
rather than reversing it. No test changes were needed -- every
existing `teatest`/direct-`Model`-driven test already drains whatever
`Init()`/`Update()` commands are queued, including the new
`tea.ClearScreen`, without asserting on `Init()` returning `nil`.

---

## 2026-07-10 — Full conversion punch list: every PickList/Display* call site classified by target tier

**Context.** After Phase 20.9 (lifecycle action menu → `huh.Select`),
the user asked to "review the source code and identify all the places
where we want to upgrade to the huh.Select, our Picker and View lists"
— a comprehensive punch list to work through quickly, extending the
Picker-only map from the previous decision entry to also cover Menu
(`huh.Select`) and List (`tui.ListView`) targets.

**Decision.** Surveyed every `ui.PickList` call site (33 total) and
every `ui.Display*` function (4 total) directly from source, classified
each into Menu / Picker / List, and recorded the full result in
DESIGN.md's "Picker tier" section as three tables (one per target tier)
with file:line references and a done/not-started status column.
Classification rule applied consistently: fixed, small, compile-time-
known option sets (domain/action menus, curated instance-type lists,
storage-class enums, kind pickers) → Menu; fetched, potentially long,
variable-length AWS resource collections → Picker; read-only resource
displays → List.

**Rejected alternatives.**
- *Classify storage-class selection (`bucket_lifecycle.go:296,399`) as
  Picker, since it's technically a list of AWS-defined values* —
  rejected: both lists are fixed and known at compile time (one
  curated to 4, one the full but still static `TransitionStorageClass`
  enum), not fetched from AWS at runtime, and short enough that
  scrolling/filtering wouldn't help — matching the Menu tier's
  definition, not the Picker tier's.
- *Classify region selection (`bucket_create.go:26`,
  `keymgmt_common.go:25`) as Picker* — rejected for the same reason:
  these are this team's own configured region list (typically 2
  entries), not a fetched AWS resource collection.

**Consequences.** No code changed by this decision — it's a planning
artifact. Nothing beyond what's already marked "done" in the three
tables is scheduled; each conversion still gets picked up and scoped
individually, per this project's established incremental discipline
(TODO.md, "Termlib Removal (before 0.0.2)").

---

## 2026-07-10 — Add a Picker tier: resource selection gets its own internal/tui component, not huh.Select

**Context.** Starting Phase 20.4 (converting the three S3
bucket-selection call sites from `ui.PickList` to `huh.Select`, reusing
`object_browser.go`'s existing `runFieldWithHelp`/`huhCancelledIsNil`
pattern) the user stopped this before any code was written: "I think
this UI should feel the same whether I select a bucket, an AMI or an EC2
instance." A real gap in the prior day's taxonomy: `huh.Select`'s own
rendering looks nothing like the bordered-box/legend-bar chrome the List
and Manager tiers just adopted (DESIGN.md, "Terminal UI Architecture"),
so converting bucket-selection to `huh.Select` would have made the S3
domain show two different visual languages depending on whether a
screen displays a resource or selects one — precisely the inconsistency
the whole termlib-deprecation effort exists to avoid.

**Decision.** New `internal/tui.PickerModel`: reuses `ListViewModel`'s
exact chrome (`TopBorder`/`BoxLine`/`Divider`/`ScrollWindow`/`StyleRow`/
`BottomBorder`) but adds selection (`Enter` chooses the row under the
cursor and returns it; `q`/`ctrl+c` cancels) and, per the user's explicit
request, incremental filtering from the start ("this allows someone to
go directly to the thing they want if they know the name or part of the
name") — `/` enters filter-typing mode (matching the keybinding table
and `huh.Select`'s own default, not an always-on type-ahead that would
collide with `j`/`k` navigation), narrows by case-insensitive substring
match, `Esc` clears it. Works on pre-rendered rows, returns an index
(not a typed value) so `internal/tui` doesn't need generics — the same
pattern `pickS3MenuItem` already uses for `s3MenuItems`.

Per the user's request, DESIGN.md's taxonomy now includes a concrete
map: every current `ui.PickList` call site that selects one instance of
a fetched resource (bucket, EC2 instance, AMI, key pair, subnet, region,
role, lifecycle rule, storage class — ~25 call sites across Compute, Key
Management, and S3), each with its file:line and status. S3 buckets are
the pilot (Phase 20.4, converting now); everything else is explicitly
listed as not-yet-scheduled rather than silently left for someone to
rediscover later. Guide-menu-shaped choices (small, fixed option sets —
domain/action menus, Instance-vs-AMI kind pickers, the tag action menu,
remediation choices) are explicitly excluded from this map; they stay on
`PickList`/`huh.Select` since they're not selecting an instance of a
resource collection.

**Rejected alternatives.**
- *Convert bucket-selection to `huh.Select` as originally planned* —
  works functionally, but reintroduces the exact visual inconsistency
  this session's termlib-deprecation work exists to eliminate; huh's
  `Select` field can't be restyled to match `internal/tui`'s chrome
  without forking huh.
- *A `Selectable bool` flag on `ListViewModel` instead of a separate
  `PickerModel`* — would avoid a second component, but conflates two
  different interaction models (pure read-only browsing vs. choose-and-
  return) in one type, against this project's established preference
  for small, purpose-built components (the same reasoning that already
  kept the List tier separate from `filemanager.Model`).
- *Defer filtering, add it later if a list turns out to need it* — this
  session's own default instinct (matching how `ListViewModel` itself
  shipped without filtering, since bucket counts are usually small) —
  overridden here because the user asked for it explicitly and gave a
  concrete rationale (typing a known name/substring beats scrolling
  through a long AMI or instance list), not a hypothetical future need.

**Consequences.** Phase 20.4 is retargeted: bucket-selection converts to
`tui.RunPicker`, not `huh.Select`. New PLAN.md Phase 20.8 builds
`PickerModel` itself (a dependency of Phase 20.4). `object_browser.go`'s
existing `huh.Select`-based bucket pre-flight is unaffected by this
decision for now — revisiting it to also use `PickerModel` is a
separate, not-yet-scoped question, not implied automatically by this
one.

**Implemented 2026-07-10, same session** (PLAN.md Phase 20.8):
`PickerModel` built test-first. Two things worth keeping on record: (1)
filtering must pin the rendered content area to the *unfiltered* row
count, not however many rows currently match, or the box's height
shrinks/grows while typing a filter and reproduces the same inline-
rendering hiccup the List tier already found for exact/changing frame
heights — confirmed by an actually-failing test, fixed by padding to a
stable height (also better UX, `fzf`-style). (2) Two filter tests hit a
second, distinct `teatest` gotcha already documented in this codebase
(bubbletea skips retransmitting unchanged lines between consecutive
frames, so checking the same text across two separate `WaitFor` calls
can race) — fixed the same way `internal/filemanager`'s tests already
do: combine assertions into one `WaitFor`.

Phase 20.4 (bucket selection) also implemented, same session: each of
the three call sites (`ConfigureBucketWebsite`, `ManageBucketLifecycle
Policies`, `DeleteBucket`) now calls a shared `pickBucket` helper, then
delegates to an unexported, directly-testable core taking the resolved
bucket. `cancelledIsNil` recognizes `tui.ErrCancelled` alongside
`ui.ErrCancelled`. Full detail: PLAN.md Phase 20.4.

---

## 2026-07-10 — Deprecate termlib; standardize on huh/bubbletea before 0.0.2; drop screen-reader/accessible-mode as a TUI requirement

**Context.** Working through the S3 menu's huh.Select conversion and the
paged bucket-list display (both same-day, below) surfaced a values
question worth having directly rather than deciding by accretion: what
is clasm actually *for*, and what does that imply about its UI? The
user's framing: clasm exists to give this team a fluid alternative to
AWS's own web console (which they find bad and getting worse) and to
one-off Bash scripts — but that alternative only works if colleagues can
learn it in one sitting, which requires every screen to look and behave
like the same tool rather than a collection of differently-styled
prompts. Reviewing `internal/filemanager/view.go` directly (not just
recalling its design) found that its box-drawing/legend/scrolling code
is already pure functions with no dependency on `filemanager.Model`, and
that `'q'` already means quit there (`case "ctrl+c", "q": m.quitting =
true; return m, tea.Quit`) and it already renders inline (no
`tea.WithAltScreen`) — meaning the "consistent chrome, consistent keys"
goal is mostly already *in* the codebase, just not applied project-wide.
Separately, `termlib.Terminal.UpdateTerminalSize` was found to hardcode
`os.Stdout.Fd()` regardless of what writer a `Terminal` was actually
constructed with — a real defect for anything wanting genuine
terminal-height-aware sizing, and a concrete symptom of `termlib` being
a poorer fit than `bubbletea`'s own `tea.WindowSizeMsg` (sent to
`Update` once at start and again on every resize) for where this tool is
headed.

**Decision.** `termlib` is removed entirely before 0.0.2. All TUI
surfaces converge on `huh` (guide menus, action wizards) and `bubbletea`
(lists, managers) exclusively — see "Terminal UI architecture" and "TUI
keybinding conventions" below for what that looks like concretely.
Screen-reader/non-TTY accessible rendering is explicitly **not** a
requirement for clasm's TUI going forward: it's an internal tool for
Library staff managing AWS resources, not public-facing, distinct from
this workspace's Frontend Guidelines A11y requirement for browser-side
Web Components (unaffected by this decision). In an ideal world the
user would like the whole application to be screen-reader friendly, but
set that aside as not a hard requirement once it was clear it was in
real tension with the visual-consistency goal above.

**This explicitly supersedes** (left in place as accurate history, not
deleted or rewritten):
- "0.0.1 scope: ship on termlib as-is; postpone CloudFront and the
  UI/UX overhaul" (2026-06-30/07-09 era) — huh is no longer merely "the
  leading candidate for the next release"; it and bubbletea are now the
  committed direction, with a `termlib` removal target (before 0.0.2),
  not an open evaluation.
- "huh fields are pipe-testable via WithAccessible(true).WithInput/
  WithOutput" (2026-07-10, earlier the same day) — remains factually
  correct (the mechanics were verified against real huh source) but is
  no longer load-bearing for design decisions, since accessible-mode
  compatibility is no longer a goal. Testing anything built as a real
  `bubbletea` component going forward uses `teatest` instead (already
  proven against `internal/filemanager`'s `Model` in Phase 20.1), not
  huh's accessible-mode pipe pattern.
- "Decouple the S3 menu from resource-list display; add a generic paged
  table to internal/ui" (2026-07-10, earlier the same day) —
  `internal/ui.PagedTable`/`DisplayBuckets`, implemented and shipped
  earlier today, are retired in favor of a `bubbletea`-based List-tier
  component (see "Terminal UI architecture" below) less than a day
  after landing. The design was correct given its stated constraint
  (stay accessible); the constraint itself is what changed.

**Rejected alternatives.**
- *Keep termlib for simple prompts, use huh/bubbletea only for more
  complex screens* — rejected because a mixed system is exactly the
  "memorize different command sequences per screen" problem the user is
  trying to avoid; partial consistency isn't the goal, whole-tool
  consistency is.
- *Fix termlib's `os.Stdout`-hardcoding bug and keep using it* —
  rejected: `termlib` is the user's own separate project
  (`~/Laboratory/termlib`), and fixing that one defect wouldn't address
  the deeper mismatch (a blocking-prompt library isn't the right
  foundation for a genuinely chrome-consistent, live-resizing bordered
  UI). `termlib` served its purpose already — see Consequences.
- *Keep accessible-mode support as a stretch goal* — rejected per the
  user directly: nice-to-have in an ideal world, not required for an
  internal staff tool, and in real tension with the visual-consistency
  goal that matters more here.

**Consequences.** `termlib` is credited with having answered three real
design questions this project needed answered before committing to a
final UI approach: how to organize an AWS-management menu system that's
intuitive without deep AWS knowledge; how to make individual actions
(create a bucket, update a policy) quick and easy; and how to keep
workflows structured for future automation of repetitive tasks (see
"Structure workflows for future record/replay ('Recorded Scripts')",
2026-07-01 — unaffected by this decision; that design was already
UI-toolkit-agnostic by construction, via the params-struct/execute
split). Having answered those, it's no longer needed. The remaining
~40 `termlib`-based call sites are not converted all at once — see
"Terminal UI architecture"'s "Not decided yet" for pacing.

---

## 2026-07-10 — Terminal UI architecture: menu → action/list/manager taxonomy; shared internal/tui chrome package

**Context.** Direct follow-on to the decision above: once `huh`/
`bubbletea` are the committed direction, what's the concrete shape of
"every screen looks and behaves like the same tool"? Full design:
DESIGN.md, "Terminal UI Architecture: Menus, Actions, Lists, and
Managers."

**Decision.**
- Every navigation path resolves to one of three destinations reached
  through a menu that is never itself a destination: **guide menu**
  (`huh.Select` today), **action wizard** (a short prompt sequence that
  gathers parameters and executes one thing), **list** (a read-only
  scrollable resource display), or **manager** (a persistent stateful
  screen, e.g. the S3 object manager).
- New `internal/tui` package: the file manager's already-pure
  box-drawing/scroll/style helpers (`topBorder`, `bottomBorder`,
  `divider`, `splitDivider`, `mergeDivider`, `boxLine`, `boxRow2`,
  `padOrTruncate`, `runeLen`, `stripANSI`, `truncateVisible`,
  `scrollWindow`, `styleRow`) move there unchanged, and
  `internal/filemanager` imports them instead of keeping its own copy.
  `internal/ui` stays in place for as long as termlib-based call sites
  remain, shrinking over the course of the termlib removal rather than
  being replaced in one step.
- New List-tier component in `internal/tui`, replacing
  `internal/ui.PagedTable`/`DisplayBuckets`: single bordered box, frozen
  header row, scrollable body via the shared `scrollWindow` logic, sized
  to the real terminal via `tea.WindowSizeMsg`, a legend bar, rendered
  inline (no alt-screen). Quitting returns to the menu it was opened
  from (for S3 buckets, the S3 menu — not `ErrBackToDomainPicker`, which
  is one level further up).

**Rejected alternatives.**
- *Keep chrome duplicated per screen* — the drift risk this whole
  decision exists to avoid; one implementation shared via
  `internal/tui` instead of `internal/filemanager` and a new List
  component each maintaining their own box-drawing.
- *Build the List tier as a variant of `filemanager.Model`* — rejected:
  that `Model` carries file-manager-specific state (panes, tagging,
  sync, linked local directories) that's the wrong shape for a plain
  read-only list; a dedicated, smaller `internal/tui` component matches
  this project's existing preference for small, purpose-built pieces
  over one component doing everything (same reasoning as keeping
  `Confirm`/`ConfirmDestructive` separate from `PickList`).

**Consequences.** `internal/filemanager` is refactored to import
`internal/tui`'s helpers with no behavior change (its existing
`teatest`-based test suite should continue to pass unmodified — a
regression there would mean the extraction wasn't actually behavior-
preserving). `internal/ui.DisplayBuckets`/`PagedTable` and their tests
are retired.

---

## 2026-07-10 — TUI keybinding conventions: q=back everywhere, arrows/j-k navigate, Enter=select, Esc cancels only an in-progress step, persistent legend bar

**Context.** Direct follow-on to the two decisions above: a concrete,
approved keybinding table so "consistent commands throughout the
application" means something specific enough to implement and test
against, not just a stated goal. Drafted, then approved by the user
with no corrections.

**Decision.**

| Key | Action | Where |
|---|---|---|
| `q` | Back to the parent screen | Everywhere |
| `↑`/`↓`, `k`/`j` | Navigate / scroll | Menus, lists, managers |
| `Enter` | Select / confirm / submit | Menus, lists, wizards |
| `Esc` | Cancel the *in-progress* action only — never closes a screen | Wizards, in-progress input |
| `/` | Filter | Menus, lists, managers |
| Legend bar | Always visible at the bottom of every screen, showing that screen's actual keys | Every screen |

Mostly formalizes precedent already in the codebase rather than
inventing new bindings: `'q'`/`ctrl+c` already quit the file manager;
huh's own `Select` default keymap already binds `↑/k`, `↓/j`, and `/`
for filter; `Esc`-cancels-not-closes matches the earlier "quit vs.
cancel" wording note (TODO.md, "UX improvements," 2026-07-09). The one
place this can't be applied uniformly: `huh.Select`'s own footer is
built solely from the focused field's `KeyBinds()`
(`SelectKeyMap` has no quit/back entry, and `KeyBinds()` isn't
overridable without forking huh), so menus get `q` bound at the `Form`
level (`Form.WithKeyMap`, `KeyMap.Quit` gains `"q"` alongside
`"ctrl+c"`) plus a separately-printed static hint line above the menu,
rather than a real legend-bar entry. List and manager tiers, which own
their full rendering, show `q` in an actual legend bar.

**Consequences.** `RunS3Menu`'s `huh.Select` gains `q` as an additional
`Quit` trigger, resolving through the already-existing
`mapS3MenuPickerErr`/`ErrUserAborted`→`ErrBackToDomainPicker` path — no
new dispatch logic. "Back to domain picker" is removed from
`s3MenuItems` (redundant with `q`); the `choice.action == nil` branch in
`runS3Menu` becomes dead code and is removed with it. "Show resource
lists" is relabeled "List S3 Buckets" (clearer, and matches what it
actually does once it's a dedicated List-tier screen rather than a
Refresh-and-print action) — label only, not the underlying
`S3Actions.ShowResourceLists` Go identifier, kept as-is since renaming
it carries no user-facing benefit and only adds diff noise.

**Implemented 2026-07-10, same session** (PLAN.md Phase 20.7): exactly
as scoped above. huh's own footer still can't show `q` (confirmed
against source, as expected), so a static `"(q to go back)"` hint is
printed via the existing `t.Println`/`t.Refresh()` before each menu
redisplay. Tests exercising "one action dispatch, then the loop ends"
were rewritten around a `context.WithCancel` + cancel-from-within-the-
test-action-closure pattern, since there's no longer a "Back to domain
picker" menu choice to select and accessible mode can't simulate the
`q`/ctrl+c abort that replaces it (matching `mapS3MenuPickerErr`'s
already-documented limitation). `go build`/`go vet`/`go test ./...
-race`/`gofmt -l` all clean. The `q`-binding's actual effect (does
pressing `q` really abort the `Select`) can only be confirmed by real
interactive use — noted, not yet done, same class of gap as this
session's other `huh`/`bubbletea` work.

---

## 2026-07-10 — Decouple the S3 menu from resource-list display; add a generic paged table to internal/ui

**Context.** Following the `RunS3Menu` huh.Select conversion (below),
the user pointed out a UX problem exposed by that same code path: every
successful S3 menu action triggers `actions.Refresh(ctx)`, and
`refreshS3` (`cmd/clasm/main.go`) both re-fetches bucket data and prints
the *entire* bucket table (`ui.DisplayBuckets`) every time — so the S3
menu redisplay is cluttered with a resource list after every action, not
just when "Show resource lists" is chosen, and that table has no
pagination at all (unlike `ui.PickList`'s existing 50-item paging), so
it would print unboundedly for a large bucket count. Requested fix: the
S3 menu should show only the menu; "Show resource lists" becomes its
own dedicated, paged display with the column titles visible on every
page, and `n`ext/`p`revious/`q`uit navigation (`q` returns to the S3
menu). A mockup was drawn up and approved before any code was written,
per this project's design-before-code process for non-trivial changes.

**Decision.** Full design recorded in DESIGN.md, "S3 Resource List
Display — Paged, Accessible-Compatible" (2026-07-10), and PLAN.md Phase
20.3. Key points carried here for the decision record:
- Split "refresh" into re-fetching data (unchanged, still happens after
  every action) versus *displaying* it (now only on explicit "Show
  resource lists").
- New generic `internal/ui` component (`PagedTable`, PLAN.md Phase
  20.3), not a bucket-specific one: takes a banner-format callback and
  pre-rendered header/row strings, owns only windowing, chrome, and
  `n`/`p`/`q` input. `DisplayBuckets`'s existing `PadRight`/`Truncate`
  column formatting is reused to build the strings passed in.
  Deliberately generic **so Compute/Key Management's own resource
  listings can reuse the same mechanism later**, if/when those menus are
  migrated — not part of this piece of work, but designed not to
  preclude it, per the user's own framing ("we'll reuse this UI approach
  as needed migrating to huh for other parts of clasm").
- Stays fully accessible: sequential printing only (banner, header,
  page of rows, command prompt, read one line, repeat or return) — no
  cursor repositioning, so behavior is identical over a real TTY,
  `TERM=dumb`, or a piped input/output pair in tests. Note this
  mechanism doesn't involve `huh` at all -- it's the same plain
  `termlib`/`LineEditor.Prompt` style `PickList` already uses; paging a
  resource list and "migrating to huh" are orthogonal concerns here.

**Rejected alternatives.** See DESIGN.md's addendum for the full list
(unpaginated-with-scrollback, a `huh`/`bubbletea`-style redraw-in-place
viewport, and reusing `PickList` directly) — each rejected for the
reasons recorded there; not repeated here to avoid drift between the
two documents.

**Consequences.** `ui.DisplayBuckets` is replaced by a `PagedTable` call
site (its signature changed: now takes `le` and returns `error`, its
only call site updated); `refreshS3` splits into a silent-refresh half
and a separate `showS3ResourceLists` closure; `s3MenuItems`' "Show
resource lists" entry calls a new `S3Actions.ShowResourceLists` field
instead of `Refresh`. Compute/Key Management's current unpaginated
`DisplayInstances`/`DisplayImages`/`DisplayKeyPairs` are explicitly
unchanged — ask before extending this pattern to them.

**Implemented 2026-07-10, same session, test-first** (PLAN.md Phase
20.3): `internal/ui/paged_table_test.go` was written and run against a
stub before `paged_table.go` existed, then `PagedTable` was implemented
to make it pass; `display_test.go`'s two `DisplayBuckets` tests were
updated for the new signature plus a new pagination test; a real test
gap was caught and closed along the way — no existing `s3_menu_test.go`
case exercised choosing "Show resource lists" (menu item 1) at all,
before or after this change, so a `TestRunS3Menu_ShowResourceListsDispatchesToItsOwnAction`
was added. `go build`, `go vet`, and `go test ./... -race` all clean.

---

## 2026-07-10 — huh fields are pipe-testable via WithAccessible(true).WithInput/WithOutput

**Context.** `internal/workflow/object_browser.go` (the only existing huh
usage) has zero test coverage. huh's real interactive `Field.Run()`/
`Form.Run()` isn't pipe-testable the way `termlib`'s `newPipeEditor`
pattern is (see `confirm_test.go`). The prior session confirmed huh's
accessible-mode text format from source but left untried whether
`huh.NewForm(...).WithAccessible(true).WithInput(r).WithOutput(&buf).
Run()` -- forcing accessible mode explicitly rather than relying on
`TERM=dumb` auto-detection -- gives a clean, reliable pipe-testable
path. This had to resolve before converting `RunS3Menu` and the three
bucket-selection call sites (the next piece of work) to more untested
huh code.

**Decision.** Yes -- confirmed by direct experiment (a scratch test file,
written, run, and deleted this session; `go test`/`go vet` clean
throughout). `huh.NewForm(huh.NewGroup(field...)).WithAccessible(true).
WithInput(r).WithOutput(&buf).Run()` drives `Select`, `Confirm`, and
multi-field groups correctly from a plain `io.Reader`/`io.Writer` pair,
with no terminal or `bubbletea` program involved -- `Form.RunWithContext`
branches straight to `Form.runAccessible`, which calls each field's own
`RunAccessible` in turn. This is the pattern to use for the
S3-menu/bucket-selection conversions and retroactively for
`object_browser.go` -- **with one correction to this same session's
earlier finding**, below.

Confirmed behavioral details worth keeping in mind writing those tests:
- **Correction, found while implementing the `RunS3Menu` conversion:**
  `r` must NOT be a `strings.NewReader`-style reader that returns
  everything it has in one `Read` call. `accessibility.PromptString`
  builds a brand-new `bufio.Scanner` on every single `RunAccessible`
  call (there's no persistent, reused scanner the way `termlib`'s
  `LineEditor` has one for its whole lifetime) -- so if the *first*
  field's `Read` call greedily returns every remaining byte (as
  `strings.Reader.Read` does when its buffer is smaller than the
  request), that Scanner buffers and then discards everything past the
  first newline when it returns, silently starving every field after it
  -- both across two separate `Form.Run()` calls in a loop (as
  `RunS3Menu` makes, once per menu redisplay) and within a single
  multi-field `Form` (as `object_browser.go`'s three-field pre-flight
  makes). This was NOT caught by this session's first three
  experiments, because each one's second/third field happened to
  assert a value that coincided with that field's own zero-input
  default -- a false-positive risk worth flagging on its own. Confirmed
  the actual bug with a repro using a *non-default* expected second
  value, and confirmed the fix: use a reader that returns at most one
  newline-terminated line per `Read` call (matching how a real terminal
  in canonical mode delivers input, one `Read` per Enter keypress) --
  implemented as `lineAtATimeReader`/`newHuhAccessibleInput` in
  `internal/workflow/huh_accessible_test.go`, reusable across every huh
  call site's tests. Use `newHuhAccessibleInput(s)`, never
  `strings.NewReader(s)`, when feeding more than one field/prompt
  through this pattern.
- `Select.RunAccessible` reprompts on out-of-range input, writing
  `"Invalid: must be a number between %d and %d"` before re-asking --
  same reprompt-until-valid shape as `newPipeEditor`'s `Confirm` tests.
- A `Select` field backed by a pointer `Value(&v)` accessor has an
  implicit default (its initially-selected option, normally index 0),
  which `PromptInt` returns on a blank line -- so premature EOF is not
  the same as "no value set"; it silently resolves to that default
  rather than erroring. Confirmed via `accessibility.PromptString`'s own
  comment: `"no way to bubble up errors or signal cancellation ... but
  the program is probably not continuing if stdin sent EOF"`. Test
  input must supply one complete line per field; don't rely on EOF as
  an error signal, and don't assert a value that coincides with this
  default (see the correction above -- it masks real bugs).
- `Form.runAccessible` discards each field's own `RunAccessible` error
  return (`_ = field.WithAccessible(true).RunAccessible(w, r)`) --
  harmless in practice since each field's own `RunAccessible` already
  loops internally until it gets a valid value, so it only returns once
  successful.
- `huh.ErrUserAborted` (the normal-mode Ctrl-C/Esc signal that
  `huhCancelledIsNil` maps to a clean return) has no accessible-mode
  equivalent -- there is no keyboard to interrupt a plain
  `io.Reader`/`io.Writer` pair, so cancellation-path tests for
  huh-backed menus need a different signal than "user aborted" if one
  is needed at all (see "Convert RunS3Menu to huh.Select" below for how
  this was handled: unit-test the abort-mapping as a standalone pure
  function instead of through the pipe path).

**Rejected alternatives.**
- *Rely on `TERM=dumb` auto-detection instead of explicit
  `WithAccessible(true)`* -- works too (`NewForm` sets it automatically
  when `TERM=dumb`), but requires mutating the test process's
  environment (`t.Setenv("TERM", "dumb")`), which is less explicit and
  risks leaking into unrelated parallel tests; calling
  `WithAccessible(true)` directly is scoped to the one `*Form` under
  test.
- *Test only via `Field.RunAccessible` directly, skip `Form`* --
  considered, since `Field.RunAccessible` is itself public and
  no longer just an internal implementation detail (`WithAccessible`
  is now deprecated in its favor per the huh v1.0.0 source). Rejected
  for the *multi-field* menu/pre-flight tests specifically, since
  `RunS3Menu`'s conversion and `object_browser.go`'s existing pre-flight
  both group multiple fields in one `huh.Group`, and `Form` is what
  sequences them in production code -- testing through `Form` keeps the
  test closer to the real call path.

**Consequences.** Unblocks converting `RunS3Menu`
(`internal/workflow/s3_menu.go`) and the three `ui.PickList`
bucket-selection call sites
(`bucket_website.go`/`bucket_lifecycle.go`/`bucket_delete.go`) to
`huh.Select` with test coverage from the start, and backfilling
`object_browser.go`'s existing zero coverage using the same pattern.
No production code changed by this decision -- it's a testing-approach
resolution only.

---

## 2026-07-10 — Convert RunS3Menu to huh.Select; select by index, not by s3Item

**Context.** `continue_next_time.txt`'s next-up item, now unblocked by
the pipe-testability resolution above: convert `RunS3Menu`
(`internal/workflow/s3_menu.go`), the S3 domain's 7-item action picker,
from `ui.PickList` to `huh.Select`. Two problems came up that the prior
session's scoping didn't anticipate:

1. `huh.Select[T]` requires `T comparable` (`Option[T comparable]`,
   confirmed from source), but `s3Item` holds an `action func(S3Actions,
   context.Context) error` field -- funcs aren't comparable, so
   `huh.Select[s3Item]` doesn't compile.
2. Converting the picker changes what "cancel/abort the menu" does.
   `ui.PickList`'s dedicated "0) Cancel" numbered option returned
   `ui.ErrCancelled`, which `isExitSignal` recognizes -- so pressing 0
   exited the whole program. `huh.Select` has no such numbered-cancel
   convention; its only cancellation signal is `huh.ErrUserAborted`
   (Ctrl-C/Esc), which `isExitSignal` does NOT recognize, so left
   unhandled it would propagate out of `RunS3Menu` entirely and still
   exit the whole program -- not the behavior the prior session asked
   for ("change this so aborting the newly-huh-converted top-level S3
   menu returns `ErrBackToDomainPicker` instead").

**Decision.**
- Select by `int` (index into `s3MenuItems`), not by `s3Item`:
  `pickS3MenuItem` builds `huh.Option[int]` from each item's label and
  its index, runs the `Select`, then looks up `s3MenuItems[idx]` after.
  Sidesteps the comparability constraint without changing `s3Item`'s
  shape (still holds a `func`, used everywhere else in this file).
- `RunS3Menu`'s exported signature is unchanged
  (`ctx, t, le, actions`), matching `RunMainMenu`/`RunKeyMgmtMenu`'s
  shape -- it now delegates to an unexported `runS3Menu(ctx, t, actions,
  menuInput, menuOutput)`. `le` is accepted but unused (huh doesn't read
  through it); a doc comment says so explicitly rather than leaving a
  reader to wonder if that's a bug. `menuInput`/`menuOutput` are nil in
  production (the picker runs interactively via `pickS3MenuItem`'s bare
  `form.Run()`, same as `object_browser.go`'s existing call sites) and
  are supplied by tests to drive the exact same `huh.Select` through the
  accessible-mode pipe path instead -- keeping the tested path identical
  to the production path, not a parallel fake.
- `huh.ErrUserAborted` from the picker maps to `ErrBackToDomainPicker`
  via a new pure function, `mapS3MenuPickerErr`, rather than an inline
  check -- because accessible mode (the only path integration tests can
  drive; see the pipe-testability entry above) has no way to produce
  `huh.ErrUserAborted` at all, this mapping can only be covered by
  calling the pure function directly with a synthetic error, not by
  driving `runS3Menu` end-to-end. Said so explicitly (this note) rather
  than shipping the mapping uncovered.
- `s3_menu_test.go`'s old `TestRunS3Menu_CleanExitOnCancelledPickList`
  (input `"0\n"`, asserting a clean whole-program exit) tested a
  `PickList`-specific affordance that no longer exists and asserted the
  *old*, now-deliberately-changed behavior -- removed rather than kept
  as a skipped/misleading test. Its replacement is
  `TestMapS3MenuPickerErr`.
- Every other `s3_menu_test.go` case kept its exact input strings
  (`"2\n7\n"`, `"3\n7\n"`, etc.) -- `huh.Select`'s accessible-mode
  1-indexed numbering happens to match `s3MenuItems`' order exactly, no
  renumbering needed. They now call `runS3Menu` directly (unexported)
  instead of `RunS3Menu`, with a `newTermOnly()` helper in place of
  `newPipeEditor` (no `LineEditor`/pipe needed for `t` now that the
  picker doesn't read through it) and `newHuhAccessibleInput` in place
  of raw strings for the menu's input.

**Rejected alternatives.**
- *Keep `isExitSignal`'s current "abort exits the whole program"
  behavior, defer the wording fix to a later pass* -- rejected because
  the prior session scoped the abort-behavior change as part of this
  same piece of work, not a separate one (this repo's own task list
  keeps the label-wording pass, e.g. "quit" vs. "cancel" text,
  separate, but the *behavior* change was explicitly bundled here).
- *Give `s3Item` an `Equal` method or make `action` a comparable
  reference (e.g. an int action-code) instead of switching to
  index-based selection* -- more invasive (every existing
  `s3MenuItems` literal and every call site that pattern-matches on
  `choice.action == nil` for "Back to domain picker" would need to
  change); index-based selection achieves the same result by touching
  only `pickS3MenuItem`.
- *Drop the now-unused `le` parameter from `RunS3Menu`* -- would ripple
  into `main.go`'s one call site for no functional benefit, and would
  make `RunS3Menu`'s signature diverge from `RunMainMenu`/
  `RunKeyMgmtMenu`'s while those two remain on `termlib`. Deferred until
  (if ever) all three domain loops are off `termlib`'s `PickList`.

**Consequences.** `internal/ui` (`PickList`, `ErrCancelled`) is
untouched and still used by `RunMainMenu`/`RunKeyMgmtMenu`/
`RunDomainPicker` -- this conversion is scoped to the S3 menu only, per
the prior session's explicit "don't let this expand" note. `go build`,
`go vet`, and `go test ./... -race` are clean. Next up, unstarted: the
three bucket-selection call sites
(`bucket_website.go`/`bucket_lifecycle.go`/`bucket_delete.go`), which
select `inventory.Bucket` values (already comparable, no func fields)
so shouldn't need the same index-based workaround.

---

## 2026-07-09 — Fix stale Find results after a refresh; name single targets in confirm prompts; add an explicit manual refresh

**Context.** Real-bucket testing (`test-clasm`) surfaced three more gaps
in the same session: after tagging and deleting some `.jsonl` objects
located via Find, the listing didn't reflect the deletion until some
later, unclear point; the delete confirm for a single object only said
"1 object(s)," not which one; and there was no direct answer to "how do
I get the window to update."

**Root cause 1 (stale Find results).** `pane.visible()` returns
`pane.find.results` -- a point-in-time flat snapshot -- whenever a Find
is active, completely bypassing `pane.entries` regardless of how
recently `entries` was refetched. The post-delete refresh
(`refreshAfterAction`) correctly reloaded `p.entries`, but the screen
kept showing the old Find snapshot until the operator manually pressed
Esc to exit Find. Fixed in the `listLoadedMsg` handler
(`model.go`): a successfully-applied (non-stale) listing for a pane now
always clears that pane's `find` too, since a fresh full-level listing
supersedes any earlier Find snapshot. Safe to do unconditionally: the
only loads that ever reach a pane while its `find` is still set are
post-action refreshes (ordinary navigation already clears `find` via
`pane.enter`), so there's no legitimate case where a landing reload
should leave a stale Find view in place.

**Root cause 2 (single-target confirms didn't name the target).**
Download/Upload/Delete/Sync's confirm prompts all read "N object(s)"/
"N file(s)," even for exactly one item -- a regression relative to
Feature 21's original single-object delete wizard, which did name the
object (`"Delete %s from %s?"`). Added `describeTargets`/`describeKeys`
(actions.go/sync.go): a single item's key/path is named directly;
multiple items still get a count. Applied to all four confirm prompts
(Download, Upload, Delete, Sync's two stages) for consistency, not just
the one the operator specifically flagged.

**Decision -- explicit manual refresh.** Even with root cause 1 fixed,
added `r` / `:refresh` (reloads the focused pane's current level,
clearing any active Find first) as a direct, always-available answer to
"how do I get the window to update" -- covers any future staleness
class this fix didn't anticipate, and covers the legitimate case of
something changing the bucket from outside this session entirely (the
AWS console, another terminal).

**Also clarified in this exchange, no code change needed:** switching
between one-pane and two-pane views is `l` -- prompts to link a
directory (opens double-pane) when unlinked, or goes straight to the
unlink confirm (collapses to single-pane) when already linked. The
hotkey legend now says "l Link (2-pane)" / "l Unlink (1-pane)" instead
of just "l Link"/"l Unlink" to make the pane-count effect explicit.

**Rejected alternatives.**
- *Prune only the deleted keys out of `find.results` instead of
  clearing `find` entirely* -- considered (would preserve the rest of
  the Find context); rejected for now as more moving parts than the
  bug warranted -- reopening the same search (`F`/`:find`) after a
  refresh is one keystroke, and the general "any landing reload clears
  find" rule is simpler to reason about and correct for every action,
  not just Delete.

**Consequences.** `model.go`'s `listLoadedMsg` handler, `actions.go`
(`describeTargets`, Download/Upload/Delete confirm titles, `r` hotkey,
`refreshFocused`), `commandline.go` (`:refresh`), `sync.go`
(`describeKeys`, both confirm titles), and `view.go` (legend wording)
all changed. New tests:
`TestModel_Delete_FromFindResultsRefreshesDisplay`,
`TestModel_Refresh_HotkeyReloadsFocusedPane`,
`TestModel_Refresh_ColonCommand`. Existing Download/Upload/Sync confirm-
title assertions updated to match the new single-item phrasing. All
tests pass; `go test -race ./...` clean.

---

## 2026-07-09 — Add per-call AWS timeouts to the file manager; add a direct unlink-to-single-pane action

**Context.** The file manager "appeared hung" after uploading a batch
of files to a real bucket. Investigated live: attached Delve
(non-destructively -- inspected goroutine stacks, then fully detached,
leaving the process running and untouched) to the running process
rather than guessing. Found, at that moment, a genuinely in-flight
`s3:PutObject` HTTP request -- not a deadlock in this project's own
code. By a second snapshot ~10 seconds later that request had completed
normally and the whole program was sitting in bubbletea's ordinary idle
event loop. The operator confirmed pressing a key brought the screen
back -- it had finished the batch and was showing the progress
overlay's "(press any key to continue)" state (DESIGN.md 21.4's
never-auto-dismiss rule), not actually stuck.

**Decision 1 -- add per-call timeouts anyway.** Even though this
specific instance wasn't a true hang, the investigation surfaced a real
gap: every direct AWS call in `internal/filemanager`/`internal/s3diff`
used the caller's own long-lived context with no per-call deadline,
unlike `internal/workflow`'s established `withCallTimeout` (30s)
convention (`call_timeout.go`) used throughout the EC2/AMI/Key
Management domains. A *genuinely* stalled connection (not just a slow-
but-progressing one) would hang the calling goroutine forever, with no
recovery short of killing the whole program. Added
`s3diff.WithCallTimeout` (30s, for lightweight metadata/listing/delete
calls) and `s3diff.WithTransferTimeout` (5 min, for Upload/Download's
actual data-transfer calls, since transfer time scales with object size
and connection speed, not just request/response latency) -- duplicated
from `internal/workflow`'s pattern rather than imported, for the same
import-cycle reason `internal/s3diff` itself exists (see the earlier
"Extract internal/s3diff..." entry). Applied at every direct
`ListObjectsV2`/`HeadObject`/`GetObject`/`PutObject`/`DeleteObject` call
site in both packages. `downloadOne`'s timeout has to span the whole
download (GetObject call + the later `io.Copy` of its response body),
not just the initial call, since the response body read is governed by
the same request context. While here, replaced `actions.go`'s
`uploadOne` (a near-duplicate of `s3diff.UploadFile` that was missing
Content-Type inference) with a direct call to `s3diff.UploadFile` --
one less duplicate implementation, and the Upload action now gets
correct Content-Type headers Sync's Upload already had.

**Decision 2 -- direct unlink action.** Separately reported: "I need a
way to go from two panels back to displaying only the S3 bucket." The
`l` hotkey's existing unlink path (open `:link <path>` pre-filled,
clear it, submit empty) was reachable but not discoverable as *the* way
back. `l` while linked (or `:unlink`) now goes straight to a Confirm
("Unlink `<path>` and return to single-pane view?"); accepting applies
instantly (`applyLink("")`) since unlinking is a state change, not a
background operation -- it never touches `beginAction`/
`overlayProgress`. `:link` (with an explicit empty argument) still
unlinks too, unchanged, so nothing that depended on the old path breaks.

**Rejected alternatives.**
- *Use the same 30s timeout for Upload/Download as everything else* --
  rejected; a large file's legitimate transfer time can easily exceed
  30s on a slow connection, and the goal is recovering from a stalled
  connection, not penalizing a slow-but-working one.
- *Make DefaultCallTimeout/TransferCallTimeout const, matching
  workflow's own const* -- rejected; kept as `var` specifically so
  tests can shrink them and prove the recovery behavior without an
  actual 30-second (or 5-minute) wait.

**Consequences.** `internal/s3diff.go` gained `WithCallTimeout`/
`WithTransferTimeout` (+ tests proving a stalled fake connection
recovers via timeout rather than hanging).
`internal/filemanager/{listing,actions,sync}.go` apply them at every
AWS call site; `uploadOne` is gone, replaced by `s3diff.UploadFile`.
`internal/filemanager/{commandline,actions}.go` gained
`startUnlinkConfirm`/`actionUnlink` and the `:unlink` command; the
hotkey legend now reads "l Unlink" once linked. New tests:
`TestModel_Unlink_LHotkeyGoesStraightToConfirm`,
`TestModel_Unlink_DeclineStaysLinked`,
`TestModel_Unlink_ColonCommand`. All pre-existing tests pass unchanged;
`go test -race ./...` clean.

---

## 2026-07-09 — Give Filter the same "/"-anchored pattern convention as Find

**Context.** Typing `/index.html` into the current-level Filter (`f`)
had no visible effect: Filter is (correctly) a plain substring match,
so the literal text `/index.html` -- including the slash -- was
compared against basenames like `index.html`, which never contains a
`/` and so never matched. The operator's expectation, reasonably, was
that the `/`-anchor convention just added to Find (the previous
2026-07-09 entry) would mean the same thing here too, since both
features are typed the same way (a pattern following a `/`) and are
listed next to each other in the hotkey legend.

**Decision.** A filter starting with `/` is now matched via
`globMatch`'s anchored form -- reusing the exact function Find already
uses, not a second implementation -- instead of plain substring
`Contains`. Since Filter only ever operates on one already-fetched
level (not a recursive path), this collapses to an exact/glob match of
the current level's basenames: `/index.html` matches only a file named
exactly that, not `myindex.html5` (which a plain substring filter for
`index.html` would have matched too). Filter without a leading `/`
keeps its original substring behavior unchanged.

**On the spinner:** confirmed with the operator that Filter correctly
shows no spinner -- it's a synchronous, instant operation over already-
loaded rows, unlike Find's recursive scan, so there's nothing to
animate. Not a bug; Find's spinner (previous entry) already covers the
one operation here that can genuinely take a while.

**Consequences.** `pane.visible()` branches on a leading `/` before
falling back to substring matching. New test:
`TestPane_Visible_AnchoredFilterMatchesExactBasenameOnly`
(`pane_test.go`). All pre-existing tests, including
`TestModel_Filter_NarrowsCurrentLevel` (the original substring case),
pass unchanged.

---

## 2026-07-09 — Add a loading spinner and an anchored Find pattern; fix a spinner/synchronous-test-drain interaction

**Context.** Two more requests after trying the file manager against a
real bucket: Find and directory listings can take a real, noticeable
amount of time with no feedback that anything is happening (looks
frozen); and there was no way to search for a root-level file (e.g.
`index.html`) without also matching every same-named file in
subdirectories.

**Decision 1 -- loading spinner.** Added `github.com/charmbracelet/
bubbles/spinner` (already a transitive dependency via huh; now direct).
A pane's header shows an animated glyph + "Loading..." while its
listing is being (re)fetched (`Model.loadingRemote`/`loadingLocal`);
Find's status row shows the same glyph while a search hasn't finished
(`pane.find.done`). The spinner only ticks while `Model.isBusy()` is
true: `loadRemoteCmd`/`loadLocalCmd`/`runFind` each batch in a fresh
`spinner.Tick` to (re)start the animation, and the `spinner.TickMsg`
handler drops its own re-tick `Cmd` once nothing is busy, rather than
ticking forever. This was a real functional requirement, not just
efficiency: a bubbletea `Model` driven synchronously (no real
`tea.Program`, no real timers -- see this project's own test pattern,
`drainCmd`) would never terminate against a perpetually-ticking
spinner.

**Decision 1's follow-on bug and fix.** Even with the isBusy() gate,
`drainCmd`-based tests hung. Root cause: `Init()`'s returned
`tea.Batch` nests one sub-batch per pane
(`loadRemoteCmd`/`loadLocalCmd`, each itself now `tea.Batch(fetch,
tick)`), and `drainCmd` drains one batch branch all the way through
before moving to the next -- so while draining the remote pane's
branch, `loadingLocal` (already set the moment `Init()` *constructed*
the local branch's Cmd, before either branch actually ran) hadn't been
cleared yet, since that happens in the *other*, not-yet-visited branch.
`isBusy()` therefore saw stale state for the entire depth of the first
branch, and kept chasing tick -> tick -> tick forever. A real
`tea.Program` doesn't have this problem: it runs every in-flight `Cmd`
concurrently, so by the time a real spinner tick fires (tens of
milliseconds later), the sibling pane's load has typically already
resolved. Fixed at the test-helper level, not in production code (the
production isBusy()-gating is correct for real concurrent execution):
`drainCmd` now processes a `spinner.TickMsg` once (so `Update`'s
bookkeeping runs) but never chases the `Cmd` it returns -- ticks are
purely cosmetic and don't affect anything a test asserts on.

**Decision 2 -- anchored Find pattern.** A pattern starting with `/` is
now matched against an entry's *full* path (relative to the search's
starting point) instead of just its basename -- `/index.html` matches
only a root-level `index.html`, `/sub/index.html` matches only that
exact nested one, `/*.html` matches only root-level `.html` files
(`filepath.Match`'s `*` still doesn't cross `/`). Implemented as one
branch in `globMatch`, since both the local and S3 recursive listings
(`listLocalRecursive`/`listS3Recursive`) already carry each entry's
full relative path in `entry.name` -- no new traversal or data needed,
just a different match target.

**Rejected alternatives.**
- *Always tick the spinner, never stop* -- the original 2026-07-09 UX
  pass's approach; rejected once it surfaced the synchronous-test-drain
  hang above, and it wastes idle redraws in real usage too for no
  benefit.
- *Fix the hang by having Update clear loadingLocal/loadingRemote
  eagerly at Init() time instead of when their fetch actually starts* --
  rejected; the flags exist specifically to reflect whether a fetch is
  genuinely in flight, and weakening that to work around a test-only
  ordering artifact would make the flags lie in the one case (a
  slow-loading pane) they're supposed to represent correctly.
- *Require the anchor to be a full path with no wildcards (exact
  match only)* -- rejected; reusing plain `filepath.Match` on the full
  path costs nothing extra and lets `/*.html`-style single-level globs
  work too, consistent with the unanchored form already supporting
  globs.

**Consequences.** `internal/filemanager/model.go` gained `spin
spinner.Model`, `loadingRemote`/`loadingLocal`, `setLoading`/
`isLoading`/`isBusy`; `view.go`'s `paneRows` takes `loading bool, spin
string`; `listing.go`'s `globMatch` gained the anchored branch.
`go.mod` moved `github.com/charmbracelet/bubbles` from indirect to
direct. New tests: `box_test.go` (loading/spinner indicator presence),
`entry_test.go`/`model_test.go` (anchored pattern, unit and end-to-end).
`testhelpers_test.go`'s `drainCmd` no longer chases `spinner.TickMsg`
chains. All pre-existing tests pass unchanged; `go test -race ./...`
clean.

---

## 2026-07-09 — Fix two real-bucket navigation bugs and add a scrolling window to the file manager's pane listings

**Context.** Trying the file manager against a real bucket
(`s3://thesis.caltech.edu`) surfaced two more real bugs beyond the
previous UX pass: the object listing had no way to scroll down to reach
an entry past the first screenful (specifically, `opensearch.xml`), and
navigating back up out of a drilled-into subdirectory didn't reliably
reach the root.

**Root cause 1 (no pagination/scrolling).** `View()` never bounded pane
listings to the terminal height at all -- every visible entry became a
box row, unconditionally. A bucket-root listing longer than one
screenful pushed the status line, command line, and hotkey legend off
the bottom of the terminal, with nothing to bring them back into view;
worse, an entry past the first screenful (`opensearch.xml`, sorting
after many `file-*`-style keys) was simply never rendered at all, with
no scroll key or indicator suggesting more existed. Fixed by adding
`paneItemWindowHeight` (derives a row budget from `m.height` minus a
fixed chrome-row count) and `scrollWindow` (keeps the cursor inside a
windowHeight-tall viewport, centering when there's room) -- the listing
now scrolls with the existing Up/Down keys, with a "[a-b of n]"
indicator on the pane header once it doesn't all fit. Overlay progress
logs (Upload/Download/Delete/Sync against many objects) get the same
treatment, tail-windowed rather than cursor-centered, since a log's
natural reading position is its most recent lines.

**Root cause 2 (broken "back to root" navigation) -- two distinct bugs,
both in prefix/path handling:**
- `parentOf` (now `parentOfS3Prefix` for the remote side) stripped the
  trailing slash from a nested S3 prefix's parent (`"logs/sub/"` ->
  `"logs"` instead of `"logs/"`). The next `s3:ListObjectsV2` call then
  used a bare string-prefix match instead of a directory-boundary one,
  silently hiding every bucket-root object that didn't happen to start
  with the literal string `"logs"` -- so "go up a level" landed on a
  corrupted, mostly-empty intermediate view instead of the actual
  parent directory.
- The local pane conflated two different path representations:
  `pane.prefix` is documented (and used by `loadLocalCmd` via
  `joinKey(root, prefix)`) as **root-relative**, but entering a local
  subdirectory assigned the entry's **absolute** filesystem path
  directly to `prefix`. One level deep this went unnoticed; a second
  level built a doubled, malformed path
  (`root + "/" + absolute-path-that-already-contains-root`), breaking
  navigation (including back to the linked root) beyond the first
  subdirectory.

  Fixed with `pane.toPrefix`/`pane.parentPrefixOf`, which convert an
  entry's identity (`entry.key` -- already bucket-relative for the
  remote side, an absolute path for the local side, per `entry`'s own
  doc comment) into the *pane's* prefix representation before it's
  assigned to `pane.prefix`, instead of assuming the two were always
  the same shape.

**Rejected alternatives.**
- *Persist a per-pane scroll offset in the Model, updated on every
  cursor move* -- considered; rejected in favor of computing the
  window as a pure function of `(cursor, total, windowHeight)` on every
  `View()` call. Simpler (no extra mutable state to keep in sync with
  cursor movement, filtering, or directory changes) and just as
  correct, since the window only ever depends on where the cursor
  currently is.
- *Give the local pane its own absolute-path-based prefix format
  instead of converting to root-relative* -- rejected; `pane.label()`
  and every action that reconstructs a full path via
  `joinKey(root, prefix)` depend on `prefix` being root-relative, and
  changing that contract everywhere would be a much larger change than
  fixing the one place (`navigateEnterOrJump`) that violated it.

**Consequences.** `internal/filemanager/entry.go`'s `parentOf` is split
into `parentOfLocal` (unchanged logic, correct for the local side's
no-trailing-slash convention) and `parentOfS3Prefix` (new, trailing-
slash-preserving). `pane.go` gained `toPrefix`/`parentPrefixOf`; `up()`
is now side-aware. `view.go` gained the scroll-window machinery.
New tests: `scroll_test.go` (scroll-window bounds, a 500-item listing
staying bounded to the terminal height, an entry past the first
screenful becoming reachable), `navigation_test.go` (both navigation
bugs, reproduced against the pre-fix code by hand-tracing the exact
failure before writing the fix). All pre-existing tests pass unchanged.

---

## 2026-07-09 — Fix three post-implementation UX gaps in the file manager and its huh pre-flight

**Context.** The user tried the file manager after Phase 20.1 shipped and
reported three real gaps against DESIGN.md: (1) the bucket-picker
pre-flight (huh) gave no indication of what keys/actions were available;
(2) the file manager's pane rows gave no clear visual indication of
which row the cursor was on or which rows were tagged; (3) the screen's
chrome didn't match DESIGN.md 21.4's bordered-box mockup -- it used
plain dashed-line separators instead.

**Root cause 1 (huh help footer missing).** `huh.Field.Run()` (called by
`object_browser.go` for the bucket `Select`, the link `Confirm`, and the
directory `Input`) is a shortcut for `huh.Run(field)`, which is itself
`NewForm(NewGroup(field)).WithShowHelp(false).Run()` -- it explicitly
disables the help footer. Fixed by adding `runFieldWithHelp(field)` (a
one-line `NewForm(NewGroup(field)).Run()`, leaving `Group`'s default
`showHelp: true` in effect) and calling it in place of `field.Run()` at
all three call sites.

**Root cause 2 (no selection indicator).** The pane rows only had a
single leading `>`/`*` character with no color or emphasis -- easy to
miss, especially in a wide terminal. Added reverse-video on the cursor
row and bold on tagged rows (`view.go`'s `styleRow`), gated by the same
NO_COLOR/non-TTY convention `internal/ui.ColorEnabled` already
establishes elsewhere in this codebase (`Model.colorEnabled`, computed
once at `New()`) -- falls back to the plain `>`/`*` markers alone when
color is disabled or stdout isn't a terminal.

**Root cause 3 (chrome didn't match the mockup).** The screen was never
actually built to render DESIGN.md 21.4's bordered-box mockup -- it used
`strings.Repeat("-", 78)` separator lines instead of the mockup's
`┌─┬┐├┼┤└┴┘│` box-drawing chrome. Rewrote `view.go` to render one
continuous bordered box: a title bar (`┌ clasm — S3 File Manager — ... ┐`),
a `┬`/`┴` divider splitting the pane area in double-pane mode, and
`├─┤` rules between the status line/command line/hotkey legend, sized
to the real terminal width (`tea.WindowSizeMsg`, falling back to a
fixed default before the first one arrives). Content within each box
row is padded/truncated to a rune-accurate visible width, correctly
accounting for the invisible ANSI escapes the reverse-video/bold
styling (root cause 2) adds -- verified with a dedicated test asserting
every rendered line between the outer borders has equal visible width.

**Also fixed while verifying this visually:** `joinKey(root, "")`
(used by `pane.label()`) was appending a spurious trailing slash at a
linked directory's root ("LOCAL: /path/on/disk/" instead of "LOCAL:
/path/on/disk") -- an empty `name` now returns `parent` unchanged.
Caught by rendering the Model directly (bypassing a running
`tea.Program`) via a small `drainCmd` test helper that synchronously
executes a `tea.Cmd` chain -- worth keeping as a pattern for visually
inspecting the `Model` without needing a real terminal.

**Rejected alternatives.**
- *Use `lipgloss.Border` per section instead of hand-rolled box-drawing*
  -- considered; rejected for this pass because lipgloss draws a
  complete border around each styled box independently, which produces
  doubled seams where sections touch (the mockup shows one continuous
  outer border with internal `├─┤`/`┬`/`┴` junctions) unless composed
  much more carefully than the plain string-building approach used
  here.
- *Leave selection color-gating unconditional (always emit ANSI)* --
  considered, since reverse/bold are text decorations rather than
  colors and the NO_COLOR spec is about colors specifically; rejected
  for consistency with this codebase's own existing (stricter)
  interpretation in `internal/ui.Highlight`, which already gates its
  own bold usage the same way.

**Consequences.** `internal/workflow/object_browser.go` gained
`runFieldWithHelp`. `internal/filemanager/view.go` was substantially
rewritten (box-drawing helpers, ANSI-aware width math); `entry.go`'s
`joinKey` got a one-line fix. New tests: `box_test.go` (padding/
truncation/alignment invariants under ANSI styling, the `joinKey`/
`label()` fix, a whole-view row-width consistency check) and a
`styleRow` NO_COLOR-gating test. All pre-existing `internal/filemanager`
tests pass unchanged.

---

## 2026-07-09 — Extract `internal/s3diff`; add a dedicated Sync action to the file manager; use `x/exp/teatest` for `Model` tests

**Context.** Implementing PLAN.md Phase 20.1 (this file's earlier
2026-07-09 entries designed it) surfaced three implementation-time
decisions the design pass didn't fully settle.

**Decision 1 — `internal/s3diff` package.** The plan's file list said
`bucket_sync.go`'s diff/walk/list helpers (`diffSync`, `walkLocalTree`,
`listAllBucketObjects`, `contentTypeFor`) would stay in
`internal/workflow`, "reused, not rewritten," by the new screen. That
doesn't fit Go's import rules once `internal/workflow/object_browser.go`
needs to call into `internal/filemanager` to launch the screen:
`filemanager` can't import back into `workflow` without a cycle. Moved
these helpers into a new, lower-level `internal/s3diff` package that
both `internal/workflow` (implicitly, via retirement -- see Decision 3)
and `internal/filemanager` depend on, preserving genuine code reuse
(same functions, not duplicated) without a cycle.

**Decision 2 — a dedicated Sync action, not just manual tag-and-act.**
The file manager as first built (single-pane, then double-pane with
manual per-directory tag-and-Upload/Download) covered Feature 21's old
single-object case and the old bulk-delete-by-prefix case, but not
Feature 20's automatic whole-tree diff (compute upload/delete candidates
by comparing an entire local directory against the entire bucket, dry
run, two-stage confirm). Flagged to the user as a real gap against this
file's own 2026-07-09 "Design the S3 object management UI/UX pass"
entry (Decision 2 there: "Sync's directory-mirroring workflow is kept
as a first-class, directly reachable capability"). The user chose to
build it properly rather than ship with manual tag-and-act as the only
path. Added a `S`/`:sync` action (DESIGN.md 21.6) reusing
`internal/s3diff.Compute`/`WalkLocalTree`/`ListAllBucketObjects` against
the *entire* linked directory and *entire* bucket (not scoped to either
pane's current navigated position) — matching the retired wizard's own
semantics exactly, gated by the same never-bundled upload-then-delete
two-stage confirm (Security Consideration #11).

**Decision 3 — retire `bucket_sync.go`'s wizard, not just
`bucket_browse.go`/`bucket_delete_objects.go`.** PLAN.md's work items
only named the latter two for retirement. Once Sync became a directly
reachable file-manager action (Decision 2) with real parity, keeping
`SyncDirectoryToBucket` around as dead, unreachable code (no menu entry
dispatches to it any more) would violate this project's own practice of
deleting confirmed-unused code rather than leaving stale copies
(`CLAUDE.md`). Deleted `bucket_sync.go` and `bucket_sync_test.go`
outright; their diff logic lives on in `internal/s3diff` (Decision 1),
tested there instead.

**Decision 4 — `x/exp/teatest` resolves PLAN.md's open testing
question.** Confirmed real and usable by pulling it into the module
(`go get github.com/charmbracelet/x/exp/teatest`) and driving the
`Model` through it, per this project's standing evaluation discipline.
`teatest.NewTestModel` runs the `Model` as an actual `bubbletea.Program`
against an in-memory terminal; `.Send` injects `tea.Msg`s (key
presses); `teatest.WaitFor` polls the rendered output for a substring.
One caveat worth recording: bubbletea's renderer only retransmits
screen lines that changed since the previous frame, so two sequential
`WaitFor` calls checking *different* substrings can race if both
substrings were already present in one earlier, since-drained frame —
check multiple substrings in a single `WaitFor` condition (or assert on
a status line's derived text) rather than assuming later calls see
everything still on screen. `go test -race` also caught one genuine
concurrency bug this pattern makes visible that a non-race-checked
manual test would have missed: `runDelete`'s background goroutine
called `pane.clearTags()` directly (a Model mutation) instead of only
sending text over its progress channel, racing with the render loop's
concurrent read of the same map. Fixed by moving that mutation into the
overlay-dismiss key handler, which runs on `Update`'s single goroutine
— the general rule going forward for this `Model`: background
goroutines started by an action may only ever send `progressLine`
values over a channel, never touch `Model`/`pane` fields directly.

**Rejected alternatives.**
- *Duplicate the diff helpers in `internal/filemanager` instead of
  extracting a shared package* — considered, given the plan's literal
  wording implied `bucket_sync.go` would stay put; rejected once it
  became clear that would mean two copies of the same key+size diff
  logic drifting apart over time, against this project's stated
  preference for simplicity over duplication.
- *Ship without a dedicated Sync action, relying on manual
  tag-everything-then-Upload* — genuinely workable and was the default
  path until the user was asked; rejected because it silently drops the
  auto-diff (only-changed-files) behavior the original wizard had, and
  doesn't literally satisfy the earlier Decision 2 commitment.
- *Write `Model` tests by manually driving `Update`/executing returned
  `tea.Cmd`s synchronously, skipping `teatest` entirely* — considered
  when `teatest`'s output-diffing first produced two flaky-looking
  failures; rejected once the actual cause (draining + unchanged-line
  suppression, not a fundamentally unreliable tool) was root-caused --
  `teatest` works well once that behavior is understood and tests
  assert accordingly.

**Consequences.** `internal/s3diff` is new, with its own tests
(`s3diff_test.go`). `internal/workflow/bucket_sync.go`/
`bucket_sync_test.go` are deleted. `internal/filemanager` gained
`sync.go`/`sync_test.go` and a `S`/`:sync` hotkey/command. PLAN.md Phase
20.1's file list and work-item checkboxes, and DESIGN.md's 21.6 section,
are updated to match what was actually built.

---

## 2026-07-09 — Demote CloudFront to someday/maybe; decouple Phase 22's real-AWS testing from it

**Context.** The 2026-07-09 "0.0.1 scope" decision (below) postponed
CloudFront (PLAN.md Phase 21, DESIGN.md Features 22-25) "to a later
version" -- phrasing that still implied it was queued up as a
reasonably near-term next step, just after 0.0.1. With the S3 object
management UI/UX pass now actively designed and planned (this file,
above; PLAN.md Phase 20.1), it's clear CloudFront isn't close to being
picked up next -- the user doesn't expect to get to it soon.

**Decision.** Recharacterize CloudFront as someday/maybe: not on the
active roadmap, no committed timeline, weaker than "postponed to a
later version" implied. The design (DESIGN.md Features 22-25, PLAN.md
Phase 21) stays intact as valid reference -- nothing is deleted, only
the status framing changes. Phase 22 ("Real-AWS Testing") is split so
it no longer depends on Phase 21: it now covers only Key Management and
S3, and CloudFront's own real-AWS verification moves into Phase 21's
own scope, to happen whenever that phase is eventually picked up rather
than gating Phase 22's completion on a someday/maybe item.

**Rejected alternatives.**
- *Leave Phase 22 depending on Phase 21* -- rejected; it would mean
  Key Management and S3 (both actively shipped in 0.0.1) can never be
  marked verified-complete until an indefinitely-postponed domain is
  built, which misrepresents how settled those two domains actually
  are.
- *Delete the CloudFront design entirely rather than demote it* --
  rejected; the design and plan are still valid reference and cost
  nothing to keep, per this project's existing practice for postponed
  work (see the original CloudFront postponement decision below).

**Consequences.** PLAN.md Phase 21's status line, its Priority Order
table row, and Phase 22's title/scope/dependency are updated. DESIGN.md's
CloudFront Domain section gets the same someday/maybe note Features 20
and 21 (S3) already carry for their own supersession. TODO.md's
"Postponed to a later version" section is split into a new "Someday/
maybe" section (CloudFront) and the existing postponed section (the
UI/UX overhaul, which is no longer purely "not started" now that Phase
20.1 exists).

---

## 2026-07-09 — Design the S3 object management UI/UX pass: one interactive file manager, not three separate wizards

**Context.** The "0.0.1 scope" decision below deferred the UI/UX pass
entirely, flagging `huh` as the leading candidate but starting no work.
This entry begins that work: the S3 domain had grown three independent,
object-touching workflows shipped in Phase 20 -- Sync Local Directory to
Bucket (Feature 20), Browse/Manage Objects (Feature 21, single-object
only), and an ad-hoc bulk delete-by-prefix case -- each with its own
selection model (auto-diff, single-pick, whole-prefix-only) and none
supporting multi-select. Read (download) was never implemented at all
(Phase 20: "object content is never downloaded, only `HeadObject`
metadata").

**Decision 1.** Replace all three with one interactive file manager
screen (DESIGN.md Features 21.2-21.8), single-pane (bucket only) or
double-pane (bucket + linked local directory) depending on whether a
local directory is linked. Tagging one item and acting on it covers
Feature 21's old single-object case; tagging many covers the old
bulk-delete-by-prefix case; both live in the same screen instead of
three parallel implementations of "filter, pick, act."

**Decision 2.** Sync's directory-mirroring workflow is kept as a
first-class, directly reachable capability -- the double-pane/linked
mode -- rather than dissolved into a generic action-first "pick an
action, then pick candidates" flow. Upload candidates only make sense
as a diff against a local directory (there's no way to compute "what
should I upload" from the bucket side alone), so Sync's shape is
inherently different from Download/Delete's "browse and pick" shape,
and it's common enough usage that it deserves a direct path, not a
detour through an action menu.

**Decision 3.** Add Read (Download) to the CRUD scope now:
`s3:GetObject` is added to `S3API`, completing Create/Update (Upload) /
Read (Download) / Delete parity. Previously deferred in Phase 20
("`GetObject` isn't needed since object content is never downloaded").

**Rejected alternatives.**
- *Keep the three existing wizards, add multi-select to each
  independently* -- rejected; perpetuates three separate
  selection/filter/confirm implementations that would need to be kept
  in sync by hand as the UI evolves further.
- *Fold Sync into the generic batch flow as an "Upload" action
  alongside Download/Delete* -- rejected (see Decision 2); there's no
  bucket-side way to build an upload candidate set, and burying a
  common, direct workflow behind an action-first menu makes it harder
  to reach, not easier.
- *Defer Download again, scope this pass to Upload/Delete UX only* --
  rejected; CRUD parity was worth completing given the interactive
  screen already has to support tagging and acting on bucket objects
  generically, and the added cost (one interface method, one action
  handler) is small next to the rest of this phase.

**Consequences.** DESIGN.md Features 20 and 21 are marked superseded
(design-only, not yet implemented) by new Features 21.2-21.8; their
existing text is otherwise untouched and still describes what 0.0.1
actually ships. See `PLAN.md` Phase 20.1.

---

## 2026-07-09 — Use a scoped bubbletea screen for the file manager's double-pane mode; every other S3 wizard stays on huh

**Context.** The "0.0.1 scope" decision below evaluated `bubbletea`
against `huh` and chose `huh` as the leading candidate specifically
because its fields are blocking/synchronous, a close match to
`termlib`'s `Prompt`/`PickList`/`Confirm` shape, while `bubbletea`'s
Elm-architecture message loop would mean rewriting every one of
`internal/workflow`'s ~40 wizards into explicit state machines.
Designing the file manager's double-pane mode (local directory + bucket,
live tag-and-move between them) surfaced a case that evaluation didn't
anticipate: two simultaneously-visible, independently-navigable
listings with cross-pane actions is a genuinely stateful,
continuously-redrawing UI -- not a sequence of blocking prompts, no
matter how the prompts are composed.

**Decision.** Build the file manager's screen (both single- and
double-pane modes, since they share one `Model`) directly on
`bubbletea` as one scoped, bounded component. Everything else in the S3
domain -- Create Bucket, Configure Static Website Hosting, Manage
Bucket Lifecycle Policies, Delete Bucket, and this same screen's own
bucket-selection pre-flight -- stays on `huh`'s blocking fields, per the
original evaluation.

**Rejected alternatives.**
- *Approximate the linked mode as a `huh`-only reviewed batch* (three
  sequential filtered-multiselect phases: upload pass, download pass,
  delete pass, each pre-checked from a diff) -- genuinely buildable
  within the existing huh-only architecture and seriously considered;
  rejected once a live, navigable dual-pane experience was preferred
  over a review-then-execute approximation of one.
- *Adopt `bubbletea` project-wide now, since `huh` already pulls it in*
  -- rejected again; the rewrite cost the original evaluation identified
  (~40 wizards' control flow) doesn't shrink just because one new screen
  needs `bubbletea` directly -- it only means this one screen's
  incremental dependency cost is zero, not that a wider migration is
  now free.

**Consequences.** No new dependency weight versus adopting `huh` at
all -- `huh` already pulls in `bubbletea`, `bubbles`, and `lipgloss`
transitively. This is the only place in the S3 domain design that needs
a custom `bubbletea` `Model`; DESIGN.md 21.8 has the detail. Test
strategy for this `Model` is an open question (`PLAN.md` Phase 20.1) --
the project's existing pipe-based test pattern doesn't directly apply
to an `Update`/`View` loop.

---

## 2026-07-09 — File manager panes navigate independently, not synced to a shared relative path

**Context.** Double-pane mode could either lock both panes to the same
relative subfolder (so every listing is inherently a live diff,
reinforcing "these two trees should match") or let each side browse
anywhere independently, like a traditional dual-pane file manager
(Midnight Commander, WinSCP).

**Decision.** Independent navigation. Tagging happens in whichever pane
has focus; an action's destination is the other pane's current
position, not a shared path both panes must already agree on.

**Rejected alternatives.** *Synced navigation* (both panes always
mirror the same subfolder, every row annotated with an
upload/download/in-sync/conflict badge) -- rejected; it's a better fit
for "reconcile these two trees" specifically, but forecloses the more
general dual-pane use case (e.g. uploading from one local folder into
an unrelated bucket prefix that doesn't mirror it), and conflicts with
the tag-in-focused-pane/act-on-other-pane convention already decided,
which assumes the destination is wherever that pane happens to be
pointed.

**Consequences.** Diff-style badges (upload/download/in-sync) are not a
core mechanic of ordinary browsing -- they only apply within Sync's own
directory-mirroring workflow (DESIGN.md Feature 20, reachable through
this same screen), not to double-pane browsing in general.

---

## 2026-07-09 — File manager command area: single-letter hotkeys plus a colon command line, both always active; no function keys

**Context.** Traditional dual-pane managers (Midnight Commander) drive
their command area with function keys (F5 copy, F6 move, F8 delete).
Function-key mappings are unreliable across terminal emulators,
multiplexers, and SSH sessions in practice -- a real operational
concern for a tool meant for wider library/archive use, not just this
team's own terminal setup.

**Decision.** Use single-letter mnemonic hotkeys (`u` Upload, `d`
Download, `x` Delete, `f` Filter, `F` Find, `l` Link, `Tab` switch pane,
`Space` tag, `q` quit) instead of function keys, and add a
`:`-prefixed command line as a fully independent second path to every
action (`:upload`, `:delete`, `:find <pattern>`). Both drive the same
underlying action dispatch -- neither is a fallback for the other.

**Rejected alternatives.** *Function-key legend bar only* (the
mc/WinSCP convention, originally the leading option) -- rejected once
the terminal-remapping risk was raised; letter keys plus a
typed-command path removes the dependency on F-key passthrough working
correctly at all. *Hotkeys only, no command line* -- rejected; a typed
path is strictly more robust across terminal configurations and costs
little extra to support given both dispatch to the same handlers.

**Consequences.** Every action needs a name (verb) as well as a key
binding, since the command line and hotkey bar must both resolve to the
same dispatch table -- a small but real constraint on how actions get
implemented (`PLAN.md` Phase 20.1).

---

## 2026-07-09 — Supersede Phase 20's whole-bucket key-prefix filter with per-directory-level (Delimiter-based) listing and a substring filter

**Context.** The 2026-07-08 "Phase 20 (S3 domain) scope decisions"
entry (below) added a key-prefix filter to Browse/Manage Objects
specifically because a single real bucket (e.g.
`sql-backups.library.caltech.edu`) can hold many objects across many
per-instance prefixes, and listing everything unconditionally doesn't
scale to this team's actual usage. That filter works against one flat,
whole-prefix listing (`ListObjectsV2` scoped to whatever prefix the
operator typed once, upfront). The file manager instead browses
hierarchically, one directory level at a time (DESIGN.md 21.5) -- a
different listing shape that changes what "filtering" should mean and
what it costs.

**Decision.** List one directory level per call via `ListObjectsV2`
with `Delimiter=/` (`CommonPrefixes` for folders, `Contents` for
files). Filtering (`f` / `/`) narrows the current level's already-
fetched rows by substring match -- cheap, since "current level" is
never the whole bucket regardless of how the tree is shaped below it.

**Rejected alternatives.** *Keep Feature 21's original flat,
whole-prefix listing plus its upfront prefix prompt* -- rejected; it
doesn't support hierarchical drill-down/breadcrumb navigation at all,
which the file manager's browsing model depends on. *Add a client-side
substring/glob filter on top of a flat whole-bucket listing* --
considered and rejected earlier in this same design pass, before
per-level Delimiter-based listing was decided, specifically because it
would mean fetching a potentially large flat listing before any
filtering could narrow it; per-level listing removes that cost by
construction, which is why the filter approach could be revisited and
approved here.

**Consequences.** Feature 21's original "Filter by key prefix (blank for
all)" prompt is retired along with the rest of Feature 21's standalone
wizard (see "Design the S3 object management UI/UX pass," above) once
Phase 20.1 ships.

---

## 2026-07-09 — File manager Find: recursive glob-on-basename search, not full-path glob or regex

**Context.** The file manager needed a way to search recursively across
a directory/bucket subtree by name pattern (e.g. `*.go`, or `\.git` to
find git repository directories) -- a different operation from the
per-level substring filter (`f`), which only ever looks at what's
already listed at the current level.

**Decision.** Match a shell glob pattern (Go stdlib
`path/filepath.Match` semantics, including backslash-escaping) against
each entry's basename, evaluated recursively at every depth below the
focused pane's current position -- the same behavior as `find <dir>
-name '<pattern>'`. Both motivating examples (`*.go`, `\.git`) already
work under this exact semantics with no further feature needed.

**Rejected alternatives.** *Regex pattern support* -- no concrete case
surfaced that a shell glob can't already express; not built, revisit if
one comes up. *Full-path glob matching (e.g. `**`-style patterns
spanning directory separators)* -- unnecessary given per-basename
matching during a recursive walk already satisfies both stated examples
and matches `find -name`'s well-understood behavior; adding
path-spanning glob syntax would be new complexity solving a problem
that hasn't come up. *Search from the tree root always* -- rejected in
favor of starting from the focused pane's current position, matching
`find`'s own convention and avoiding an unbounded scan when the operator
only meant to search what they're currently looking at.

**Consequences.** S3-side Find pays the cost of a full recursive
`ListObjectsV2` (no `Delimiter`) under the current prefix when invoked
-- the same cost Feature 20 (Sync) and the old delete-by-prefix case
already paid, just now on-demand and user-triggered rather than
automatic every time the workflow runs. Needs a cancellable, live
progress indicator for large subtrees (DESIGN.md 21.7, `PLAN.md` Phase
20.1).

---

## 2026-07-09 — 0.0.1 scope: ship on termlib as-is; postpone CloudFront and the UI/UX overhaul

**Context.** With Phase 20 (S3 domain) real-AWS verified and this
session's follow-on work landed -- local validation for lifecycle
rule ordering, a read-only "View rule details" action, Delete Bucket,
Delete Objects by Prefix, and filter-as-you-type/alphabetical sort in
`ui.PickList` -- the user judged core functionality complete enough
for colleagues to start using the tool, but not the UI itself: the
current numbered-menu, blocking-prompt style built on `termlib` (the
user's own library) needs a UX/UI pass before wider use.

As part of scoping what "ready for 0.0.1" means, two third-party
libraries were evaluated (by pulling their actual source into a
scratch module, not just reading marketing pages):

- `github.com/charmbracelet/bubbletea`: an Elm-architecture (Model/
  Update/View, async message loop) TUI framework. Replacing `termlib`
  with it would mean rewriting the control flow of every one of
  `internal/workflow`'s ~40 prompt-driven wizards into explicit state
  machines, plus their tests -- not a drop-in swap.
- `github.com/charmbracelet/huh`: forms/prompts built on `bubbletea`,
  but each field is run synchronously/blocking (`huh.Run(field)`),
  much closer to `termlib`'s `Prompt`/`PickList`/`Confirm` shape. Its
  `Select` field's built-in incremental filtering is a nicer target
  than the numbered-list approach, and every field's
  `RunAccessible(w io.Writer, r io.Reader) error` is structurally the
  same shape as this project's existing pipe-based test harness
  (`newPipeEditor`), so the existing test style would largely carry
  over. Cost: `huh` pulls in `bubbletea`+`bubbles`+`lipgloss` and
  ~18 transitive modules, versus `termlib`'s ~2,800 dependency-free
  lines.
- `github.com/peak/s5cmd/v2`: not a good dependency candidate either
  way (its `command/` package is coupled to `urfave/cli/v2.Context`;
  its cleaner `storage/` package is built on `aws-sdk-go` v1, while
  this project is on `aws-sdk-go-v2`). Its `storage.S3.MultiDelete`
  pattern -- batch `s3:DeleteObjects` in chunks of up to 1000 keys
  instead of one `DeleteObject` call per key -- is worth reimplementing
  natively later (see TODO.md, "Nice to have"), since `aws-sdk-go-v2`
  already exposes `DeleteObjects`.

**Decision.** 0.0.1 ships on `termlib` unchanged. `huh` is the leading
candidate for the next release's UI/UX pass (evaluated, not started).
CloudFront (PLAN.md Phase 21-22) is postponed to a later version -- its
domain-picker entry was removed entirely (`DomainActions.CloudFront`,
the `"CloudFront"` menu item, and its `NotYetImplemented` wiring in
`cmd/awsops/main.go`) rather than left as a placeholder, so the
released UI doesn't expose a menu item that goes nowhere. Its design in
DESIGN.md/PLAN.md is untouched and stays valid for when it's picked
back up.

**Rejected alternatives.** Migrating to raw `bubbletea` now, to avoid a
second migration later -- rejected because the architectural mismatch
with `termlib`'s ~40 blocking call sites makes it a full rewrite either
way; better to ship 0.0.1 first and spend that rewrite effort once,
against `huh` (or whatever's chosen), informed by real colleague usage
rather than guessing at UX needs up front.

**Consequences.** No `internal/ui` or `internal/workflow` prompt code
changed as a result of this decision -- it's scope/sequencing only.
`TODO.md`'s "Postponed to a later version" section and `PLAN.md`'s
Phase 21 heading both note the CloudFront postponement.

---

## 2026-07-08 — Clear a bucket's lifecycle configuration via DeleteBucketLifecycle, not an empty PutBucketLifecycleConfiguration

**Context.** Real-AWS manual verification of Phase 20's Manage Bucket
Lifecycle Policies feature (immediately after implementation) surfaced
two real bugs the unit tests' fakes couldn't catch, since fakes don't
enforce AWS's own validation rules:

1. Setting a guided backup policy with expiration (30 days) shorter than
   its transition (90 days) fails with a real AWS error:
   `'Days' in the Expiration action ... must be greater than 'Days' in
   the Transition action`. This is a genuine AWS API constraint (an
   object must transition to cheaper storage before it can expire, not
   after) that neither the guided flow's two independent prompts nor
   this decision log's earlier design round anticipated.
2. Removing a bucket's *last* remaining lifecycle rule failed outright:
   `PutBucketLifecycleConfiguration` requires a non-empty `Rules` field
   (enforced client-side by the AWS SDK before any network call), so
   `bucket_lifecycle.go`'s fetch-modify-PutBack pattern (see the 21.1
   entry below) breaks down exactly when the "modify" step empties the
   rule set.

**Decision.** Fixed #2 (the one blocking Remove entirely, not just a
specific input combination): added `DeleteBucketLifecycle` to `S3API`
and its logging wrapper, and `ManageBucketLifecyclePolicies` now branches
on the modified rule set's length -- empty calls `DeleteBucketLifecycle`
instead of `PutBucketLifecycleConfiguration`. Per this project's
test-before-fix practice, a failing test
(`TestManageBucketLifecyclePolicies_RemovingLastRuleCallsDeleteNotPutWithEmptyRules`)
reproduced the bug against the pre-fix code before the fix was written,
then re-verified against real AWS (create a rule, remove it, confirm
`GetBucketLifecycleConfiguration` reports `NoSuchLifecycleConfiguration`
afterward).

#1 (the expiration/transition ordering constraint) was **not** given
local validation in this pass -- worked around during verification by
choosing valid test values (transition < expiration), and left as a
known gap: the guided flow currently surfaces AWS's own error message
verbatim rather than catching the mismatch before calling AWS. A future
pass should add a local check (transition days, when both are set, must
be less than expiration days) mirroring `validateBucketName`'s
locally-caught-before-AWS pattern from Create Bucket.

**Consequences.** `S3API` gains a thirteenth method,
`DeleteBucketLifecycle`, alongside the twelve added for Phase 20.
`internal/workflow/bucket_fakes_test.go`'s shared fake gained matching
fields/methods. The Add/Edit guided-flow's real-world expiration-vs-
transition ordering constraint remains an open, documented gap (see
TODO.md).

---

## 2026-07-08 — Add Feature 21.1, Manage Bucket Lifecycle Policies, with a Purpose-tag-driven guided/generic split

**Context.** Before writing any Phase 20 code, the user identified three
bucket use cases this tool should make easy: a website bucket (already
Features 18-19), a shared backup bucket needing expiration and
transition-to-cheaper-storage policies on its objects (the pattern
Backup Archive & Trim already produces, per DECISIONS.md "Namespace
backup uploads by instance"), and an internal-use bucket with no
predictable policy shape. None of this existed in DESIGN.md — S3
Lifecycle Configuration management wasn't designed at all.

**Decisions (asked, user confirmed all four):**
1. **Bucket "purpose" is tagged and remembered, not just a creation-time
   wizard.** Create Bucket (Feature 18) now prompts for a purpose
   (Website/Backup/Internal) and applies it as a `Purpose` tag
   (`s3:PutBucketTagging`); Feature 17 (List Buckets) reads it back for
   every bucket so later features don't need to re-ask.
2. **Lifecycle policy scope is an optional prefix, blank = whole
   bucket** — the same convention as Feature 21's browse-filter
   addition, rather than forcing either a mandatory prefix or a
   whole-bucket-only model.
3. **Two different UIs, selected automatically by the `Purpose` tag**:
   `backup` gets a guided flow (two yes/no-shaped prompts: expire after
   N days, transition after N days); `internal` (and `website`, and any
   untagged bucket) gets a generic rule editor (named rules, arbitrary
   transitions, optional expiration) — one feature (21.1), one menu
   entry, branching internally, not two separate menu items.
4. **Multiple named rules with full CRUD**, not a single-policy-per-scope
   model — fetch all existing rules, let the operator pick one to edit
   or remove, or add a new one, then write the complete rule set back
   (the only way AWS's `PutBucketLifecycleConfiguration` API supports
   changes at all — it always replaces the whole rule set atomically).

**Smaller decisions made without a separate question round:**
- **Guided flow's storage-class choices are curated** (Standard-IA,
  Intelligent-Tiering, Glacier Flexible Retrieval, Glacier Deep Archive)
  rather than the full AWS enum — these four cover "make backups
  cheaper over time" without exposing storage classes irrelevant to
  that goal (One Zone-IA's reduced durability isn't appropriate for the
  only copy of a backup; Reduced Redundancy Storage is legacy). The
  generic editor (`internal`/`website`/untagged buckets) exposes the
  *full* `types.TransitionStorageClass` enum instead, matching its
  "unpredictable needs" framing.
- **Numbered 21.1, not renumbered into the 22-26 sequence** — CloudFront
  Features 22-26 already have ~15 cross-references across
  DESIGN.md/DECISIONS.md/PLAN.md; renumbering them to make room risked
  missing one silently. Mirrors PLAN.md's own existing convention of
  decimal-numbered insertions (Phase 15.1 through 15.26) rather than
  introducing a new pattern.
- **Rule removal and edits stay a plain yes/no confirm**, not the
  stronger dry-run + type-to-confirm tier, but the confirmation text
  must say plainly that this schedules *future* automated deletion, not
  an immediate one (see DESIGN.md, Security Considerations #13) — AWS
  evaluates lifecycle rules on its own cadence (typically within 24-48
  hours), not instantly on `PutBucketLifecycleConfiguration`.

**Rationale.** All four user-facing decisions were asked and confirmed
before any implementation started, per this project's design-then-code
discipline. The Purpose-tag branch (one feature, one menu entry) was
chosen over two separate menu entries because the user's own framing —
"the internal bucket is similar" — describes one capability used two
ways, not two capabilities.

**Rejected alternatives.**
- *Two separate menu entries* (e.g. "Manage Backup Policies" / "Manage
  Object Lifecycle Policies") instead of one Purpose-branching feature —
  rejected per the user's own framing above; also would require the
  operator to already know a bucket's purpose before picking the right
  menu item, defeating the point of tagging it.
- *Single active policy per bucket/prefix* (no rule naming/listing) —
  rejected; doesn't fit the internal bucket's explicitly "unpredictable"
  needs, and the guided flow's simplicity doesn't actually require
  giving up multi-rule support underneath (the guided prompts just
  populate one more named rule in the same underlying store).

**Consequences.** `S3API` grows further to include `PutBucketTagging`,
`GetBucketTagging`, `GetBucketLifecycleConfiguration`, and
`PutBucketLifecycleConfiguration` on top of Phase 20's already-broadened
surface (see "Phase 20 (S3 domain) scope decisions," below). `internal/
inventory.Bucket` gains a `Purpose` field, fetched during Feature 17's
existing per-bucket enrichment fan-out (no new listing pass). Phase
20's effort estimate in PLAN.md needs updating to reflect this
meaningfully larger scope.

---

## 2026-07-08 — Phase 20 (S3 domain) scope decisions: defer public-read opt-out, add a key-prefix filter

**Context.** Before starting Phase 20 (S3 domain: List/Create Bucket,
Configure Static Website Hosting, Sync Local Directory to Bucket,
Browse/Manage Objects), two places where DESIGN.md's existing Features
19 and 21 needed a concrete scope call that the design text itself
didn't fully pin down.

**Decision 1 — defer the public-read bucket policy opt-out.** Feature 19
(Configure Static Website Hosting) mentions an operator can explicitly
opt into a public-read bucket policy instead of the CloudFront + Origin
Access Control default -- but CloudFront (Phase 21, Feature 24) doesn't
exist in this codebase yet, so there's nothing for the default path to
actually hand off to. Phase 20 implements only the default path
(configure website documents; bucket stays private via Feature 18's
`PutPublicAccessBlock`); where DESIGN.md's text says "hand off to
CloudFront," awsops instead prints that CloudFront support isn't
implemented yet. The public-read opt-out (its own explicit warning,
confirmation, and `s3:PutBucketPolicy` call) is deferred until there's
an actual need for it.

**Decision 2 — add an optional key-prefix filter to Browse/Manage
Objects.** Not in DESIGN.md's original Feature 21 text, which lists
every object in the bucket unconditionally. This team's actual S3 usage
(e.g. `sql-backups.library.caltech.edu`, namespaced
`<instance-name>/<filename>` per DECISIONS.md's "Namespace backup
uploads by instance") means a single real bucket can hold many objects
across many per-instance prefixes -- listing everything unconditionally
would be substantially less usable on this team's actual buckets than on
a small test bucket. Feature 21 now prompts `"Filter by key prefix
(blank for all)"` before calling `s3:ListObjectsV2`; blank preserves the
original "list everything" behavior exactly.

**Rationale.**
- Both decisions were surfaced as explicit questions before writing any
  code, per this project's design-then-implement discipline, rather than
  silently decided during implementation.
- Deferring the public-read opt-out avoids building a whole secondary
  confirmation-and-policy-construction path for something DESIGN.md
  itself frames as a secondary escape hatch, before the primary
  (CloudFront) path it's an alternative *to* even exists.
- The prefix filter is a small, backward-compatible addition (default
  behavior unchanged) directly motivated by how this team's own S3
  buckets are actually structured, not a hypothetical future need.

**Rejected alternatives.**
- *Build the public-read opt-out now anyway* — rejected; it would mean
  significant Feature 19 surface area (policy JSON construction, its own
  confirmation tier) serving a path that's explicitly secondary in
  DESIGN.md's own framing, before Phase 21 (CloudFront) even exists to
  make the *primary* path complete.
- *Implement Browse/Manage Objects exactly as spec'd, no filter* —
  rejected; DESIGN.md's own Feature 1 precedent (PickList pagination for
  >50 items) already acknowledges large lists are a real concern for
  this tool, and a bucket with thousands of objects across many prefixes
  is a materially worse experience without a filter than EC2/AMI/key
  pair lists ever are.

**Consequences.** PLAN.md's Phase 20 work items and DESIGN.md Features
19 and 21 are updated to match. When Phase 21 (CloudFront) ships, Feature
19's "hand off to CloudFront" message should be revisited -- either
wired to an actual Create Distribution entry point, or left as
informational if the operator is expected to navigate there manually.

---

## 2026-07-08 — Fix termlib's LineEditor.Prompt for overlong prompt labels; keep awstools' own prompts short

**Context.** A real bug, hit live: Import Key Pair's "Public key file
path" prompt initially embedded a long (~180 character) explanatory hint
directly in the prompt label passed to `ui.Prompt`/
`termlib.LineEditor.Prompt`. `LineEditor.Prompt`'s raw-mode redraw logic
computes its input viewport as `terminal width - prompt length`,
assuming the whole prompt fits on one terminal row, and repaints via
`"\r"`, which only returns to column 0 of the terminal's *current* row.
Once the prompt itself was wider than the terminal, printing it wrapped
onto multiple rows; every subsequent keystroke's redraw then reprinted
the *entire* prompt again after only a `"\r"`, re-wrapping and pushing
the display down further each time -- garbled, repeated prompt text,
with the input viewport (`vw`) clamped to 1 column, so typed characters
were never visibly readable. Not a cosmetic line-wrap issue.

**Decision.** Two changes, at two levels:
1. **Fixed the actual root cause in `termlib` itself**
   (`~/Laboratory/termlib/lineeditor.go`, this team's own library, not an
   external one): a new `splitSafePrompt(prompt, termWidth)` helper splits
   any prompt into a `head` (everything up to and including the last
   embedded `\n`, printed once and never revisited) and a `tail` (only
   the portion that could ever share a terminal row with typed input).
   If `tail` is still `>= termWidth` runes wide on its own, it's folded
   into `head` too (with a trailing newline), and `tail` becomes empty --
   editing then starts on its own blank row with the full terminal width
   available, instead of trying to redraw a prompt too wide for the
   existing single-row cursor math to track. `Prompt()` now calls this
   before entering its edit loop and uses the (now guaranteed-short)
   `tail` as `prompt` throughout the rest of the function -- `redraw()`
   itself needed no changes, since `curPromptLen`/`vw` now always operate
   on a value that's safe by construction.
2. **Simplified awstools' own prompt** back down to a short label,
   `"Public key file path (.pub -- e.g. ~/.ssh/id_ed25519.pub)"`, with the
   longer "not a private key / derive with `ssh-keygen -y -f
   <private-key> > file.pub`" guidance moved into
   `validatePublicKeyFile`'s own rejection error message instead --
   delivered reactively, exactly when the operator actually makes that
   mistake, rather than proactively in every single prompt.

**Rationale.**
- Fixing `termlib` closes this whole bug class everywhere it's used, not
  just this one prompt -- including this project's own pre-existing
  ~137-character "Key pair name (...)" prompt (`create_key_pair.go`) and
  ~116-character "IAM instance profile (...)" prompt
  (`create_instance_profile.go`), both of which had the same latent risk
  on a narrow enough terminal without ever being reported broken.
- `splitSafePrompt` also incidentally fixes embedded-newline prompts,
  which were never explicitly tested before and would have hit the same
  miscounted-`curPromptLen` problem (newlines counted toward row width
  even though they occupy zero visual columns).
- A short, single-purpose prompt label matches this project's dominant
  convention (see `backup_archive.go`'s "Backup directory (e.g.
  /opt/rdm_sql_backups)") better than either the original overlong
  version or the printed-hint-then-short-prompt workaround tried first
  -- and, now that `termlib` itself is fixed, the workaround is no longer
  load-bearing for correctness, just a style preference.

**Rejected alternatives.**
- *Insert a literal `\n` inside the prompt string, unfixed* — doesn't
  work on its own; the pre-fix `redraw()` counted the entire prompt
  (including embedded newlines) as occupying one row, so this needed the
  `termlib` fix to actually behave correctly.
- *Print the hint separately via `t.Println`, leave `termlib` alone* —
  tried first as a same-day workaround; superseded once the actual
  `termlib` bug was fixed properly, since papering over it in every
  affected caller (there were at least two pre-existing ones) is more
  maintenance than fixing the one shared library function.

**Consequences.** The fix was committed and pushed to `termlib`'s `main`
branch (commit `354195d`, "fix prompt and input bug"); `awstools/go.mod`
pins it directly by commit via a pseudo-version
(`github.com/rsdoiel/termlib v0.0.10-0.20260708184214-354195d36c57`,
resolved from the real GitHub remote, not a local-path `replace`) --
bump this to a proper tagged release version once `termlib` cuts one.
The new `splitSafePrompt` logic is fully unit-tested in `termlib` itself
(`lineeditor_test.go`, `TestSplitSafePrompt_*`), since it's a pure
function; the actual redraw behavior it fixes is still not reproducible
in either project's pipe-based test harness (`os.Pipe()` is never a TTY,
so `LineEditor.Prompt` always takes its plain `fallback()` path in
tests) -- confirming the real-terminal symptom is gone required manual
interactive testing, which the user did: Show, Create, Import, and
Delete Key Pair all confirmed working against real AWS on 2026-07-08.

---

## 2026-07-08 — Key Management independently refreshes instances for Delete Key Pair's dependency check

**Context.** Phase 19 (Key Management domain) implements Delete Key Pair
(DESIGN.md Feature 16), which warns about instances launched with the
key pair being deleted -- the same dependency-check pattern Remove AMI
(Phase 11) already uses, filtering an already-fetched `ListInstances`
result rather than making a fresh AWS call. But Compute's `state.instances`
in `cmd/awsops/main.go` is only populated once the operator has entered
the Compute domain at least once in the current run -- if Key Management
is entered first, that slice is nil, and Delete Key Pair's warning would
silently under-report (or entirely miss) real dependents.

**Decision.** Key Management's own `refreshKeyMgmt` closure in `main.go`
independently calls `inventory.ListInstances` (not just `ListKeyPairs`)
every time the domain is entered or its listing is refreshed, purely to
keep the dependency check correct -- the fetched instances aren't
displayed by Key Management, only used internally.

**Rationale.**
- Correctness of a safety-tier warning matters more than saving one
  cheap, two-region `DescribeInstances` call -- consistent with this
  project's existing bias (e.g. Compute already refreshes its own
  listing on every domain entry, not just once at startup, specifically
  so displayed/used data is never stale).
- The alternative -- sharing a single `state.instances` across both
  domains, refreshed only by whichever domain is entered -- would make
  Key Management's correctness depend on navigation order, which is a
  subtle, easy-to-miss bug class (works fine in every manual test that
  happens to visit Compute first).

**Rejected alternatives.**
- *Share Compute's `state.instances` unconditionally* — rejected for the
  navigation-order fragility above.
- *Only fetch instances lazily inside `DeleteKeyPair` itself, not on every
  Key Management refresh* — rejected; it would mean the Show Resource
  Lists refresh and the Delete Key Pair action could see different data
  if instances changed in between, and it complicates `DeleteKeyPair`'s
  signature (already takes `instances []inventory.Instance` like
  `RemoveAMI` does for images) for no real benefit over fetching once per
  Key Management refresh.

**Consequences.** One extra `ec2:DescribeInstances` fan-out (across
configured regions) on every Key Management domain entry and every
"Show resource lists" refresh within it. Negligible cost; Key Management
still displays only its own key pair listing, not instances.

---

## 2026-07-08 — Add public-key format validation for Import Key Pair

**Context.** DESIGN.md Feature 15 (Import Key Pair) specifies that a
local `.pub` file should be "read and validated... fail locally with a
clear message rather than surfacing AWS's raw `InvalidKeyPair.Format`
error." No existing helper in this codebase validates SSH public-key
*format* -- `isReadableFile` (used elsewhere for private-key-path
detection) only checks the file opens, and the cloud-init "@file" loader
just reads raw bytes with no format check at all.

**Decision.** New `validatePublicKeyFile` (`internal/workflow/keypair_import.go`)
checks the file is readable, then that its first whitespace-delimited
field is a recognized SSH key-type token (`ssh-ed25519`, `ssh-rsa`,
`ecdsa-sha2-nistp256/384/521`) and that a second field (the base64 key
body) is present. Not a full RFC4253 parse -- just enough to catch "this
obviously isn't a public key" (a private key pasted by mistake, an empty
file, random text) locally before ever calling AWS.

**Rationale.**
- Matches this project's established preference for local, actionable
  validation over letting AWS's own error surface raw (the same
  reasoning behind the AMI-name-length check, the security-group-ID
  format check, etc.) -- see the Bash-to-Go retarget's core motivation.
- Full cryptographic parsing (base64-decoding and structurally validating
  the key blob per RFC4253) would catch more malformed inputs but adds
  real complexity for a false-input class (a resembles-but-isn't-a-key
  file) that hasn't actually been observed as a real failure the way the
  Bash version's three real bugs were -- not worth the added surface
  area pre-emptively.

**Rejected alternatives.**
- *Full RFC4253 key-blob parsing* — rejected per the rationale above;
  revisit if a real malformed-but-prefix-matching file is ever seen in
  practice.
- *No local validation, let `ec2:ImportKeyPair` reject it* — rejected;
  DESIGN.md explicitly calls for local validation here, and AWS's own
  `InvalidKeyPair.Format` error is not actionable on its own.

---

## 2026-07-08 — Retire ec2_ami_manager.bash, ami_copy.bash, and the Bash test suite

**Context.** Phase 16's manual real-AWS verification (`TEST_PLAN_REAL_AWS.txt`)
is now fully signed off: all 112 checklist items confirmed against real AWS
across EC2/AMI instance and AMI lifecycle management, tag management,
cloud-init inspection, Backup Archive & Trim, and the `~/.awsops` config
file. Per this project's own retire-after-verify criterion (see
"Retarget implementation from Bash to Go" and the 2026-06-30 entries below),
`ec2_ami_manager.bash` and `ami_copy.bash` were kept, unchanged, purely as
the working reference for the Go rewrite's behavior until parity was
verified against real AWS. That condition is now met.

**Decision.** **Delete `ec2_ami_manager.bash`, `ami_copy.bash`,
`ami_copy_basic_steps.md`/`.html`, and the entire `tests/` directory
(`tests/*.bats`, `tests/lib/test_helper.bash`, `tests/README.md`) from the
repository.** `awsops` (Go) is now the sole implementation.

**Rationale.**
- Two parallel implementations of the same functionality is a maintenance
  cost with no offsetting benefit, once the newer one is verified equivalent
  or better -- the same reasoning as the 2026-06-30 retirement of
  `check_ami.bash`/`check_ec2_instances.bash`.
- `tests/lib/test_helper.bash` sources `ec2_ami_manager.bash` directly, and
  `tests/README.md` documents that BATS suite specifically -- neither has
  any purpose once the Bash script they test is gone, so the whole `tests/`
  directory retires together, not just the `.bats` files.
- Verified before deleting: the Makefile, `website.mak`, and Go source
  under `cmd/`/`internal/` have no functional dependency on these files
  (some Go source comments cite `ec2_ami_manager.bash` for historical
  lineage only, which don't affect `go build`/`go vet`/`go test`).

**Rejected alternatives.**
- *Keep the Bash files as an archived reference* — rejected; git history
  already preserves them in full (see the 2026-06-30 entries' precedent of
  actual deletion, not archival), and a live copy in the tree invites
  someone eventually running the stale, unmaintained version by mistake.

**Consequences.**
- `TODO.md`'s "Superseded (Bash version)" section and its remaining
  Bash-specific items are now historical only -- the files they describe no
  longer exist in the working tree.
- `DESIGN.md`'s top-of-file banner note about `ec2_ami_manager.bash`
  "remaining unchanged... until the Go version reaches parity" is updated
  to record this retirement.
- `README.md`, `INSTALL.md`, `user_manual.md`, `software_requirements.md`,
  and `codemeta.json` (and its generated `about.md`/`CITATION.cff`) were
  rewritten in the same pass to describe the Go `awsops` binary instead of
  the retired Bash scripts -- they had drifted since the 2026-07-01
  Bash-to-Go retarget and still described the original proof-of-concept.

---

## 2026-07-02 — Preflight check: AWS CLI availability before Backup Archive & Trim

**Context.** A missing AWS CLI on the target instance was, by a wide
margin, the most common real-AWS failure this project hit while
verifying Backup Archive & Trim -- it happened on `newauthors`, then
again on `data-new`. Both times the symptom was identical and
indirect: every file in the upload phase reported `FAIL`, with the real
cause (`aws: not found`) buried in the -debug JSONL log's stderr
capture, several prompts and a full dry-run list deep into the
workflow. The user's own framing: "That should be an explicit error we
check for much like with shell scripts we check if a command exists
before trying to execute it."

**Decision.** `BackupArchiveAndTrim` now calls a new
`CheckAWSCLIAvailable`, right after picking the instance and before any
other prompt, which runs `command -v aws` via SSM and aborts
immediately with `"AWS CLI not found on instance <id> -- install it
before running Backup Archive & Trim"` if it's missing.

**Rationale.**
- `command -v aws` is the same POSIX-portable existence check idiom
  this project already reaches for elsewhere (shellQuote's own doc
  comment references similar shell-safety discipline) -- no new pattern
  introduced, just applied one layer earlier.
- Checking immediately after instance selection, before the directory/
  age/bucket prompts and the dry-run list, means a missing CLI is
  caught in the time of one quick SSM round-trip instead of after
  several prompts and a potentially large `find` listing have already
  run for nothing.
- A dedicated check (not just "let the first `aws s3 cp` fail
  naturally") turns a misleading generic `FAIL` on every file --
  requiring a debug-log trip to explain -- into one specific,
  actionable, immediate error.

**Consequences.** One extra `ssm:SendCommand` round-trip per Backup
Archive & Trim run (negligible cost against the dry-run list and
uploads that follow). `internal/workflow/backup_cli_check.go` is new;
five existing tests' fake SSM clients needed a `"command -v aws"`
response entry added, and four `sendCommandCalls()` count assertions
shifted by one to account for the new leading check.

---

## 2026-07-02 — Suppress aws s3 cp's progress output to avoid truncating the OK/FAIL signal

**Context.** After fixing the target instance's IAM permissions, real-AWS
testing against `newauthors` still reported every file as `FAIL` in
`awsops`'s own upload progress line -- but the debug log showed the
underlying `aws s3 cp` had actually succeeded (real transfer progress,
no error). Inspecting the full `ssm:GetCommandInvocation`
`StandardOutputContent` showed it was truncated at exactly 24,000
characters -- SSM's documented cap on captured command output -- cut off
mid-way through `aws s3 cp`'s own `\r`-updated progress meter
("Completed 350.0 MiB/972.0 MiB ..."), with this script's own
`printf 'OK\t<key>\t<size>\n'` signal never reached because the noise
ahead of it already filled the entire captured buffer.

**Decision.** The remote `aws s3 cp` invocation in
`buildUploadCommand` now runs with `--only-show-errors`, which
suppresses its normal per-chunk progress output entirely while still
writing real error messages to stderr (confirmed still working via the
earlier `AccessDenied` diagnosis). This keeps stdout limited to just
this script's own short `OK`/`FAIL` line, regardless of file size.

**Rationale.**
- The bug scaled with file size, not a fixed defect -- small files
  never produced enough progress-meter text to hit the 24,000-character
  cap, which is why it wasn't caught until testing against real,
  multi-hundred-MB backup files.
- `--only-show-errors` (rather than redirecting `aws s3 cp`'s stdout to
  `/dev/null` inline, or post-processing to strip progress lines) is
  the AWS CLI's own supported way to ask for exactly this: quiet on
  success, verbose on failure -- no fragile string-stripping needed.

**Consequences.** No result-shape or parsing change -- `parseUploadResults`
and `UploadResult` are unchanged; the fix is entirely in what the remote
script asks `aws s3 cp` to emit. Retroactively, this also means any
earlier "FAIL" report for a large file could have been a false negative
if the underlying IAM/CLI issue had otherwise been fixed at the time --
not a concern in practice here, since every prior real-AWS run failed
for an unrelated, definitively-confirmed reason (missing AWS CLI, then
missing IAM permission) before ever reaching a file large enough to
trigger this truncation.

---

## 2026-07-02 — Show instance IP addresses in the main listing

**Context.** While troubleshooting the AWS-CLI-missing backup failure,
the operator installed the AWS CLI on the wrong EC2 instance because
there was no easy way to see which instance had which IP address and
SSH to the right one -- the main "CURRENT EC2 INSTANCES" listing showed
ID/Name/State/AMI/Region/Project/Environment, but no IP at all.
Connection info (`displayConnectionInfo`) already existed, but only as a
one-shot printout right after Create/Start -- not something you could
look up for an already-running instance days later.

**Decision.** `inventory.Instance` gains `PublicIP`/`PrivateIP` fields,
populated from the same `ec2:DescribeInstances` call already made for
every listing (no new API calls). `DisplayInstances` adds "PUBLIC IP"
and "PRIVATE IP" as two new trailing columns, rendering "none" (not
"unknown" -- this isn't missing data, it's a real, common state for a
stopped instance or one without a public IP) when blank.

**Rationale.**
- Adding columns to the always-shown listing, rather than a separate
  "show connection info" action, means the whole fleet's reachability
  is visible on every refresh with no extra menu step -- exactly the
  "which IP do I SSH to" question that triggered this.
- Zero additional AWS calls: `PublicIpAddress`/`PrivateIpAddress` are
  already present on every `DescribeInstances` response object; this
  is populating fields that were already being fetched and discarded.
- "none" instead of "unknown" for blank IPs distinguishes a legitimate
  state (no IP assigned) from `orUnknown`'s existing meaning (missing
  tag data) used for Project/Environment in the same table.

**Consequences.** The table grows two more columns (~26 more characters
per row) -- accepted since the existing table (~115 characters) already
assumes a reasonably wide terminal, and no width budget was documented
as a hard constraint anywhere.

---

## 2026-07-02 — Namespace backup uploads by instance

**Context.** `sql-backups.library.caltech.edu` is meant to hold backups
from multiple systems, not just one instance -- but every upload key was
just `path.Base` of the source file (e.g. `caltechauthors-db-1-...sql.gz`
directly at the bucket root). Two instances producing identically- or
similarly-named backup files (a very real possibility -- not every
system's backup script embeds a distinguishing name the way this one
happens to) would silently collide and overwrite each other in the
shared bucket.

**Decision.** Every upload key is now namespaced by the source
instance's Name tag: `s3://bucket/<name>/<filename>`. `uploadKey(prefix,
filePath)` builds this from `path.Join`, used consistently for the
`aws s3 cp` destination, the `printf`'d key the instance reports back,
and the tool's own `s3:HeadObject` verification and `rm -f` path
resolution. An untagged instance (blank Name) falls back to its
instance ID as the prefix -- every instance needs *some* non-empty,
distinguishing prefix, and the ID is always available.

**Rationale.**
- Name (not instance ID) as the primary prefix keeps the bucket
  browsable by a human restoring from backup -- `newauthors/...` reads
  far better than `i-0c4c81336aea33d27/...`. The ID-on-blank-Name
  fallback covers the one case where Name alone wouldn't be usable at
  all.
- Building the prefix once, in `BackupArchiveAndTrim`, and threading it
  through as a single `prefix` parameter to `UploadBackupFiles` (rather
  than each of upload/verify/delete separately reconstructing "which
  instance is this for") keeps the same value used everywhere a key is
  built or matched, so upload, verification, and delete can never
  disagree about a file's key.

**Consequences.** `buildUploadCommand` and `UploadBackupFiles` gain a
`prefix` parameter; `BackupArchiveParams`'s `Directory`/`Bucket`-style
plain fields didn't need to change since the prefix is derived from the
already-available picked instance, not a new prompt. Existing backups
already sitting at the bucket root today are not automatically
migrated -- an operator restoring from a backup uploaded before this
change should account for that when scripting a bulk migration into the
new per-instance prefixes, if desired.

---

## 2026-07-02 — Resolve a bucket's actual region before Backup Archive & Trim's access check

**Context.** Real-AWS testing against `sql-backups.library.caltech.edu`
failed with `AWS error [MovedPermanently]: Moved Permanently` on the
brand-new `s3:HeadBucket` preflight check (above). The debug log showed
the call went out scoped to `us-west-1` (`cfg.Regions[0]`, the region
`awsops`' single global S3 client has always used) -- but the bucket
isn't in that region. `HeadBucket`/`HeadObject` return a bare 301 with
no useful detail when the calling client's region doesn't match the
bucket's; DESIGN.md's "a bucket's home region is unrelated to the
instance's" was correct, but the code never actually resolved *which*
region a given bucket is in before talking to it.

**Decision.** `BackupArchiveAndTrim` now takes both the original
`s3Client` (used only to call the new `BucketRegion`, which resolves a
bucket's true region via `s3:GetBucketLocation` -- a control-plane call
that, unlike `HeadBucket`, works from a client scoped to any region) and
a `newS3Client func(ctx, region) (awsclient.S3API, error)` factory. Once
the bucket's region is known, `newS3Client` builds a client actually
scoped to that region, used for the `CheckS3BucketAccess` preflight and
every later `s3:HeadObject` verification call in the run.

**Rationale.**
- `s3:GetBucketLocation` exists specifically to answer "what region is
  this bucket in" without already knowing the answer -- it's the
  standard mechanism for this, not a workaround.
- A factory function (rather than eagerly building N per-region S3
  clients at startup, the way EC2/SSM clients are pre-built for every
  configured region) fits S3 better: buckets can be in any AWS region,
  not just the ones in `~/.awsops`' `regions` list, so there's no fixed
  set to pre-build against.
- Considered and rejected using
  `github.com/aws/aws-sdk-go-v2/feature/s3/manager`'s `GetBucketRegion`
  helper (which does something similar by inspecting a `HeadBucket`
  redirect's `x-amz-bucket-region` header) -- `go get` reported that
  module deprecated in favor of `feature/s3/transfermanager`, and
  `GetBucketLocation` alone, already available via the `service/s3`
  package already in use, needs no new dependency at all.

**Consequences.** `awsclient.S3API` gains `GetBucketLocation` alongside
`HeadObject`/`HeadBucket` (real client, logging decorator, and test fake
all updated). `BackupArchiveAndTrim`'s signature grows a
`newS3Client` parameter; `main.go` supplies both the initial
`cfg.Regions[0]`-scoped probe client and the factory closure.

---

## 2026-07-02 — Preflight check: S3 bucket access before Backup Archive & Trim's dry-run list

**Context.** Today's `sql-backups.library.caltech.edu` bucket test run
happened to fail for an unrelated reason (the target instance's AWS CLI
wasn't installed), but it highlighted a gap: if the operator's own
credentials can't reach the entered bucket at all (typo'd name, bucket
doesn't exist yet, missing `s3:ListBucket`), the workflow wouldn't find
out until the independent verification step, well after the dry-run
list, the destructive type-to-confirm gate, and a potentially large
upload have already run.

**Decision.** Right after the "S3 bucket" prompt, `BackupArchiveAndTrim`
calls a new `CheckS3BucketAccess`, which does an `s3:HeadBucket` with the
operator's own credentials and aborts immediately with an actionable
error (naming the bucket, and hinting at the likely cause) if it fails
-- before the dry-run list, before type-to-confirm, before any upload.

**Rationale.**
- `s3:HeadBucket` is the standard cheap existence-and-access check: no
  object needs to exist, and both "bucket doesn't exist" and "no
  permission" surface as an error from this one call.
- Checking immediately after the bucket name is entered, rather than
  waiting for the independent verification step later, means an
  operator who mistypes a bucket name finds out in seconds, not after
  a dry-run list and (potentially large) upload have already run.

**Consequences.** `awsclient.S3API` gains `HeadBucket` alongside the
existing `HeadObject`; every implementation (the real SDK client, the
logging decorator, and the test fake) had to add it. No functional
change to the upload/verify/delete pipeline itself.

---

## 2026-07-02 — Per-file upload progress for Backup Archive & Trim

**Context.** Real-AWS testing on `newauthors` (85 files, ~85 GB total)
surfaced that the upload phase's only feedback was a generic "...
uploading backup files to S3 (elapsed Xs)" heartbeat every 30 seconds --
no file count, no bytes, no way to tell whether it was actually making
progress or stuck, for a phase that can legitimately run for a long time
on a large backup set.

**Decision.** `UploadBackupFiles` now runs one `ssm:SendCommand` per
file instead of a single script covering the whole batch, and takes an
`onProgress func(UploadProgress)` callback invoked after each file
completes with a running `Done`/`Total` file count and `BytesDone`/
`BytesTotal`. `BackupArchiveAndTrim` uses this to print one line per
file: `... uploading 12/84 (1.2 GiB of 85.5 GiB) - OK <key>`.

**Rationale.**
- Real per-file progress requires the client to observe each file's
  outcome individually, which is only possible if each file is its own
  SSM command -- `ssm:GetCommandInvocation` doesn't expose partial
  stdout mid-script for a single long-running batched command.
- A callback (not a hardcoded print inside `UploadBackupFiles`) keeps
  the SSM-calling code testable without capturing terminal output, and
  leaves `nil` as a valid "don't care" value for callers that don't need
  a live display (as the existing unit tests exercise).

**Rejected alternatives.**
- *Keep one batched script, just upgrade the heartbeat to a spinner.*
  Cheaper, but still can't report which file or how far through the
  batch the operation actually is -- doesn't address the real ask.

**Consequences.** One `ssm:SendCommand`/`ssm:GetCommandInvocation`
round-trip pair per file instead of one for the whole batch -- more AWS
API calls and a bit more wall-clock overhead per file (each carries its
own poll-until-terminal wait), accepted in exchange for real progress
visibility on backup sets that can take a long time regardless.
`DefaultBackupUploadTimeout` (30 minutes) is now a per-file timeout
rather than a whole-batch timeout, which is more generous in aggregate
than before -- appropriate since a single ~1 GB file uploading within 30
minutes is already a very loose bound.

---

## 2026-07-02 — Configure per-instance backup directories by Name pattern

**Context.** Backup Archive & Trim's "Backup directory" prompt has no
default, by design (DESIGN.md, Feature 11) — every run requires an
explicit, deliberate value. In practice, though, the value is
predictable per machine: RDM instances all use `/opt/rdm_sql_backups`,
while some other service's instances use a different fixed path. Typing
the same known path from memory every run invites a typo, and there was
no way to record "this is the answer for this kind of instance" now
that `~/.awsops` (above) exists for exactly this kind of site-specific
setting.

**Decision.** `~/.awsops` gains `backup_directories`, an ordered list of
`{pattern, directory}` rules. `pattern` is matched against the picked
instance's Name tag using `path.Match`'s glob syntax (`*`, `?`,
`[...]`); the first matching rule's `directory` pre-fills the "Backup
directory" prompt as an editable default (`ui.WithDefault`) — pressing
Enter accepts it, typing something else overrides it. No match
(including an untagged instance with a blank Name) leaves the prompt
exactly as it behaved before this setting existed: required, no
default.

**Rationale.**
- Matching by Name (not the Project tag also carried by instances) was
  an explicit choice over Project-tag matching: Name tags already
  encode the kind of machine an operator is looking at (e.g. `rdm-*`
  covers every RDM instance across all its Projects) and glob patterns
  let one rule cover a whole family of instance names without requiring
  every instance to carry a distinct Project value.
- Pre-filling as an *editable* default, not skipping the prompt
  entirely, keeps this consistent with every other field on this
  workflow's confirmation gate: no field is silently accepted without
  the operator seeing it, since Backup Archive & Trim is a genuinely
  destructive operation (DESIGN.md, Feature 11).
- A malformed or non-matching pattern degrades to "no default" rather
  than an error — a typo'd pattern in `~/.awsops` shouldn't be able to
  break the workflow, only fail to save a keystroke.

**Rejected alternatives.**
- *Match by Project tag instead of Name.* Simpler in principle (one tag
  already used for grouping elsewhere), but doesn't fit the stated use
  case as well: multiple Projects can share the same backup directory
  convention (e.g. every RDM Project uses the same path), which a
  Name-glob (`rdm-*`) expresses in one rule where per-Project matching
  would need one entry per Project value.
- *Skip the prompt entirely on a match.* Faster, but removes the
  deliberate per-run check this workflow's other fields (age threshold,
  S3 bucket) also require with no silent defaults.

**Consequences.** `internal/config.BackupDirectoryRule` and
`BackupDirectoryFor` are new; `internal/workflow.BackupArchiveAndTrim`
gains a `backupDirRules []config.BackupDirectoryRule` parameter, wired
from `cfg.BackupDirectories` in `main.go`. `internal/workflow` now
imports `internal/config` for the first time.

---

## 2026-07-02 — Highlight PickList's prompt header when color is enabled

**Context.** Real-AWS testing surfaced a UX gap: after picking a main
menu action by number (e.g. typing 4 for Start instead of 5 for Stop),
the resulting pick list looks the same regardless of which action was
chosen -- the operator has to read through the whole list of instances
to notice the mismatch, since the only place the action name ("Select an
instance to start") appeared was buried at the bottom as the trailing
input prompt.

**Decision.** `ui.PickList` now prints its `prompt` argument as its own
line *before* the numbered list, wrapped in a bold ANSI escape when
color output is enabled (`ui.Highlight`), so the action name is the
first thing on screen. Color enablement for this is a new
package-level flag (`ui.SetColorEnabled`, read by `Highlight`), set once
in `main.go` from the same `ui.ColorEnabled()` result already computed
for `DisplayInstances`'s STATE column.

**Rationale.**
- The header must appear *before* the list, not just as the trailing
  input query, to actually save the reread the operator asked for --
  otherwise the highlighted text is the last thing read, not the first.
- A package-level flag (rather than threading a `colorEnabled bool`
  parameter through `PickList` and the ~26 call sites across 16
  workflow files that reach it) keeps this a small, low-risk change for
  a cosmetic feature. `DisplayInstances`/`DisplayImages` keep their
  existing explicit-parameter style since they're called directly from
  `main.go` with no intervening call chain.
- Bold (not a hue like green/red/yellow) avoids implying success,
  failure, or a warning -- meanings already claimed by
  `stateColor`'s use of color for instance state.

**Rejected alternatives.**
- *Thread `colorEnabled` through every workflow function down to
  `PickList`.* Consistent with `DisplayInstances`'s style, but a large
  diff (16 files, dozens of function signatures) for a purely cosmetic
  feature; revisit if a second cross-cutting per-call setting emerges
  that would justify threading a shared options struct instead.
- *Color the prompt only in its existing position (after the list).*
  Doesn't address the actual complaint -- the operator has already read
  the list by the time they reach it.

**Consequences.** `internal/ui/color.go` gains one small piece of
mutable package state (`colorEnabled`, defaulting to `false`, set once
at startup and never changed thereafter) -- acceptable since it's
write-once and read-only from that point on, and every unit test that
doesn't call `SetColorEnabled` observes the same disabled default as
before this change.

---

## 2026-07-02 — Add a `~/.awsops` YAML config file for awsops' own operational settings

**Context.** Narrowing configured regions (above) meant editing a Go
source constant and rebuilding. That's fine for a one-off, but the
conversation that led to it -- and the mention of S3 bucket settings the
S3 domain will eventually need -- made clear this will keep happening as
the tool grows into more domains (S3, CloudFront, Key Management), each
likely wanting its own site-specific defaults. Rebuilding the binary to
change a setting doesn't scale past the first one or two changes.

**Decision.** `awsops` now reads its own operational settings from an
optional YAML file at `~/.awsops` (overridable with `-config <path>`),
parsed with `gopkg.in/yaml.v3`. Scope is deliberately narrow: this file
covers only settings `awsops` itself needs to decide *how it operates*
(starting with which regions to manage) -- it explicitly does **not**
cover AWS credentials, profiles, or SSO configuration, which remain
entirely the AWS SDK's own responsibility via the standard chain
(`~/.aws/credentials`, `~/.aws/config`, environment variables) exactly
as today. `internal/config.Config` is a single flat struct with one
YAML-tagged field per setting (`Regions []string` today); the file is
optional (missing = built-in defaults, `[us-west-1, us-west-2]` for
regions), a field left unset in an otherwise-valid file falls back to
its own default independently (not all-or-nothing), and a malformed
file is a hard error, not a silent fallback.

**Rationale.**
- Directly what was asked: config that will "grow to have more fields
  over time," built for that now rather than retrofitted later, while
  keeping today's actual change (regions) the only field that does
  anything yet.
- The `~/.aws` boundary matters: conflating "which regions does awsops
  manage" with "which AWS account/credentials is this" would blur two
  genuinely different concerns and risk awsops silently reading or
  fighting with the AWS CLI's/SDK's own config files.
- Per-field defaults (not all-or-nothing) mean a config file only ever
  needs to state what's actually being overridden -- a two-line
  `regions:` file today doesn't need to also restate every future
  setting just because the file exists at all.
- No versioning/migration machinery: this is a single-operator-
  maintained local dotfile, not a multi-tenant service config pushed to
  many machines by many people -- schema evolution here is "add a field
  to the struct," not a problem that needs solving in advance.

**Rejected alternatives.**
- *JSON via the standard library, no new dependency* -- raised as the
  no-new-dependency alternative; explicitly declined in favor of YAML
  for hand-editability, with `gopkg.in/yaml.v3` approved for this
  specific use.
- *Environment variables only* -- rejected as not scaling to structured/
  list settings (a region list today; likely nested settings later, e.g.
  per-domain defaults) the way a YAML file naturally does.
- *A versioned/migrating config schema* -- rejected as solving a problem
  this tool doesn't have: there's one file, on one machine, maintained
  by the person running the tool, not a config format needing backward-
  compatibility guarantees across independently-upgrading consumers.

**Consequences.**
- New dependency: `gopkg.in/yaml.v3`.
- New package: `internal/config` (`Config`, `DefaultRegions`,
  `DefaultPath`, `Load`).
- `internal/awsclient/regions.go` and `regions_test.go` removed --
  region-list ownership moves entirely to `internal/config`;
  `internal/awsclient/client_test.go`'s sanity test now iterates a
  small test-local region literal instead of a shared package var,
  decoupling it from wherever the "real" list lives.
- `cmd/awsops/main.go` gains a `-config` flag (default
  `config.DefaultPath()`), loads the config early (failing fast on a
  parse error, matching every other startup failure mode), and uses
  `cfg.Regions` everywhere `awsclient.Regions` was read before.

---

## 2026-07-02 — Tolerate GetCommandInvocation's post-SendCommand eventual-consistency window

**Context.** A real launch succeeded all the way through `RunInstances`
and reaching `running`, then failed during the cloud-init completion
check: `Error: AWS error [InvocationDoesNotExist]: ` immediately after
`ssm:SendCommand`. The `-debug` log showed exactly one `SendCommand`
followed by exactly one `GetCommandInvocation`, which failed -- the
identical shape of bug as `InvalidInstanceID.NotFound`/`InvalidAMIID.NotFound`
(both fixed earlier this session), just on the SSM side: a newly
submitted command invocation can be briefly invisible to
`ssm:GetCommandInvocation` for a few seconds after `ssm:SendCommand`
returns its ID.

**Decision.** `RunShellCommand`'s poll loop now tolerates AWS's own
`InvocationDoesNotExist` the same way it already tolerates "not in a
terminal status yet" -- keep polling instead of returning the error. Any
other `GetCommandInvocation` error still fails immediately, unchanged.

**Rationale.** Exactly the precedent set by the two earlier fixes in
this same family (DECISIONS.md, "Tolerate DescribeInstances'
post-RunInstances eventual-consistency window"): this is documented AWS
eventual-consistency behavior, not something specific to this account,
so the fix is to expect it during polling, not to work around it
operationally.

**Rejected alternatives.** None -- same reasoning as the two prior fixes
in this family; no alternative was seriously considered.

**Consequences.**
- `internal/workflow/ssm.go`: `isInvocationNotYetVisible`, following the
  exact naming/shape convention of `isInstanceNotYetVisible`
  (`launch_execute.go`) and `isImageNotYetVisible`
  (`create_ami_execute.go`).
- This is now the third instance of the identical bug pattern found in
  as many real-AWS testing sessions (`RunInstances`/`DescribeInstances`,
  `CreateImage`/`DescribeImages`, `SendCommand`/`GetCommandInvocation`)
  -- worth remembering as a general AWS API shape (submit an async
  operation, get an ID back, immediately query that ID) rather than
  three unrelated one-off bugs, if a fourth case turns up in an
  unreviewed code path.

---

## 2026-07-02 — Validate key pair name against the AMI's region

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

## 2026-07-02 — Narrow configured regions to us-west-1/us-west-2

**Context.** The official-Ubuntu-AMI addition (above) surfaced a real
launch failure (`InvalidKeyPair.NotFound`) precisely because it made
`us-west-1` -- one of the four originally-configured regions this team
doesn't actually run anything in -- selectable as a base-AMI region for
the first time. The account's real resources (key pairs, security
groups, subnets already provisioned for real use) only exist in
`us-west-1`/`us-west-2` in practice; `us-east-1`/`us-east-2` were
configured from the start but never actually used.

**Decision.** `awsclient.Regions` narrowed from
`{us-east-1, us-east-2, us-west-1, us-west-2}` to `{us-west-1,
us-west-2}`. Every region-fanned-out listing, pick list, and lookup
(instances, AMIs, key pairs once Key Management ships, official Ubuntu
AMI lookup, etc.) automatically follows since they all iterate over this
one slice -- no other code changes needed.

**Rationale.** Directly shrinks the blast radius of the class of bug
just found: every region-scoped resource (key pairs, security groups,
subnets) that doesn't exist in a region this team never uses can no
longer surface unexpectedly through a feature (like the official-Ubuntu
lookup) that fans out across every configured region. This doesn't
replace the deeper fix (validating a chosen key pair actually exists in
the target region -- tracked separately) but removes the two regions
most likely to produce this exact surprise with zero cost, since nothing
runs there anyway.

**Rejected alternatives.**
- *Leave all four regions configured, rely solely on per-resource
  validation* -- rejected as not mutually exclusive with this change;
  both are worth doing. Narrowing regions fixes the "AMI in a region we
  don't use" case at the root; validation (separate work) still matters
  for genuine two-region mismatches (e.g. a key pair that only exists in
  `us-west-2` being typed while launching into `us-west-1`).

**Consequences.**
- `internal/awsclient/regions.go`, `regions_test.go` updated.
- `DESIGN.md`/`helptext.go` updated; `awsops.1.md` regenerates from
  `helptext.go` via the existing `cmt`/Makefile pipeline, not edited by
  hand.
- Every existing region-fanned-out feature (instance/AMI listing,
  official Ubuntu AMI lookup) now makes two round-trips instead of four
  per refresh/launch -- strictly less work, no behavior change beyond
  which regions are included.

---

## 2026-07-02 — Fix official Ubuntu AMI name filter pattern

**Context.** Real-AWS testing of the just-added official-Ubuntu-AMI
feature (above): the pick list showed only the account's own AMIs, no
Ubuntu entries, with no error printed -- exactly the designed best-
effort fallback behavior, which made it silent rather than obviously
broken. The `-debug` JSONL log showed why: every `EC2.DescribeImages`
call scoped to Canonical's owner ID (`099720109477`) returned zero
images, with no error, for both curated releases, every time. The
`name` filter value (`"ubuntu-noble-24.04-amd64-server-*"`, no leading
wildcard) only matches AMI names that *start* with that literal string
-- but Canonical's real, published AMI names are prefixed with a
path-like `ubuntu/images/hvm-ssd/` (or the newer
`ubuntu/images/hvm-ssd-gp3/`), so the filter could never match anything,
in any region, ever.

**Decision.** Both curated name patterns gained a leading
`ubuntu/images/hvm-ssd*/` segment --
`"ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*"` and the
Jammy equivalent -- anchoring to Canonical's actual documented naming
convention (the trailing `*` after `hvm-ssd` covers both the `hvm-ssd`
and `hvm-ssd-gp3` root-volume-type variants) instead of a bare suffix
match.

**Rationale.** This is exactly the kind of mistake real-AWS testing (not
unit tests against a fake) is positioned to catch: the fake's
`officialUbuntuImages` map is keyed by whatever literal string the
production code happens to pass in, so a test using the same wrong
literal for both "what the code searches for" and "what the fake
returns" passes without ever validating that string against AWS's
actual naming rules. Nothing about the unit tests was wrong; they
simply couldn't have caught this class of error on their own -- another
concrete case for why `TEST_PLAN_REAL_AWS.txt` and `-debug` remain load-
bearing, not just a formality after unit tests pass.

**Rejected alternatives.** None -- this is a factual correction to match
Canonical's real naming convention, not a design trade-off.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go`: `curatedUbuntuReleases`'
  `namePattern` values corrected; a code comment now records the exact
  failure mode (silent zero-match, not an error) so a future change to
  Canonical's naming convention is easier to recognize if it recurs.
- Test fixtures (`nobleNamePattern` constant, shared across
  `official_ubuntu_amis_test.go` and both launch-flow integration tests)
  updated to the corrected pattern for consistency, though as noted
  above this was necessary for consistency, not sufficient on its own to
  have caught the original bug.

---

## 2026-07-02 — Offer official Ubuntu LTS AMIs alongside owned AMIs when picking a base AMI

**Context.** Follow-up to clarifying the Create-from-Cloud-Init-YAML
workflow: the user's actual goal was launching an entirely new machine
from a stock base image + cloud-init, not from one of the account's
existing, already-application-specific AMIs (`plots-backup`,
`newauthers-clone-2026-06-25`, `authors-2024-03-07` -- all pre-existing
snapshots, not generic OS images). Since the AMI pick list is scoped to
AMIs the account owns (a deliberate existing decision -- otherwise the
list would include every public AMI in existence), a stock Ubuntu AMI
never appeared as an option. The user's own framing: keep it simple,
cover the likely common case (official Ubuntu images, plus what's
already owned); if something more exotic is needed, copying the
specific public AMI into the account first (already-documented guidance)
remains the answer.

**Decision.** The "Select an AMI"/"Select a base AMI" pick list (Feature
2/3) now also includes a small, curated list of official Ubuntu LTS
releases -- currently 24.04 (Noble Numbat) and 22.04 (Jammy Jellyfish),
amd64/x86_64 only -- resolved via `ec2:DescribeImages` against
Canonical's well-known, publicly documented AWS account ID
(`099720109477`), picking the single most recently published AMI per
release per region. This lookup happens once, on demand, right before
the AMI pick list is shown (not as part of the general resource-listing
refresh, which stays owned-AMIs-only, unchanged) -- launching an
instance is an infrequent, deliberate action, so a handful of extra
`DescribeImages` calls at that moment is not the same cost concern it
would be if it ran on every screen refresh. Best-effort: if the lookup
itself errors, the picker silently falls back to owned AMIs only, same
as this tool's other best-effort diagnostics.

**Rationale.**
- Matches the explicit scope given: "pretty simple," "cover the likely
  bases," with anything more exotic staying a manual (already-documented)
  copy-the-public-AMI-in step -- not a general public-AMI browser.
- amd64-only matches the curated instance-type list's architecture
  (2026-07-02, "Instance type pick list: curated shortlist, not the full
  AWS catalog") -- none of the curated instance types are Graviton/arm64,
  so offering arm64 Ubuntu AMIs would create options that don't actually
  pair with anything in the other curated list.
- Carrying `EnaSupport` through from the real `DescribeImages` response
  (not defaulting it) matters here specifically: official Ubuntu AMIs
  are modern and genuinely ENA-enabled, so without this the instance-
  type-vs-AMI-ENA-support pre-flight check (2026-07-02, above) would
  wrongly flag every one of them as incompatible with the curated
  instance types that actually work fine with them.

**Rejected alternatives.**
- *A general public-AMI browser/search* -- explicitly declined by the
  user in favor of a small curated set; a full public-AMI search is a
  much bigger feature (arbitrary owner IDs, name search, architecture
  filtering UI) that isn't needed for the stated common case.
- *Include arm64/Graviton variants now* -- deferred: no curated instance
  type could launch one today; revisit if/when Graviton types are added
  to the curated instance-type list.
- *Fetch these as part of the general resource-listing refresh* --
  rejected: that listing is specifically scoped to "what does this
  account own" (an oversight/inventory view); Canonical's AMIs aren't
  owned by the account and aren't something this team needs to track,
  only something useful at the moment of picking a base image.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go` (new): `latestUbuntuAMI`,
  `listOfficialUbuntuAMIsInRegion`, `listOfficialUbuntuAMIs`,
  `imagesWithOfficialUbuntu`.
- `launch_instance.go`/`launch_from_cloud_init.go`: both AMI pick lists
  now go through `imagesWithOfficialUbuntu` before display.
- No new AWS permissions -- `ec2:DescribeImages` is already required
  (DESIGN.md, Assumptions), and querying it against a different `Owners`
  value needs no additional IAM grant.

---

## 2026-07-02 — Create EC2 Instance from Cloud-Init YAML always reads from a file

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

## 2026-07-02 — Auto-detect a bare existing-file path in User data / Cloud-init YAML input

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

## 2026-07-02 — Move "Show resource lists" to the top of the Compute menu; rename from "Refresh"

**Context.** User feedback: "Refresh resource lists" sat near the bottom
of the Compute menu (item 11 of 12), and "Refresh" was ambiguous about
what it actually does (re-fetch from AWS and redisplay both tables, not
just repaint the screen). Since every other successful action already
triggers an automatic refresh afterward (2026-06-30, "Refresh data after
each operation"), this item's real purpose is letting the operator
deliberately re-orient -- see current state -- without taking an action,
which is a natural first move on entering the domain, not action #11 of
12.

**Decision.** Renamed to "Show resource lists" and moved to menu
position 1; every other item shifts down by one, "Back to domain picker"
stays last (position 12, unchanged, since the total item count didn't
change). The underlying behavior and `MenuActions.Refresh` field name
are unchanged -- this is a label and position change only.

**Rationale.** Matches how the operator actually uses this tool: check
what's running, then act -- not "act ten times, and eleventh, maybe
check." "Show" also describes the operator-visible effect (the two
tables reappear) rather than an AWS-side connotation ("refresh" could
read as "refresh the AWS resources themselves").

**Rejected alternatives.** None seriously considered -- this is a small,
low-risk UX tweak; no behavior, permissions, or data flow changes.

**Consequences.**
- `internal/workflow/menu.go`'s `mainMenuItems` reordered; every test in
  `menu_test.go` that referenced a menu item by number was updated to
  match (item numbers shift by one; "Back to domain picker" stays 12).
- `DESIGN.md`'s Compute Menu ASCII diagram and `TEST_PLAN_REAL_AWS.txt`'s
  menu-order checklist updated to match, preserving existing `[ok]`
  markers against their renamed/renumbered items (the capability was
  already verified; only its label and position changed). Also fixed
  unrelated staleness noticed while there: `TEST_PLAN_REAL_AWS.txt` still
  said item 12 was "Exit" from before the domain-picker refactor
  (2026-07-02, "Redesign navigation as a domain picker...") -- corrected
  to "Back to domain picker".

---

## 2026-07-02 — Filter the subnet picker by instance-type Availability Zone support

**Context.** Real-AWS testing surfaced repeated back-and-forth: pick an
instance type, pick a subnet, get told after the fact ("Instance type
... is not offered in ... this subnet's Availability Zone") that the two
don't work together, then recover via a pick list. The user's framing:
"the choices are out of context" -- since instance type is already
chosen by the time Subnet ID is prompted, the tool already has enough
information to never offer an incompatible subnet in the first place,
rather than offering it and then walking the operator back out.

**Decision.** `promptSubnetID` now takes the already-chosen
`instanceType` and narrows its subnet listing to those whose
Availability Zone actually offers it
(`filterSubnetsByInstanceTypeAZ`, reusing `instanceTypeOfferedAZs`) --
instance type stays the first choice (unchanged position in the flow;
it's the workload-driven decision -- cost, performance, ENA-compatibility
with the AMI), and the network choice narrows around it, not the other
way around. Filtering is best-effort and never a dead end: if the
AZ-offerings lookup itself errors, or if filtering would leave zero
subnets to pick from, `promptSubnetID` falls back to showing the full,
unfiltered list. `ensureInstanceTypeSupportedInSubnet`'s reactive
recovery pick list (2026-07-02, above) is unchanged and stays in place as
the safety net for exactly those two fallback cases (and the free-text-
fallback path, where the AZ isn't known at all) -- in the common case
where filtering succeeds, that reactive check now simply finds the
already-filtered subnet compatible on the first try and returns
immediately, invisible to the operator.

**Rationale.** Matches the "different routes through the same choices
should all reach a running system" framing directly: narrowing options
before they're offered, instead of discovering and recovering from an
incompatible combination after the fact, removes a whole category of
back-and-forth without removing any actual capability -- every subnet
that could have worked is still offered; only the ones that couldn't
are pre-filtered out.

**Rejected alternatives.**
- *Reorder to prompt for subnet before instance type* -- rejected (see
  the exploratory discussion this decision follows from): instance type
  is the more workload-driven decision and should stay a free first
  choice; subnet is more of an implementation detail that can be
  narrowed once the type is known. Reordering would also do nothing for
  the unrelated instance-type-vs-AMI-ENA-support check, which depends on
  the AMI (chosen long before either instance type or subnet), not the
  network.
- *Remove `ensureInstanceTypeSupportedInSubnet` now that filtering
  exists* -- rejected: it's still the only thing that catches an
  incompatibility when filtering itself couldn't run (lookup error) or
  couldn't narrow anything (all known subnets incompatible) or wasn't
  attempted at all (free-text fallback, unknown AZ). Removing it would
  turn those cases back into dead ends.

**Consequences.**
- `promptSubnetID`'s signature gained an `instanceType string` parameter;
  its three call sites (`launch_instance.go`, `launch_from_cloud_init.go`,
  and `ensureInstanceTypeSupportedInSubnet`'s "Pick a different subnet"
  branch) already had the instance type in scope, so no new plumbing was
  needed beyond passing it through.
- No new AWS permissions or calls in the common case -- the same
  `ec2:DescribeInstanceTypeOfferings` call `ensureInstanceTypeSupportedInSubnet`
  already made reactively now happens once, proactively, per launch.

---

## 2026-07-02 — Tolerate DescribeInstances' post-RunInstances eventual-consistency window

**Context.** A real launch (subnet/instance-type mismatch resolved,
confirmed, `RunInstances` succeeded) immediately failed anyway: `Launched
i-088ab06fb0c16eb0b, waiting for it to reach running... Error: AWS error
[InvalidInstanceID.NotFound]: The instance ID 'i-088ab06fb0c16eb0b' does
not exist`. This is documented AWS behavior, not a real failure: a newly
launched instance ID can be briefly invisible to `ec2:DescribeInstances`
for a few seconds after `ec2:RunInstances` returns it, before the
instance is fully registered. `waitUntilState` (backing `WaitUntilRunning`,
used by every launch and by Start Instance) treated *any*
`DescribeInstances` error as fatal, so this blocked every single launch
that happened to hit the window -- not an edge case, a near-certain
race every time.

**Decision.** `waitUntilState` now tolerates AWS's own
`InvalidInstanceID.NotFound` the same way it already tolerates "not in
the wanted state yet" -- keep polling instead of returning the error.
Any other `DescribeInstances` error still fails immediately, unchanged.
Found and fixed the identical exposure on the AMI side while here:
`WaitForAMIAvailable` could hit the equivalent `InvalidAMIID.NotFound`
right after `ec2:CreateImage` returns, before `ec2:DescribeImages`
recognizes the new image -- same tolerance added there
(`isImageNotYetVisible`), even though it hadn't been reported yet, since
it's the exact same class of bug on the exact same code path shape.

**Rationale.** This is a well-known, documented AWS eventual-consistency
behavior (not specific to this account or these instances) -- the fix is
to expect it, not work around it operationally (e.g. "just retry the
whole launch"). Fixing the AMI-side analog preemptively, rather than
waiting for a second bug report, matches this session's pattern of
fixing the failure *class*, not just the exact reported instance of it.

**Rejected alternatives.**
- *Retry the whole launch flow on this error* -- rejected: the instance
  already launched successfully; retrying `RunInstances` would create a
  second, redundant instance instead of just waiting a few more seconds
  for the first one to become visible.
- *A fixed short sleep before the first `DescribeInstances` call* --
  rejected in favor of tolerating the specific error code during normal
  polling: simpler, no new timing constant to tune, and self-correcting
  regardless of how long the window actually is.

**Consequences.**
- `internal/workflow/launch_execute.go`: `isInstanceNotYetVisible`.
- `internal/workflow/create_ami_execute.go`: `isImageNotYetVisible`.
- No new AWS permissions -- purely client-side error handling around
  calls this tool already makes.

---

## 2026-07-02 — Add non-ENA-required options to the curated instance type list

**Context.** Trying to launch from a real, legacy AMI (`etd-workflow-v0.0.1`)
that isn't ENA-enabled, every entry in the newly-added curated instance
type list (t3/m5/c5/r5) failed `ensureInstanceTypeENACompatible` --
all nine are Nitro-based and require ENA unconditionally. The "Change
instance type" recovery pick list technically worked, but every
alternative it could offer was equally incompatible: the operator was
launch-blocked with no way to get unstuck without already knowing (from
outside awsops) that e.g. `t2.micro` would work, and typing it via
"Other". This is a real gap in the curated list, not a one-off: any
sufficiently old AMI (common for long-lived, hand-maintained gold
images) hits the same dead end.

**Decision.** Add `t2.micro` and `t2.medium` to `curatedInstanceTypes`
as the list's only non-Nitro, no-ENA-required entries, each labeled
"no ENA required, works with older/legacy AMIs" so they're
self-explanatory in the pick list, not just a name an operator has to
already recognize. Every ENA-requiring entry's label now also says
"(requires ENA)" for the same reason -- so the *first* pick, not just
the recovery pick, can be an informed choice. `ensureInstanceTypeENACompatible`'s
incompatibility message now explicitly suggests these two by name, plus
a one-line pointer to the actual (out-of-scope-for-awsops) permanent
fix: enabling ENA on the source instance and re-creating the AMI.

**Rationale.** The recovery pick list (DECISIONS.md, "Pre-flight check:
instance type vs. AMI ENA support") is only actually useful if it can
offer *some* type that works; a list where every option shares the same
failure mode isn't a recovery path, it's a longer way to the same dead
end. Two low-cost, universally-available legacy types cover the
common case without expanding the list's scope back toward "the full
AWS catalog," which the curated-list decision (above) already rejected.

**Rejected alternatives.**
- *Make promptInstanceType AMI-aware (filter or reorder based on the
  picked AMI's EnaSupport)* -- rejected as unnecessary complexity for
  now: the static list already contains a working answer once it
  includes non-ENA options; the pre-flight check's message pointing at
  them by name accomplishes the same practical outcome without the
  static-list design changing shape or `promptInstanceType` needing new
  parameters/context it didn't have before.
- *Only fix it via the incompatibility message, without adding to the
  curated list* -- rejected because the *first* instance-type pick
  (before any AMI-compatibility check has even run) should also be able
  to make an informed choice for a known-legacy AMI, not just recover
  from a bad one after the fact.

**Consequences.**
- `curatedInstanceTypes` grew from 9 to 11 entries; "Other" shifted from
  pick-list position 10 to 12.
- No new AWS permissions or calls -- purely a static list change plus a
  message update.

---

## 2026-07-02 — Pre-flight check: instance type vs. AMI ENA support

**Context.** Real-AWS testing hit `AWS error [InvalidParameterCombination]:
Enhanced networking with the Elastic Network Adapter (ENA) is required
for the 't3.small' instance type. Ensure that you are using an AMI that
is enabled for ENA.` -- this is the ENA pre-flight check idea already
queued in TODO.md since an earlier session (two real launch failures
that day: AMI `ami-0da49db6a772dda02` isn't ENA-enabled, both `t3.micro`
and now `t3.small` require it). With the AZ pre-flight check (above)
just implemented as a template, this was the natural next failure class
to close.

**Decision.**
- `inventory.Image` gained an `EnaSupport bool` field, populated for
  free from the same `ec2:DescribeImages` call `ListImages` already
  makes (the SDK's `Image.EnaSupport` field) -- no extra AWS call for
  the AMI side.
- After Instance type is picked (Feature 2/3), check whether it
  requires ENA (`ec2:DescribeInstanceTypes`,
  `NetworkInfo.EnaSupport == Required`) and, if so, whether the
  already-picked AMI supports it. If not, print the incompatibility and
  show a pick list: **Change instance type** or **Abort this launch** --
  no "pick a different AMI" option, unlike the AZ check's "pick a
  different subnet": swapping the AMI this late would mean redoing
  earlier choices that depend on it (e.g. the Project tag default), so
  aborting and restarting covers that case instead, same as any other
  declined confirmation.
- "Abort" reuses `ui.ErrCancelled`, same as the AZ check.
- "Change instance type" reuses `promptInstanceType` (the curated pick
  list, below), not a bespoke free-text prompt -- for consistency, both
  pre-flight checks' recovery flows now go through the same instance-
  type entry point.

**Rationale.**
- Closes the exact TODO.md item this team already flagged, using the
  same pick-list-recovery pattern just established for the AZ check
  rather than inventing a third UX shape.
- Getting the AMI's `EnaSupport` for free from data already fetched
  avoids adding a new per-check AWS call on the AMI side.

**Rejected alternatives.**
- *Also offer "pick a different AMI"* -- rejected because the AMI's
  already-collected downstream effects (Project tag default, region-
  scoped clients) would need to be redone; abort-and-restart is simpler
  and matches this tool's existing cancellation semantics.
- *A shared multi-check framework covering both AZ and ENA together* --
  still rejected for now, per the AZ check's own decision above: two
  checks doesn't justify an abstraction yet.

**Consequences.**
- New EC2 permission required: `ec2:DescribeInstanceTypes` (see
  `DESIGN.md`, "Assumptions").
- `internal/workflow/instance_type_ena_check.go` (new):
  `instanceTypeRequiresENA`, `ensureInstanceTypeENACompatible`,
  `enaIncompatibilityChoices` (reuses `incompatibilityChoice`/
  `incompatibilityChoiceLabel` from the AZ check).

---

## 2026-07-02 — Instance type pick list: curated shortlist, not the full AWS catalog

**Context.** The "Instance type" prompt was free text with only a
suggested default (`t3.micro`). Asked whether it could become a pick
list like Security group IDs/Subnet ID. AWS offers 600+ instance types
per region -- listing them all (even paginated) would reproduce, at a
much larger scale, the exact "flat list of every key pair in the
account... was noise, not help" problem already found and rejected for
key pairs at just 16 entries (2026-07-01 decision, "Support creating a
new key pair from within awsops"). A full list would also need
architecture filtering (x86_64 vs. arm64) against the picked AMI to
avoid creating a *new* incompatibility class right after fixing three
others this session (key pair name, IAM instance profile, instance-
type/AZ) -- `inventory.Image` doesn't carry AMI architecture today.

**Decision.** `promptInstanceType` offers a short, hand-picked list of
~9 types relevant to this team's actual usage (t3 family for testing/
small Invenio RDM instances, m5/c5/r5 for steady-state/compute/memory-
optimized needs), each labeled with vCPU/memory, plus "Other" to type
any value not listed. No AWS call is made to build this list -- it's
static. The instance-type-vs-AZ and instance-type-vs-ENA pre-flight
checks (this file, both entries above) are what actually validate
whatever value is chosen (curated or typed) against AWS, so the list
itself doesn't need to be exhaustive or live to be safe.

**Rationale.** Matches this project's established preference (key
pairs) for a short, curated list plus an escape hatch over an
exhaustive one; the real safety net against picking an incompatible
type is the two pre-flight checks, not an exhaustive picker.

**Rejected alternatives.**
- *Full list filtered by region + AMI architecture* -- rejected for
  now: still likely 100-300+ entries even filtered, and requires adding
  Architecture to `inventory.Image` and a new filtering call. Worth
  reconsidering if the curated list proves too restrictive in practice.
- *Full list, region-only, no architecture filter* -- rejected as the
  most noise for the least benefit: hundreds of entries, some of which
  wouldn't even work with the picked AMI.

**Consequences.**
- `internal/workflow/launch_prompts.go` gained `promptInstanceType`,
  `curatedInstanceTypes`, `instanceTypeChoice`/`instanceTypeChoiceLabel`.
  No new AWS permissions -- the list is static.
- The "Change instance type" recovery step in both pre-flight checks
  (above) now goes through this same function, so a corrected value
  also comes from the curated list + "Other", not a separate free-text
  prompt.

---

## 2026-07-02 — Pre-flight check: instance type vs. subnet Availability Zone

**Context.** The `-debug` JSONL log from the same real-AWS testing
session that surfaced the key-filename bug (above) showed a second,
unrelated real failure in the middle of that sequence: once the key
pair name was correct, `RunInstances` failed with `Unsupported: Your
requested instance type (t2.micro) is not supported in your requested
Availability Zone (us-west-2d). Please retry your request by not
specifying an Availability Zone or choosing us-west-2a, us-west-2b,
us-west-2c.` -- the picked subnet (`subnet-5870b473`) sits in
`us-west-2d`, which doesn't offer `t2.micro`. This is the same general
class of problem as the already-deferred ENA pre-flight check idea
(TODO.md) -- an instance-type/launch-parameter incompatibility AWS only
reports after the fact -- but a different specific incompatibility (AZ
offering, not ENA support).

**Decision.**
- After the Subnet ID prompt (Feature 2/3), check whether the
  already-chosen instance type is actually offered in the picked
  subnet's Availability Zone (`ec2:DescribeInstanceTypeOfferings`,
  `LocationType=availability-zone`).
- If it isn't, print the incompatibility and (best-effort) the AZs the
  instance type *is* offered in, then show a pick list: **Change
  instance type**, **Pick a different subnet**, or **Abort this
  launch** -- rather than a dead-end error message or silently sending
  a `RunInstances` call already known to fail.
- "Abort this launch" returns `ui.ErrCancelled`, reusing the exact same
  cancellation path every other declined/cancelled confirmation in this
  tool already uses (`CreateInstanceFromAMI`/`CreateInstanceFromCloudInit`
  catch it and print "Cancelled.", returning to the domain menu) --
  no new cancellation mechanism needed.
- The check is skipped entirely (not just tolerantly failed) when the
  subnet's Availability Zone is unknown -- i.e. `promptSubnetID` fell
  back to its free-text prompt -- or when the check call itself errors,
  matching this tool's existing "best-effort diagnostic, never blocks
  the whole flow" pattern (e.g. SSM-unavailable fallbacks).
- Scoped to this one incompatibility class for now, not a general
  multi-check framework -- the ENA-support variant (TODO.md) remains a
  separate, not-yet-implemented item; if a third class of
  incompatibility turns up, that's the point to reconsider a shared
  abstraction, not before.

**Rationale.**
- Fixes a real failure found in the same debug-log session as the key-
  filename bug, using the tool that made it discoverable in the first
  place (the `-debug` log) rather than guessing.
- A pick list of concrete remediation options (change type / change
  subnet / abort) is more actionable than a printed error the operator
  has to interpret and act on by restarting the flow -- and matches an
  explicit request that error recovery should offer a pick list, not
  just a message.
- Reusing `ui.ErrCancelled` for "abort" avoids inventing a second
  cancellation contract alongside the one this tool already has.

**Rejected alternatives.**
- *Generalize into a multi-check pre-flight framework covering ENA and
  AZ together* -- rejected for now as premature: only one check is
  actually implemented; building shared abstraction for a framework of
  one (plus one still-deferred idea) isn't justified yet.
- *Just show a better error message, no recovery pick list* -- rejected
  per explicit direction that a pick list to correct or abort is the
  right shape once a chosen setting turns out to be invalid.

**Consequences.**
- `promptSubnetID`'s return type changed from `(string, error)` to
  `(SubnetInfo, error)`, so its caller has the picked subnet's
  Availability Zone available without a redundant lookup -- `SubnetInfo`
  already carried this field for the pick-list label. The free-text
  fallback path returns `SubnetInfo{SubnetID: ...}` with an empty
  `AvailabilityZone`, which is exactly the "unknown, skip the check"
  signal `ensureInstanceTypeSupportedInSubnet` looks for.
- New EC2 permission required: `ec2:DescribeInstanceTypeOfferings` (see
  `DESIGN.md`, "Assumptions").
- No new AWS SDK dependency -- `DescribeInstanceTypeOfferings` is
  already part of the `ec2` package this tool depends on.

---

## 2026-07-02 — Derive the AWS key pair name from a private key filename/path

**Context.** Real-AWS testing hit `AWS error [InvalidKeyPair.NotFound]:
The key pair '~/.ssh/etd-ami-test.pem' does not exist` -- the operator
typed the private key's file path at "Key pair name" instead of the AWS
key pair name, despite the prompt's own explicit "not a local file path"
wording. The `-debug` JSONL log showed the full sequence of what was
tried in one session: `etd-ami-test.pem` (bare filename with extension,
no directory) failed the same way, then the correct bare name
`etd-ami-test` worked (it got past `RunInstances`' key-pair check
entirely and failed for an unrelated reason -- see the separate
instance-type/Availability-Zone finding this surfaced, tracked in
TODO.md), then `~/.ssh/etd-ami-test.pem` was tried and failed again.
`ssh -i` muscle memory makes typing the key *file* rather than the key
*pair name* a recurring mistake, not a one-off typo.

**Decision.**
- `promptKeyPairNameOrCreate` now recognizes input that looks like a
  private key filename or path -- contains `/`, starts with `~`, or ends
  in `.pem`/`.ppk`/`.key` (case-insensitive) -- as distinct from a bare
  AWS key pair name.
- When recognized, the file is validated as actually readable (`~` is
  expanded against the home directory; a bare filename with no directory
  component that isn't readable relative to the current directory is
  also checked against this tool's own key directory, since that's where
  Create Key Pair saves keys and where a bare filename most plausibly
  lives) before anything is derived from it -- an unreadable path
  re-prompts with a clear local error instead of being sent to AWS,
  which would otherwise fail distantly and confusingly.
- Once validated, the AWS key pair name is derived from the file's
  basename with its extension stripped (e.g. `~/.ssh/etd-ami-test.pem`
  or bare `etd-ami-test.pem` -> `etd-ami-test`) and used as the launch's
  key pair name, with an on-screen note explaining what happened so the
  operator isn't surprised by what gets sent to AWS.
- This works because it's this tool's own convention: Create Key Pair
  (`createKeyPair`) always saves a new key's private material to exactly
  `<keyDir>/<name>.pem`, so the filename reliably encodes the real AWS
  key pair name regardless of which directory (or none) the operator
  typed.

**Rationale.**
- Fixes the actual reported failure and the two variants of it found in
  the same debug session (bare filename-with-extension, and full path),
  not just the one exact string from the bug report.
- Auto-deriving (rather than just rejecting with a "that looks like a
  path" message) is safe specifically because this tool controls the
  naming convention on the writing side (Create Key Pair) as well as the
  reading side (this prompt) -- it isn't guessing at an external
  convention it doesn't control.

**Rejected alternatives.**
- *Reject with a clarifying error instead of deriving* -- raised as an
  explicit scope question and declined in favor of the more helpful
  auto-derive path, consistent with this project's general preference
  (Security groups/Subnet ID, IAM instance profile) for fixing an
  AWS-error class locally rather than just describing it better.

**Consequences.**
- `internal/workflow/create_key_pair.go` gained `looksLikeKeyFilename`,
  `keyPairNameFromFilePath`, and `isReadableFile`; the existing "new"
  sub-flow was extracted into `createNewKeyPairInteractive` unchanged, so
  `promptKeyPairNameOrCreate` could loop on a bad key-filename input
  without duplicating that retry logic.
- No new AWS permissions or SDK dependencies -- this is entirely local
  validation before any AWS call is made.

---

## 2026-07-02 — Support picking or creating an IAM instance profile from within awsops

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

## 2026-07-02 — CloudFront + OAC by default for static websites, not public-read buckets

**Context.** Scoping the new S3 domain's static-website primitive
(Feature 19/24 in `DESIGN.md`) raised a real security-relevant default:
the classic "S3 static website hosting" pattern most tutorials show
makes the bucket world-readable via a public-read bucket policy. This
team already treats public-AMI/public-exposure as something to warn
about explicitly (`DESIGN.md` Security Considerations #4), and a tool
that makes "public S3 bucket" the path of least resistance for every new
static site works against that stance.

**Decision.** Feature 18 (Create Bucket) enables
`s3:PutPublicAccessBlock` (all four settings) by default on every new
bucket. Feature 19 (Configure Static Website Hosting) only sets the
bucket's website document config; it does not open public access.
Standing up an actual public-facing site is Feature 24 (Create
Distribution): CloudFront + a per-distribution Origin Access Control,
with the bucket policy scoped to that distribution's ARN
(`s3:PutBucketPolicy` restricted by `AWS:SourceArn`) so the bucket stays
private and only that CloudFront distribution can read it. A public-read
bucket policy remains available as an explicit opt-out inside Feature
19, gated by its own separate confirmation that plainly states the
bucket becomes world-readable directly — never the default, never
reachable by just accepting defaults through the flow.

**Rationale.**
- Matches this tool's existing posture toward exposure (dry-run/warn
  before anything that broadens what's publicly reachable).
- CloudFront in front of a private bucket is also just better practice
  independent of security — caching, HTTPS, and a real CDN domain name
  come for free, not just access control.
- Keeping the public-read path available (not removed) avoids blocking a
  legitimate simple-case use if someone genuinely wants it, while making
  sure it's never the accidental default.

**Rejected alternatives.**
- *Public-read bucket policy as an equal, unranked option* — considered
  and rejected in this session's design discussion; the concern was that
  presenting both paths as equivalent choices, with no recommended
  default, makes it too easy to pick the less safe one out of habit or
  unfamiliarity with OAC.

**Consequences.**
- Feature 24 (Create Distribution) needs write access to the bucket
  policy (`s3:PutBucketPolicy`) in addition to CloudFront permissions —
  see `DESIGN.md` Assumptions.
- Standing up a fully working static site now requires walking through
  two features (19 then 24) rather than one; `DESIGN.md` notes the
  handoff between them explicitly so the flow doesn't feel like two
  disconnected tasks.
- ACM certificate provisioning for a custom domain name on the
  distribution is out of scope (see `DESIGN.md`, "Deferred to a Later
  Version") — Feature 24 assumes the certificate already exists.

---

## 2026-07-02 — Redesign navigation as a domain picker; add Key Management, S3, and CloudFront domains

**Context.** With Compute (EC2/AMI) at 12 features and real-AWS
verification (Phase 16) underway, the user raised that `awsops`'s actual
job spans more than EC2/AMI: this team's AWS footprint is really "deploy
and operate Invenio RDM" (Compute, plus the SSH key pairs instances
launch with) and "publish static websites" (S3 + CloudFront) — and the
existing single flat main menu doesn't make that structure visible, nor
does the current feature set actually cover the S3/CloudFront side of
the job at all (S3 today is only ever a write-destination inside Backup
Archive & Trim, never a managed resource in its own right). Growing a
single menu to cover all of this was rejected outright as unusable long
before reaching a final feature count.

**Decision.**
- Replace the single flat main menu with a domain picker: **Compute
  (EC2 & AMI)**, **Key Management**, **S3 (Buckets & Static Websites)**,
  **CloudFront**, **Exit**. Each domain has its own resource listing and
  its own numbered menu underneath (same shape Compute's menu already
  has today), reached via the picker and returned to via a "Back to
  domain picker" entry in every domain menu.
- **EC2 and AMI stay one domain ("Compute"), not two.** They're already
  deeply interleaved — "Create Instance *from* AMI," "Create AMI *from*
  Instance," and both Manage Tags and Show/Export Cloud-Init operate on
  "an instance or an AMI" as a single pick — splitting them would force
  those cross-cutting workflows to pick an arbitrary home or be
  duplicated across two menus.
- **Key Management becomes a first-class domain**, not just a label:
  key pairs get their own List/Create/Import/Delete primitives
  (`DESIGN.md` Features 13-16), not only the inline "type `new`" launch
  shortcut that already existed (2026-07-01 decision above) — that
  shortcut now calls the same standalone Create Key Pair primitive.
- **S3 gets full static-website scope**, not just a backup destination:
  bucket listing/creation, static website hosting configuration, local
  directory sync, and object browsing (`DESIGN.md` Features 17-21) — see
  the paired 2026-07-02 "CloudFront + OAC by default" decision above for
  the specific access-pattern default.
- **CloudFront gets core lifecycle scope**: list, show detail, create (S3
  origin + OAC), and invalidate (`DESIGN.md` Features 22-25) — not just
  read-only listing, since creating and refreshing a distribution are
  routine parts of standing up and updating a static site, not rare
  one-time console tasks.
- This redesign runs **alongside**, not blocking, Phase 16's real-AWS
  verification of Compute — the domain picker is a navigation refactor
  around Compute's existing, already-tested workflows, not a rewrite of
  them; see `PLAN.md` for how the new phases are sequenced relative to
  Phase 16/17.

**Rationale.**
- A two-level menu keeps each screen's numbered choices in the
  single-digit-to-low-teens range the interactive picker pattern
  (2026-06-30 decision, "Use numbered list selection...") was designed
  for, instead of scaling that pattern past where it stays usable.
- Merging EC2/AMI avoids fragmenting workflows that are, in AWS's own
  model, already cross-cutting between the two resource types.
- Scoping Key Management/S3/CloudFront generously now (rather than
  shipping thin listing-only versions and expanding later) matches this
  project's stated goal — reduce manual, undocumented AWS console work
  for this team's actual two use cases — instead of just adding a
  smaller surface that still leaves most of that work in the console.

**Rejected alternatives.**
- *Five separate top-level domains (EC2, AMI, Key Management, S3,
  CloudFront)* — matches the user's original phrasing most literally,
  but splits Compute's interleaved workflows for no real navigational
  benefit, since EC2 and AMI together are still a small enough menu on
  their own.
- *Key Management as a label only, no new primitives* — rejected because
  it leaves key pairs exactly as under-managed as today (create-only,
  buried inside instance launch), which doesn't actually close the gap
  that motivated calling it out as its own domain.
- *S3 scoped to backup-only, static website deferred* — rejected because
  static website hosting is one of the two concrete, named use cases
  driving this whole redesign, not a hypothetical future one.
- *CloudFront read-only for v1* — rejected because creating a
  distribution and invalidating its cache after a content update are
  both routine, not rare, once a site is live; deferring creation would
  leave the tool unable to actually finish standing up a site it just
  helped populate via S3 sync.
- *Pause Phase 16 to do this redesign first* — rejected; the two are
  independent (navigation refactor vs. verifying already-implemented
  Compute workflows against real AWS), so serializing them would waste
  time for no coordination benefit.

**Consequences.**
- `internal/ui` gains a `domainmenu.go` shared loop that Compute's
  existing menu code is refactored to use, rather than owning its own
  bespoke top-level loop (see `DESIGN.md` Architecture).
- Three new `internal/inventory` listers (`keypairs.go`, `buckets.go`,
  `distributions.go`) and a new `internal/awsclient/cloudfront.go` client
  are needed; `internal/awsclient/s3.go` is broadened well beyond
  Feature 11's original HeadObject-only scope.
- New IAM permissions are required beyond what's listed in `DESIGN.md`
  Assumptions as of 2026-07-01 — see that section's 2026-07-02 additions
  for the full list per domain.
- `Environment=production`'s extra safety-gate warning (today gating
  Compute's Terminate/Remove AMI) is *not* extended to the new domains'
  destructive operations in this round — an open item, not an oversight
  (see `DESIGN.md` Feature 26 and "Deferred to a Later Version").
- `PLAN.md` needs new phases for Key Management, S3, and CloudFront,
  sequenced after Phase 15 but not blocking Phase 16/17's completion of
  Compute's real-AWS verification and Bash retirement.

---

## 2026-07-01 — Support creating a new key pair from within awsops

**Context.** The user asked "what is Key pair name" while testing the
Key pair name prompt, and once it was explained (the name of an
already-registered AWS key pair used to install its public half on the
new instance, not a local file path), asked to be able to create a new
one from inside awsops instead of leaving the tool: "I don't like
re-using keys... this seems like an option that is needed." AWS's
`ec2:CreateKeyPair` generates a new key pair and returns the private key
material exactly once — AWS never stores or re-displays it — so
awsops has to capture and save it immediately or it's gone.

**Decision.**
- At the existing Key pair name prompt, typing `new` (case-insensitive)
  instead of a name switches into a small sub-flow: prompt for a new
  key pair's name, call `ec2:CreateKeyPair` (`KeyType=ed25519`,
  `KeyFormat=pem`), save the returned private key to
  `~/.ssh/<name>.pem` with `0600` permissions (creating `~/.ssh` first
  if it doesn't exist), print the saved path, and use that name as the
  launch's key pair.
- A name collision (`InvalidKeyPair.Duplicate`) re-prompts for a
  different name rather than failing the whole launch; any other
  `CreateKeyPair` error (e.g. missing IAM permission) is returned as a
  real error, same as any other unrecoverable AWS failure elsewhere in
  the tool.
- Default key type is ED25519 (AWS's own current recommendation for
  new key pairs — smaller, faster than RSA) rather than RSA.
- `internal/awsclient`'s `-debug` logging decorator for `CreateKeyPair`
  does not use the shared generic `logAWSCall` helper — its response
  carries the private key material, which must never be written to the
  debug log even though everything else the tool does is logged in
  full. The wrapper logs everything except `KeyMaterial`, which it
  replaces with a fixed redaction marker.

**Rationale.** Inline ("type `new`") beats a separate main-menu action
because the natural point to decide "I want a fresh key for this one"
is exactly when you're being asked for a key pair name — a separate
menu item would mean leaving the launch flow, remembering the name you
picked, then re-entering the flow to use it. Saving straight to
`~/.ssh/` with correct permissions means the key is immediately usable
(`ssh -i ~/.ssh/<name>.pem ...`) without a manual `chmod` step.

**Trade-off.** awsops now writes files outside its own working
directory (`~/.ssh`) for the first time. Accepted: this is the
standard, expected location for SSH private keys, and the alternative
(printing the key material to the terminal for the operator to save
themselves) risks losing an unrecoverable secret in scrollback.

---

## 2026-07-01 — Add -debug: a JSONL log of every AWS SDK call

**Context.** The user asked for a `-debug` option to make awsops'
behavior easier to diagnose, pointing at a similar feature already
built for `~/Laboratory/harvey` (a JSONL debug log of specific
hand-instrumented events — LLM requests/responses, tool calls, etc.,
via a `*DebugLog` type whose methods are all nil-receiver-safe). Unlike
Harvey, where "interesting events" are a hand-picked subset of a large
surface area, awsops' entire interesting surface *is* its AWS SDK
calls — every EC2/SSM/S3/STS method it calls is already declared in
one of four narrow interfaces (`internal/awsclient`'s `EC2API`/
`SSMAPI`/`S3API`/`STSAPI`), the same interfaces the test fakes already
implement.

**Decision.**
- `internal/debuglog`: a new package with the same nil-safe `*DebugLog`
  shape as Harvey's — `Log(event string, fields map[string]any)`,
  `Path()`, `Close()`, all safe on a nil receiver — so `-debug=false`
  needs no `if debug` conditionals anywhere else in the codebase.
- Rather than hand-instrumenting ~15 call sites (Harvey's approach),
  wrap each of the four client interfaces with a logging decorator
  (`internal/awsclient`'s `WrapEC2`/`WrapSSM`/`WrapS3`/`WrapSTS`) that
  implements every method mechanically via one shared generic helper
  (`logAWSCall`): log method name, region, request params, duration,
  and response-or-error, then delegate. This gets full coverage of
  every AWS action awsops takes, for about the same code as
  hand-picking a subset would have cost, because the interfaces were
  already narrow and enumerable.
- `Wrap*` returns the original client unchanged when `dl` is nil, so
  there's no wrapper-object overhead when `-debug` isn't set.
- Sink: a timestamped file in the current directory
  (`awsops-debug-<timestamp>.jsonl`, `debuglog.DefaultPath()`), printed
  to stderr once at startup — not `agents/logs/` like Harvey, since
  awsops has no `agents/` directory convention of its own.

**Rationale.** Decorating the interfaces instead of instrumenting call
sites means new AWS calls added to `EC2API`/`SSMAPI`/`S3API`/`STSAPI`
in the future are logged automatically (the interface's method set is
enumerable and the compiler enforces the decorator implements all of
it) — no risk of a future workflow silently bypassing the debug log
because nobody remembered to add a manual `dl.Log(...)` call at the new
site.

**Trade-off.** Full-fidelity request/response logging (not just event
names) means `DescribeInstances`/`DescribeImages` calls that return
large collections write correspondingly large JSON records. Accepted:
the log is opt-in, local, and meant for active debugging sessions, not
continuous production telemetry — verbosity favors "can actually see
what happened" over log-file size.

---

## 2026-07-01 — Key pair stays free text; Name tag moves earlier

**Context.** A prior fix (this same day) added pick lists for key pair
name, security group IDs, and subnet ID, fetched from the AMI's region,
after real-AWS testing surfaced a typo-prone free-text UX (typing a
security group *name* instead of an ID caused a real AWS
`InvalidParameterCombination` error). Further real-AWS testing then
showed the key pair pick list didn't help: unlike opaque `sg-xxxx`/
`subnet-xxxx` IDs, key pair names are already human-readable, and a flat,
unsorted list of every key pair across the account (16+ entries in one
real test run, most irrelevant to the AMI being launched) added noise
without solving a real problem. Separately, the user noted the Name tag
prompt landed too late in the flow — after all of instance type/key
pair/security groups/subnet/IAM profile/user data — when naming the
instance is a natural first step once the AMI is picked.

**Decision.**
- Revert the key pair prompt to free text (`ui.Prompt` with
  `requireNonEmpty`), removing `listKeyPairNames` and the pick-list
  wrapper. Security group IDs and subnet ID keep their pick lists —
  those IDs are the genuinely opaque case the original gap was about.
- Move the Name tag prompt to immediately after the AMI pick (and, for
  the cloud-init-first workflow, immediately after its own AMI pick),
  before Instance type and everything after it.

**Rationale.** Pick lists help when the identifier is opaque and the
account/region has more of them than a person can remember (security
groups, subnets). They don't help when the identifier is already a
name the user chose and knows — showing every key pair in the account
regardless of relevance is worse than just typing the name. Naming the
instance right after picking its AMI matches how a person actually
thinks through the workflow (what is this, then how is it configured).

**Trade-off.** A key pair pick list would still catch a typo'd key pair
name before `RunInstances` runs; free text defers that to AWS's own
error. Accepted, since `ec2:RunInstances` rejects an unknown key pair
name immediately and unambiguously, unlike the security-group-name-vs-ID
confusion this was originally meant to prevent.

---

## 2026-07-01 — Use github.com/rsdoiel/termlib for the Terminal UI

**Context.** PLAN.md's Phase 3 (Terminal UI) originally called for a
stdlib-only implementation (`text/tabwriter` for tables, `bufio.Scanner`
for the numbered pick list and free-text prompts) — no interactive-input
library existed under the pre-approved `github.com/rsdoiel` or
`github.com/caltechlibrary` namespaces (CLAUDE.md) at the time DESIGN.md
was drafted. The user has since pointed at `github.com/rsdoiel/termlib`
("a light weight terminal interface library... ncurses light"), which
already exists and is under the `github.com/rsdoiel` namespace, so no
separate approval discussion is needed per CLAUDE.md's pre-approved
dependency list.

**Decision.** Build `internal/ui` on `termlib` instead of a hand-rolled
stdlib-only implementation:
- `DisplayInstances`/`DisplayImages` use `termlib.Terminal` (buffered
  output, flushed via `Refresh`) plus `termlib.PadRight`/`termlib.Truncate`
  for column formatting, replacing `ec2_ami_manager.bash`'s
  `printf "%-20s ..."` pattern
- `PickList[T]` and `Prompt` are built on `termlib.LineEditor.Prompt`
  (readline-style editing, history, `Ctrl+C`/`Ctrl+D` handling) rather
  than a plain `bufio.Scanner` loop
- Tests drive `LineEditor` through an `os.Pipe()` (not a TTY), which
  `LineEditor.Prompt` detects and falls back to plain line reading for —
  documented and intended by `termlib` itself for exactly this use case,
  so no fake/mock input abstraction was needed

**Rationale.**
- `termlib` is pre-approved (CLAUDE.md: `github.com/rsdoiel` namespace)
  and already provides exactly what Phase 3 needs: buffered flicker-free
  terminal output, readline-style prompts with history and `Ctrl+C`/
  `Ctrl+D` handling, and column-formatting helpers (`PadRight`,
  `Truncate`) that map directly onto the Bash version's `printf`/
  `${var:0:N}` conventions
- Still numbered-menu based, not raw-keystroke arrow navigation —
  consistent with the existing "Numbered menu for pick lists" decision
  (2026-06-30); `termlib`'s raw-mode keystroke reading (`ReadKey`/
  `KeyReader`) is not used here
- Avoids reimplementing readline-style editing/history/multi-line-paste
  handling that a hand-rolled `bufio.Scanner` loop would not have had

**Rejected alternatives.**
- *Stdlib-only (`text/tabwriter` + `bufio.Scanner`)* — the original
  PLAN.md approach; works, but reinvents editing/history niceties
  `termlib` already provides, for no benefit now that an approved library
  covers it
- *A third-party TUI/prompt library outside the `github.com/rsdoiel` /
  `github.com/caltechlibrary` namespaces* — would need discussion per
  CLAUDE.md; moot once `termlib` was pointed out

**Consequences.**
- `go.mod` gains `github.com/rsdoiel/termlib` (direct) and its transitive
  `golang.org/x/term`/`golang.org/x/sys` dependencies
- `DESIGN.md`'s Dependencies section and its "no dependency beyond the Go
  standard library and the AWS SDK" claim are updated to include
  `termlib`
- If a gap surfaces in `termlib` while building later phases (e.g. a
  table-drawing helper `internal/ui` ends up hand-rolling), flag it back
  to the user for `~/Laboratory/termlib/TODO.md` rather than working
  around it silently in `awstools`

---

## 2026-07-01 — Add Create EC2 Instance from Cloud-Init YAML as a v1 primitive

**Context.** Feature 2 (Create Instance from AMI) already got a cloud-init
file-input and completion-check enhancement (see "Enhance Create Instance
from AMI" below), but the user pointed out that burying it as the 6th of
7 parameters inside an AMI-first workflow doesn't serve the actual use
case: someone who starts from "I have a cloud-init recipe, give me a
machine" has a different mental model than someone who starts from "give
me another copy of this AMI." That deserves to be its own visible
primitive, not an option nested inside Feature 2's parameter list.

**Decision.** Add "Create EC2 Instance from Cloud-Init YAML" as its own
v1 primitive (Feature 8): the cloud-init file is the *first* thing
collected, then a base AMI is picked, then the same remaining launch
parameters Feature 2 already collects. It shares Feature 2's underlying
execution path entirely (the same `LaunchInstanceParams` struct, the same
launch/poll/cloud-init-completion-check logic) — the only difference is
the order and framing of the interactive prompts. This is distinct from
the deferred "Bake AMI from cloud-init" idea: that one snapshots the
result into a new AMI and terminates the instance; this one leaves a real,
running, usable instance.

**Rationale.**
- Matches the user's explicit ask: this needed to be visible in the
  primitive list, not folded into another feature's parameter collection
- Sharing execution logic with Feature 2 avoids duplicating the
  launch/poll/cloud-init-check code — only the front-end prompt sequence
  differs, consistent with the params-struct/confirm-gate seam already
  required of every workflow (see "Structure workflows for future
  record/replay")
- Placed as Feature 8 (after Backup Archive & Trim, before Stop/Terminate
  Instance and the cross-cutting Project/Environment Tagging convention —
  see the companion decision below) rather than immediately after
  Feature 2, to avoid a costly renumbering cascade through Features 3-7
  and their many existing cross-references, for a placement decision that
  doesn't carry strong semantic weight either way

**Rejected alternatives.**
- *A sub-mode within Feature 2 ("how do you want to start: AMI or
  cloud-init?")* — was the initial implementation; rejected because the
  user wants it directly visible as its own primitive, not a branch
  hidden inside another feature's flow
- *Insert immediately after Feature 2, renumbering Features 3-7* —
  arguably better thematic grouping, but not worth the renumbering risk
  across this document's many existing Feature N cross-references for a
  placement question without a strong correctness argument either way

**Consequences.**
- `DESIGN.md` gets a new Feature 8
- `PLAN.md` gets a new Phase 10 ("Create Instance from Cloud-Init YAML"),
  inserted before Main Menu and Integration
- No new IAM permissions — reuses exactly what Feature 2/Phase 4 already
  needs (`ec2:RunInstances`, SSM for the completion check)

---

## 2026-07-01 — Add Start/Stop/Terminate EC2 Instance as v1 primitives

**Context.** Immediately after adding "Create Instance from Cloud-Init
YAML," the user pointed out more gaps: there's no way to stop a running
instance or to terminate/remove one, and then — while this very decision
was being written up — no way to start a stopped one back up either. The
v1 list only covered AMI lifecycle and instance *creation*, not the rest
of an instance's power-state lifecycle.

**Decision.** Add three more v1 primitives:
- **Start EC2 Instance** (Feature 9): pick a stopped instance, simple
  yes/no confirm (safe and reversible, the symmetric counterpart to Stop),
  `ec2:StartInstances`, poll until `running`, display connection info
  (public IP may have changed unless an Elastic IP is in use)
- **Stop EC2 Instance** (Feature 10): pick a running instance, simple
  yes/no confirm (stopping is reversible — data on EBS volumes persists,
  the instance can be started again), `ec2:StopInstances`, poll until
  `stopped` (bounded timeout)
- **Terminate EC2 Instance** (Feature 11): pick an instance, dry-run
  showing what would be destroyed — **including whether any attached EBS
  volume has `DeleteOnTermination=true`**, since that volume's data
  (including any not-yet-archived backups — see Backup Archive & Trim) is
  destroyed along with the instance — an `Environment=production` warning
  if tagged, type-to-confirm, then `ec2:TerminateInstances`. Same safety
  tier as Remove AMI (Feature 4)

**Rationale.**
- Stopping/starting and terminating are fundamentally different risk
  levels (reversible vs. permanent), so they get different confirmation
  tiers — matching this project's existing principle of scaling friction
  to actual risk rather than applying one blanket confirmation style
  everywhere
- Surfacing `DeleteOnTermination` in the dry-run closes a real gap this
  project already cares about: an instance can be terminated with its
  root volume set to delete-on-termination, destroying exactly the kind
  of not-yet-archived backup data Backup Archive & Trim exists to protect
- `ec2:TerminateInstances` was already a planned permission (for Show/
  Export Cloud-Init's AMI-path cleanup); only `ec2:StartInstances`/
  `ec2:StopInstances` are new

**Rejected alternatives.**
- *One combined "manage instance power state" primitive covering start/
  stop/terminate* — considered, but start/stop and terminate have
  different confirmation tiers and different risk profiles; combining
  them risks the lighter-weight start/stop confirmation habituating users
  to clicking through what should be a heavier gate for terminate

**Consequences.**
- `DESIGN.md` gets three new Features (9, 10, 11); Project/Environment
  Tagging moves from Feature 8 to Feature 12 (four new features inserted
  ahead of it: Feature 8 Create-from-Cloud-Init-YAML, Feature 9 Start,
  Feature 10 Stop, Feature 11 Terminate)
- `PLAN.md` gets three new Phases after the Create-from-Cloud-Init-YAML
  phase, before Main Menu and Integration; later phases renumbered
  accordingly
- `DESIGN.md`'s IAM permission list gains `ec2:StartInstances` and
  `ec2:StopInstances`

---

## 2026-07-01 — Name the CLI binary `awsops`

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

## 2026-07-01 — Enhance Create Instance from AMI: cloud-init file input + completion check

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

## 2026-07-01 — Broaden Rename Instance into a general Manage Tags primitive

**Context.** Immediately after adding "Rename Instance" (below), the user
pointed out the obvious generalization: renaming is just "update the
`Name` tag," and if the tool can create tags, it should be able to
manage tags generally — add, update, and remove, on any resource, not
just set `Name` on an instance.

**Decision.** Replace Feature 5 ("Rename Instance") with a general
"Manage Tags" primitive: pick a resource (instance or AMI), see its
current tags, then add a new tag, update an existing one, or remove one.
Renaming is simply the common case of updating `Name` through this same
flow — no separate operation. Confirmation stays at the same lightweight
tier Rename Instance had (simple yes/no — tag edits are cheap and
reversible, not the dry-run/type-to-confirm tier reserved for actually
destructive operations), routed through the same reusable confirmation
gate as every other workflow.

**Rationale.**
- Avoids two overlapping menu items backed by the same underlying API
  (`ec2:CreateTags`) doing the same thing at different scopes
- AMIs get tags too (Project/Environment are set at creation per Feature
  3) but had no way to edit them after the fact — this closes that gap
  symmetrically for both resource types
- One general primitive is simpler to reason about, test, and eventually
  expose to Recorded Scripts than two narrow ones

**Rejected alternatives.**
- *Keep both Rename Instance and a separate Manage Tags primitive* — lets
  renaming stay a one-step action, but two menu items for the same
  underlying operation is redundant and was explicitly rejected in favor
  of consolidation
- *Tag management as an instance-only feature* — considered, but AMIs
  need the same capability (they carry Project/Environment tags too), so
  scoping it to instances only would just recreate the same gap for AMIs

**Consequences.**
- `DESIGN.md` Feature 5 is retitled "Manage Tags" and rewritten; no
  renumbering needed elsewhere since it's a same-slot replacement
- `DESIGN.md`'s IAM permission list gains `ec2:DeleteTags` (removal) —
  `CreateTags`/`DescribeTags` were already planned
- `PLAN.md` Phase 9 is retitled "Manage Tags" and its work items rewritten
  to cover add/update/remove on both instances and AMIs; still Phase 9,
  no renumbering elsewhere

---

## 2026-07-01 — Add Rename Instance as a v1 primitive; AMI Name is immutable

**Context.** The user noticed renaming was missing from the v1 primitive
list and asked whether the AWS SDK supports it. The two resources behave
completely differently:
- An EC2 instance's "Name" is not a real API attribute at all — it's just
  the `Name` tag by convention (the same tag this project's Project/
  Environment tagging convention already reads/writes). Changing it is a
  plain `ec2:CreateTags` call, already a planned permission
- An AMI's `Name` is a real attribute, but it is set once at `CreateImage`
  time and **cannot be changed afterward via the AWS API** — this is an
  AWS EC2 limitation, not a gap in the SDK or this tool. `ModifyImageAttribute`
  allows changing `Description` and launch permissions, but not `Name`.
  The only way to get an AMI with a different name is `CopyImage`
  (produces a brand-new AMI with a new ID and duplicated snapshots) plus
  deregistering the original — a materially heavier operation than a
  rename, closer in cost/risk to Feature 3 (create-AMI) than a quick edit

**Decision.** Add "Rename Instance" as a v1 primitive (pick an instance,
prompt for a new `Name` tag value, confirm, `ec2.CreateTags`). Do not add
an "AMI rename" primitive of any kind. Feature 3 (Create AMI from EC2
Instance) keeps its existing default-name-suggestion behavior unchanged,
but gains an explicit note that the name is permanent once created, so
the user isn't surprised later. "Edit AMI Description" (the one AMI
attribute that *is* mutable) is recorded as a deferred, not-yet-requested
idea rather than built now.

**Rationale.**
- Renaming an instance is cheap, reversible, and was a real gap — no
  reason to defer it
- Silently supporting "rename" for AMIs by only updating a tag while the
  real `Name` attribute stays stale would be actively misleading, since
  AWS's own console/CLI would keep showing the original name
- Building a "rename via copy + deregister" primitive for AMIs was
  considered and rejected: it's a different operation with different
  risk (storage duplication, a new AMI ID, anything referencing the old
  ID breaks) than what a user asking to "rename" would expect

**Rejected alternatives.**
- *AMI rename via CopyImage + DeregisterImage* — technically the only way
  to get a differently-named AMI, but it's a heavyweight operation
  disguised as a rename; not built for v1
- *Only update the AMI's tags, leave Name attribute alone* — would create
  a confusing mismatch between this tool's tag-based "name" and the AMI's
  actual `Name` shown everywhere else in AWS

**Consequences.**
- `DESIGN.md` gets a new Feature 5 ("Rename Instance"), renumbering
  Show/Export Cloud-Init, Backup Archive & Trim, and Project/Environment
  Tagging by one
- `PLAN.md` gets a new Phase 9 ("Rename Instance"); Phases 9-12 in the
  prior draft are renumbered to 10-13 accordingly
- No new IAM permission needed — `ec2:CreateTags` was already planned for
  the tagging convention

---

## 2026-07-01 — Structure workflows for future record/replay ("Recorded Scripts")

**Context.** Discussing "bake an AMI from cloud-init" led to a bigger idea:
capture the sequence of actions taken in an interactive session as an
editable, replayable script — analogous to how a "skill" packages a
procedure for a language model, but for this deterministic tool. The user
wants this to use YAML (prior success with YAML-driven configuration in
other projects, e.g. `dataset`'s web service config) and templated values
(not just literal captured values), with safety gates enforced on replay,
not bypassed. This is a substantial feature — a new execution mode
(record / replay) that cuts across the whole menu/dispatch loop, not a
single primitive — so it is not built in v1 (see "Recorded Scripts" under
"Deferred to a Later Version" in `DESIGN.md`/`PLAN.md`). It also
potentially subsumes the earlier-deferred "Clone instance for testing",
"Upgrade with rollback point", and "Bake AMI from cloud-init" composite
workflows: if a user can record a sequence once and replay it with
different values, those don't need to be bespoke Go features.

**Decision.** v1 does not build the recorder, the YAML schema, or the
replay engine. It does structure every confirmation-gated workflow
(Phases 4, 5, 6's AMI path, 7, 8) around a specific seam so that adding
record/replay later does not require reopening already-finished code:
1. Each workflow separates **building a resolved parameters struct**
   (interactive prompts fill it in v1) from **executing it against AWS**.
   The execution code takes a plain, typed struct (e.g.
   `CreateAMIParams{InstanceID, Name, Description, NoReboot, Tags}`) and
   never knows whether prompts or a future YAML file produced it
2. The **confirmation/dry-run gate is its own reusable step**, not inlined
   into each workflow's prompt loop, so a future replay engine can route
   through the identical gate rather than a second, parallel
   implementation of "is this safe to do"
3. When templating is eventually built, it applies via Go's standard
   library `text/template` to the YAML text before parsing — no new
   dependency, consistent with this project's stdlib-first preference

**Rationale.**
- The params-struct/execute split and the reusable confirmation gate are
  good structure on their own merits (testability, single source of truth
  for "is this safe"), so requiring them now costs nothing extra — it's a
  constraint on code already being written, not additional code
- Building the actual recorder/replay engine before v1's primitives have
  proven themselves against real AWS would be scope creep on an already
  large rewrite (see "V1 scope" below)
- Safety gates must not be bypassable by replay without deliberate,
  explicit opt-in — reusing the exact same gate function is the simplest
  way to guarantee that, rather than trusting a second implementation to
  stay in sync

**Rejected alternatives.**
- *Build record/replay now, as part of v1* — most directly useful, but
  conflicts with "V1 scope: ship the four primitives first" and adds a new
  execution mode on top of an already-large Bash→Go rewrite
- *Defer without any structural constraint on v1's workflows* — cheaper
  short-term, but risks having to rewrite Phase 4-8's internals later to
  retrofit the params-struct/confirm-gate seam once record/replay is
  actually built
- *Literal-only captured values (no templating)* — simpler, but the user
  specifically wants templating for repurposing a saved sequence across
  different targets (different instance, different environment), which a
  literal-only capture can't do without hand-editing every value each time

**Consequences.**
- `PLAN.md` Phases 4, 5, 6, 7, 8 each get a work item noting this
  structural requirement
- The "Deferred to a Later Version" entries for "Clone instance for
  testing", "Upgrade with rollback point", and "Bake AMI from cloud-init"
  are annotated as likely to become example Recorded Scripts rather than
  bespoke Go workflows, once the mechanism exists
- No new third-party dependency is introduced by this decision — YAML
  parsing (`gopkg.in/yaml.v3` or similar) would be needed when the feature
  is actually built, and would need the same approval process as
  `aws-sdk-go-v2` did, at that time

---

## 2026-07-01 — Add Backup Archive & Trim as a v1 primitive

**Context.** Today, cleaning up stale Postgres backups on an RDM instance
is a fully manual chore: log in, look at `/opt/rdm_sql_backups`, decide
what's old enough to remove, delete it by hand. A read-only SSM check of
`newauthors` found the real scale of the problem: 87GB in
`/opt/rdm_sql_backups`, one `<project>-db-<n>-<project>-<date>.sql.gz`
file per day at ~980MB, with no rotation at all (root volume: 484GB total,
157GB used, 328GB free — the backups are over half of what's used). This
is the concrete cause of the "over-provisioned disk makes cloning slow"
problem that motivated this whole design conversation. No S3 destination
exists yet for these backups — that's a separate infrastructure task the
user still needs to do, not something this project creates.

This conversation also surfaced a broader framing for the tool: it should
help with ongoing *administration* of these instances (not just
create/remove EC2/AMI lifecycle events), and with improving development,
test, and deployment workflows more generally.

**Decision.** Add "Backup Archive & Trim" as a v1 primitive: given an
instance, a backup directory, and an age threshold (both explicit prompts,
no baked-in default — same reasoning as the `Environment` tag having no
default), it uploads files older than the threshold to S3, independently
verifies each upload, deletes only the verified files, then runs `fstrim`.
The sequence is deliberately split into two separate remote steps with the
tool itself as the arbiter in between, rather than one script that
uploads-then-immediately-deletes based on its own success report:

1. **Dry-run list** (SSM, read-only): candidate files matching the age
   threshold, with size/age, shown before anything happens
2. **Type-to-confirm** (matches the AMI-removal safety tier, since step 5
   is irreversible)
3. **Upload phase** (SSM): the instance uploads each candidate file to S3
   (`aws s3 cp`, run from the instance — see rejected alternatives) and
   reports back a small per-file JSON summary (S3 key, size). Nothing is
   deleted yet
4. **Independent verification**: the tool itself, using its own AWS
   credentials (not the instance's self-report), calls `s3:HeadObject` on
   every uploaded key and confirms it exists with the expected size
5. **Delete phase** (a *second*, separate SSM command): the instance
   deletes exactly the tool-verified file list — it does not re-derive its
   own "what's stale" list, avoiding a time-of-check/time-of-use gap
6. **fstrim**, then a report of bytes freed and any files that failed
   verification (left untouched, flagged, not deleted)

**Rationale.**
- Matches this project's existing safety pattern for destructive
  operations (dry-run, then explicit confirm — see "Multi-layer
  confirmation for AMI removal")
- SSM Run Command is not a bulk file-transfer channel (confirmed when
  designing Show/Export Cloud-Init) — ~980MB backup files are far too
  large to round-trip through SSM output, so the upload must happen
  *from* the instance itself via its own AWS CLI/credentials
- Splitting upload and delete into two round-trips with independent
  verification in between means the tool — not the remote script — decides
  what's safe to delete, based on a second, independent read from S3.
  Trusting a single script's self-report to authorize an irreversible
  delete was considered and explicitly rejected (see below)

**Rejected alternatives.**
- *Single script: upload then immediately delete on self-reported success*
  — simpler, one round-trip, but means a script bug or a transient S3
  error that still reports "success" could delete backups with nothing
  actually saved. Rejected in favor of independent verification
- *Tool downloads/re-uploads the files itself instead of the instance
  doing it* — would let the tool verify with certainty, but requires
  streaming multi-GB files through the operator's machine or through SSM
  (impractical for the same reason raw file transfer was rejected for
  Show/Export Cloud-Init's AMI path)
- *Fold into the existing fstrim step* — considered, but the user chose a
  standalone primitive (see "Should v1 add composite workflows" discussion
  in the Show/Export Cloud-Init decision above); backup cleanup is useful
  on its own, independent of AMI creation

**Consequences.**
- Each target instance's IAM instance profile needs `s3:PutObject` (and
  likely `s3:ListBucket` scoped to its own prefix) on the destination
  bucket — this is a cloud-init/AMI change in `caltechlibrary/cloud-init-
  examples`, not something this Go tool can retrofit from outside. This is
  in scope for this project to specify (per the earlier "prereq scope"
  decision) even though the actual bucket creation and IAM/Terraform work
  happens separately
- The tool's own IAM permissions (the operator's identity, distinct from
  the instance's own instance profile) need `s3:HeadObject` (or
  `s3:GetObject`) on the bucket, for independent verification
- The S3 bucket itself does not exist yet — real-AWS verification of this
  primitive is blocked on that being created first (tracked in `TODO.md`,
  not by this project)
- Testing plan (per discussion with the user): unit tests first, with
  fakes for `EC2API`/`SSMAPI`/a new `S3API` (covering `HeadObject`); then,
  once those pass, a live test against a *throwaway instance launched from
  an existing AMI that already has these backups baked in* — never
  directly against the production instance. This reuses Phase 4
  (Create EC2 Instance from AMI) as a testing tool in its own right, and
  the throwaway instance must be terminated after the test, same cleanup
  discipline as Show/Export Cloud-Init's AMI path
- `DESIGN.md`'s Overview is broadened to describe ongoing instance
  administration and dev/test/deployment workflow support, not just
  EC2/AMI lifecycle management
- `PLAN.md` gets a new Phase 7 ("Backup Archive & Trim"); Phases 7-11 in
  the prior draft are renumbered to 8-12 accordingly

---

## 2026-07-01 — Add Show/Export Cloud-Init as a v1 primitive

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

## 2026-07-01 — Introduce a light Project/Environment tagging convention

**Context.** The stated goal for this tool is to speed up upgrading RDM
deployments and creating accurate test environments across production and
development instances. A read-only check of the live account
(`aws ec2 describe-instances`) found tagging is inconsistent today: a
`project` tag exists on some instances (`caltechauthors`, `caltechdata`,
`caltechthesis`) but not others (`new-plots`, `thesis`,
`authors-test-recovery`), and there is no dedicated environment tag —
production vs. test is encoded only in the instance *name string* (e.g.
`caltechdata-test` vs. `oldcaltechdata`). `newauthors` is additionally
managed via an EC2 Launch Template, not ad hoc parameters.

**Decision.** The tool suggests/requires `Project` and `Environment`
(`production` | `development` | `test`) tags when creating new instances
and AMIs, uses them to group the resource listing, and adds extra
confirmation friction for destructive actions (AMI removal) on anything
tagged `production`. Existing untagged resources display as "unknown"
until tagged — the tool does not retroactively rewrite tags on resources it
didn't create.

**Rationale.**
- Directly serves the stated goal: distinguishing production from
  development/test at a glance, and grouping by application (`Project`),
  is exactly what "manage our RDM production and development instances"
  requires
- Inferring environment from free-text instance names (today's de facto
  approach) is fragile and inconsistent, as the live-account check showed
- Extra confirmation on production-tagged resources targets friction at
  the resource class where a mistake is most costly, rather than applying
  uniform friction everywhere

**Rejected alternatives.**
- *Work with what exists, no enforcement* — keeps inferring environment
  from the name string with no structured signal; considered, but doesn't
  move the account toward consistency and leaves "is this production?"
  a matter of reading a name carefully rather than checking a tag
- *Retroactively tag all existing resources* — out of scope for this tool;
  a one-time cleanup task, not an ongoing tool responsibility

**Consequences.**
- `PLAN.md` Phase 2 (listing) groups/filters by `Project`/`Environment`;
  Phases 4/5 (creation) prompt for and default these tags; Phase 6
  (removal) adds a heightened warning for `Environment=production`
- `DESIGN.md`'s IAM permission list must include `ec2:CreateTags` and
  `ec2:DescribeTags` (already present in `software_requirements.md`'s
  policy but missing from `DESIGN.md`'s own Assumptions section — fixed in
  this update)
- No migration or backfill of existing untagged resources is planned

---

## 2026-07-01 — V1 scope: ship the four primitives first, defer composite workflows

**Context.** The stated goal for this tool — speed up upgrading
deployments, create accurate test environments with confidence — is
naturally served by *composite* operations (e.g. "clone this instance for
testing, inheriting its network/instance-type config" or "upgrade with a
tracked rollback point"), not by the four raw primitives
(create-instance-from-AMI / create-AMI-from-instance / remove-AMI /
refresh) alone. Those primitives require the user to manually chain
operations and re-enter configuration (instance type, security groups,
subnet, IAM profile) from scratch each time, which is exactly the
slowness/accuracy-drift risk the stated goal wants to eliminate.

**Decision.** V1 of the Go tool ships a faithful port of the four existing
primitives (matching `ec2_ami_manager.bash`'s feature set), verified
against real AWS, before any composite workflow is added. "Clone instance
for testing" and "upgrade with a tracked rollback point" are recorded here
as intended fast-follow work, not dropped.

**Rationale.**
- Keeps the rewrite scoped and verifiable: Bash→Go parity is itself
  nontrivial (see "Retarget implementation from Bash to Go" below) and
  mixing in new composite behavior would make it harder to tell whether a
  bug is a porting regression or new-feature bug
- The primitives are the building blocks the composite workflows will call
  — building them first, correctly, is not wasted work
- Real-AWS verification (`TEST_PLAN_REAL_AWS.txt`) is easier to reason
  about against a known, already-specified feature set

**Rejected alternatives.**
- *Build composite workflows into v1 directly* — more directly serves the
  stated goal sooner, but risks conflating porting bugs with new-feature
  bugs during the highest-risk phase (initial Go implementation)

**Consequences.**
- `PLAN.md` gets a "Deferred / Future Work" section describing "Clone
  instance for testing" and "Upgrade with rollback point" so they aren't
  lost, to be scheduled once Phase 9 (real-AWS verification) passes
- The Project/Environment tagging convention (see companion decision
  above) is still built in v1, since the composite workflows will depend
  on it and it's needed for the listing/removal-friction behavior anyway

---

## 2026-07-01 — Retarget implementation from Bash to Go

**Context.** `ec2_ami_manager.bash` reached feature parity with the design
(Phases 0–7) but real-world use against production AWS resources surfaced a
string of bugs rooted in Bash's lack of static typing and its reliance on
runtime string construction for control flow:
- an `eval`-based array-copy helper (`show_pick_list`) had unbalanced
  quoting that crashed the interactive picker outright
- the AMI-name validation regex broke under BSD `grep` combined with a
  UTF-8 locale (`invalid character range`)
- the AMI-creation tag-specification string builder produced syntactically
  invalid AWS CLI shorthand that silently failed `create-image`, with no
  AMI created in any region and only a scrollback error message as evidence

Each bug class (shell quoting/escaping, locale-dependent tool behavior,
hand-built CLI argument strings) is structural to shelling out from Bash
rather than incidental, and none would be caught by static analysis before
runtime.

**Decision.** Set aside the Bash implementation and retarget the
interactive EC2/AMI manager to Go, in place in this repository, targeting
full feature parity with the existing design (all four operations: create
instance from AMI, create AMI from instance, remove AMI, main menu) before
the Bash version is retired.

**Rationale.**
- Go is this workspace's stated primary backend language (CLAUDE.md)
- A typed AWS SDK (see companion decision below) replaces "aws CLI + jq +
  eval" with compiled, typed API calls — the entire class of quoting/
  escaping bugs hit in this session becomes structurally impossible
- Go's `go test` and table-driven tests replace BATS plus a hand-rolled
  mock-`aws`-binary harness, and can mock the AWS SDK client via interfaces
  without shelling out at all
- The feature set, UX flow, and hard-won domain knowledge (multi-region
  aggregation, owned-AMIs-only scope, three-layer removal confirmation,
  fstrim/SSM pre-snapshot step, Invenio RDM crash-consistency guidance,
  volume-size time estimates) all carry forward unchanged — this is a
  reimplementation, not a redesign

**Rejected alternatives.**
- *Patch the specific bugs and stay in Bash* — would fix these three bugs
  but leaves the same eval/quoting/locale hazard class open for the next
  feature added
- *Python + boto3* — also a strong, typed-enough option with less ceremony
  than Go, but Go is this workspace's designated primary backend language
  (CLAUDE.md); Python/Perl are described there as secondary/legacy
- *Deno + TypeScript* — this workspace reserves Deno/TypeScript for
  middleware/frontend, not backend CLI tools (CLAUDE.md's layered
  architecture pattern)

**Consequences.**
- `ec2_ami_manager.bash`, `ami_copy.bash`, and their supporting docs remain
  in the repo unchanged as a working reference/spec until the Go version
  reaches parity and is verified against real AWS (the same retire-after-
  verify pattern already used for `ami_copy.bash`, see below)
- `DESIGN.md` and `PLAN.md` are retargeted for Go in this same update; the
  new `PLAN.md` phases restart from Phase 0 (Go module setup)
- BATS test debt for Phase 6/7 (`test_remove_ami.bats`, `test_menu.bats`) is
  superseded — those workflows get Go tests instead, not backfilled BATS
  tests
- `TEST_PLAN_REAL_AWS.txt`'s manual verification step now targets the Go
  binary, not `ec2_ami_manager.bash`

---

## 2026-07-01 — Use official AWS SDK for Go v2

**Context.** Retargeting to Go (see companion decision above) could either
keep the Bash version's shell-out pattern (`exec aws ...` from Go) or adopt
AWS's official Go SDK.

**Decision.** Use `github.com/aws/aws-sdk-go-v2` (with its `ec2` and `ssm`
service packages) for all AWS API calls. This is a third-party dependency
outside the pre-approved `github.com/rsdoiel` / `github.com/caltechlibrary`
namespaces (CLAUDE.md); explicitly approved for this project.

**Rationale.**
- Typed request/response structs eliminate the JSON-through-subshell-through-
  jq round trips that made the Bash version fragile
- No runtime dependency on the `aws` CLI binary or `jq` being installed —
  only the Go binary and AWS credentials
- Official, actively maintained by AWS
- Enables interface-based mocking of AWS calls in tests without a
  hand-rolled mock CLI binary (the pattern `tests/lib/test_helper.bash` used)

**Rejected alternatives.**
- *Shell out to the `aws` CLI from Go (`os/exec`)* — keeps the CLI-argument-
  quoting risk that caused this session's tag-specification bug, just moved
  into Go's `exec.Command` argument building; still requires the `aws` CLI
  as a runtime dependency
- *Hand-rolled AWS API client (SigV4 signing, raw REST calls)* —
  reinventing a well-solved, well-maintained problem for no benefit

**Consequences.**
- `go.mod` declares `github.com/aws/aws-sdk-go-v2` and its `ec2`/`ssm`/`sts`
  submodules as dependencies
- Credential resolution (env vars, `~/.aws/credentials`, SSO) is handled by
  the SDK's default credential chain, matching current Bash behavior
- Region iteration (the four configured regions) becomes explicit per-region
  SDK client construction, replacing the `AWS_REGION` env var wrapper
  pattern used in `ec2_ami_manager.bash`

---

## 2026-06-30 — Retire check_ami.bash and check_ec2_instances.bash

**Context.** `check_ami.bash` and `check_ec2_instances.bash` predate
`ec2_ami_manager.bash`. Both are non-interactive, read-only listing scripts
across the same four regions; their functionality is fully covered by
`list_ec2_instances()`/`list_amis()` and `display_instances()`/`display_amis()`
in `ec2_ami_manager.bash`, which additionally aggregate and sort consistently.

**Decision.** **Retire both scripts; the unified manager is the single
listing entry point.**

**Rationale.**
- No functionality in either script is missing from the manager
- Two parallel implementations of the same AWS queries is a maintenance cost
  with no offsetting benefit
- DESIGN.md's file-structure section listed them as "Existing" scripts to
  keep alongside the new manager without deciding their long-term role —
  this resolves that gap

**Rejected alternatives.**
- *Keep as quick non-interactive utilities* — considered for cron/scripting
  use cases, but nothing in this project currently invokes them
  non-interactively, and `ec2_ami_manager.bash` could add a non-interactive
  `--list` flag later if that need arises

**Consequences.**
- DESIGN.md's file-structure section should drop these two scripts
- Deletion is a separate, explicit step — not yet performed as of this entry

---

## 2026-06-30 — AMI-from-instance: fold ami_copy.bash capabilities into Phase 5

**Context.** `ami_copy.bash` (and `ami_copy_basic_steps.md`) was merged in
from a separate repository (`git log`: "merged ami copy from ami_copy
repo") before this project's DESIGN.md/DECISIONS.md/PLAN.md existed. It was
never reconciled with the design: it duplicates the "Create AMI from EC2
Instance" feature (Phase 5 — see "Include both running and stopped
instances for AMI creation" above) but is single-region only, and it
contains capabilities Phase 5's `create_ami_from_instance_workflow` lacks:
volume-size-based time estimates, prior-snapshot detection, an SSM `fstrim`
pre-snapshot optimization step, unbounded elapsed-time polling during
creation, and Postgres/OpenSearch-specific crash-consistency guidance for
running instances (relevant because this team's primary AMI target is
Invenio RDM instances).

Separately, Phase 5's `post_ami_creation_actions()` times out after 600
seconds (10 minutes) of polling. Per `ami_copy_basic_steps.md`'s own timing
table, this is too short for real usage — even small volumes take 5–15
minutes, and an Invenio RDM instance is estimated at 20–60+ minutes.

**Decision.** **Port `ami_copy.bash`'s capabilities into Phase 5's
multi-region workflow (tracked as Phase 5b in PLAN.md), then retire
`ami_copy.bash`.** Keep the multi-region aggregation Phase 5 already has
rather than narrowing to `ami_copy.bash`'s single-region scope.

**Rationale.**
- Multi-region aggregation (an earlier decision, see "All four regions
  aggregated in unified view" above) is more valuable than what
  `ami_copy.bash` offers and shouldn't be lost
- The volume-size estimate, fstrim step, and unbounded polling are real
  operational value specific to this team's large, stateful instances —
  losing them by simply deleting `ami_copy.bash` would be a regression
- The 600-second timeout in Phase 5 is a correctness bug independent of the
  consolidation question and should be fixed regardless

**Rejected alternatives.**
- *Keep both scripts indefinitely* — two divergent implementations of the
  same operation with different region scope is confusing and doubles
  future maintenance
- *Retire ami_copy.bash immediately without porting* — would silently drop
  working functionality (fstrim optimization, realistic time estimates,
  Invenio-specific guidance) that was never documented elsewhere

**Consequences.**
- New PLAN.md Phase 5b enumerates the specific functions/behavior to port
- `ami_copy.bash` and `ami_copy_basic_steps.md` are retired only after
  Phase 5b is implemented and verified, not before
- DESIGN.md's Core Features and File Structure sections should be updated
  once Phase 5b lands

---

## 2026-06-30 — AMI scope limited to account-owned only

**Context.** When listing available AMIs for instance creation, we must choose between showing all AMIs (public + private), only AWS marketplace AMIs, only account-owned AMIs, or a filtered subset.

**Decision.** **Show only AMIs owned by the current AWS account.**

**Rationale.**
- Reduces clutter and confusion for users managing their own infrastructure
- Public AMIs are typically accessed through other workflows (Launch Templates, console)
- Owned AMIs are the primary concern for lifecycle management (creation/removal)
- Simplifies the pick list to relevant, actionable items
- Aligns with the AMI removal feature which only makes sense for owned AMIs

**Rejected alternatives.**
- *All AMIs (public + private)* — would overwhelm users with thousands of irrelevant AMIs
- *Owned + AWS marketplace* — AWS marketplace AMIs cannot be deleted, creating inconsistency in the removal feature
- *Custom filter by tags* — adds complexity for initial implementation; can be added later as a filter option

**Consequences.**
- Users cannot launch instances from public AMIs through this script
- If needed, a future enhancement could add a "Show public AMIs" toggle
- The script focuses on managing custom/private infrastructure

---

## 2026-06-30 — All four regions aggregated in unified view

**Context.** The script must operate across four AWS regions. We must decide whether to show each region separately, aggregate all regions, or let the user select a region per operation.

**Decision.** **Aggregate all four regions in a single unified view.**

**Rationale.**
- Provides a complete picture of infrastructure across all regions at a glance
- Matches the user's stated requirement to "list the EC2 instances from one of the four regions we use"
- Simplifies the user experience by eliminating region selection from the critical path
- Instance and AMI IDs are globally unique, so aggregation doesn't cause collisions

**Rejected alternatives.**
- *Single region (user picks first)* — requires extra step and doesn't show full infrastructure state
- *Region per operation* — inconsistent UX, requires region selection for every action

**Consequences.**
- All API calls must be made against each of the four regions
- Aggregation logic needed to combine and deduplicate results
- Display must include region column for all resources
- Performance: slightly slower due to 4x API calls, but acceptable for interactive use

---

## 2026-06-30 — Prompt user for all instance creation parameters

**Context.** When creating an EC2 instance from an AMI, we need values for instance-type, key-name, security-group-ids, subnet-id, and other optional parameters. We must decide on the default behavior.

**Decision.** **Prompt the user for all required parameters interactively, with no hardcoded defaults.**

**Rationale.**
- Maximum flexibility for users with different requirements
- Prevents launching instances with inappropriate defaults
- Educational: helps users understand what's required for instance creation
- Reduces risk of launching instances in wrong subnet/security group

**Rejected alternatives.**
- *Hardcoded sensible defaults* — defaults may not be appropriate for all use cases; could lead to instances in wrong network configuration
- *Config file with overrides* — adds complexity; users may not have config files set up; harder to use across different projects

**Consequences.**
- More interactive steps required from user
- Need validation for each parameter
- Should provide lists of available options (key pairs, security groups, subnets) to help user choose
- May add helper to show "common" instance types with descriptions

---

## 2026-06-30 — User-provided AMI names for new AMIs

**Context.** When creating an AMI from an EC2 instance, we need a name for the new AMI. We must decide how this name is generated.

**Decision.** **Require user to provide the AMI name interactively.**

**Rationale.**
- AMI names are user-facing and should be meaningful
- Users have different naming conventions based on their organization
- Auto-generated names might not match organizational standards
- Simple and predictable for users

**Rejected alternatives.**
- *Auto-generate from instance name* — instance may not have a Name tag; generated names might not be descriptive enough
- *Auto-generate with prefix* — requires configuration of prefix; less flexible
- *Name + tags* — adds complexity for initial implementation

**Consequences.**
- User must think of a name each time (minor friction)
- Can add auto-suggestion in future based on instance metadata
- Name validation needed (AMI name constraints: 3-128 characters, specific allowed characters)

---

## 2026-06-30 — Multi-layer confirmation for AMI removal

**Context.** Removing an AMI is destructive and irreversible. We must implement safety mechanisms to prevent accidental deletion.

**Decision.** **Implement three-layer confirmation:**
1. Dry-run first: Show exactly what would be deleted
2. Show dependencies: List any instances currently using this AMI
3. Type to confirm: User must type the AMI ID or name exactly

**Rationale.**
- AMI deletion cannot be undone
- Instances using a deleted AMI cannot be launched or rebooted (if instance-store backed)
- Typo in selection could cause significant data loss
- Multiple confirmation layers provide defense in depth

**Rejected alternatives.**
- *Simple yes/no prompt* — too easy to accidentally confirm
- *Dry-run only* — doesn't prevent accidental confirmation
- *Show dependencies only* — doesn't verify user intentionality

**Consequences.**
- More steps required to delete an AMI (acceptable for safety-critical operation)
- Need to implement dependency checking (query instances by ImageId)
- Must handle case where AMI has no instances using it
- Error handling for typed confirmation mismatch

---

## 2026-06-30 — Use AWS CLI v2 with jq for JSON parsing

**Context.** We need to interact with AWS APIs and parse their JSON output in Bash.

**Decision.** **Use AWS CLI v2 with jq for JSON parsing.**

**Rationale.**
- AWS CLI v2 is the current standard, maintained by AWS
- jq is the de facto standard for JSON parsing in shell scripts
- Both are likely already installed in environments using AWS
- Provides full access to AWS API functionality

**Rejected alternatives.**
- *AWS CLI v1* — deprecated, no longer maintained
- *aws-shell* — not suitable for scripting
- *Boto3 (Python)* — requires Python, not pure Bash
- *Custom JSON parsing with grep/sed* — fragile, error-prone

**Consequences.**
- Script requires AWS CLI v2 and jq as dependencies
- Need to check for these dependencies on startup
- Error messages should guide users to install missing tools

---

## 2026-06-30 — Numbered menu for pick lists

**Context.** The script needs to present lists of resources (AMIs, instances) for user selection. We must choose an interaction method.

**Decision.** **Use numbered menus for resource selection.**

**Rationale.**
- Works in all terminal environments (no special dependencies)
- Simple and familiar to users
- Easy to implement in pure Bash
- No external tools required (like fzf)

**Rejected alternatives.**
- *fzf fuzzy finder* — requires fzf installation; not available on all systems
- *arrow-key navigation* — complex to implement in pure Bash; requires ncurses or similar
- *Search/filter* — adds complexity for initial implementation

**Consequences.**
- For large lists (>20 items), may need pagination
- User must type number corresponding to selection
- Input validation needed for number range
- Can add fzf support as optional enhancement in future

---

## 2026-06-30 — Include both running and stopped instances for AMI creation

**Context.** When creating an AMI from an instance, the instance can be in various states. We must decide which states are allowed.

**Decision.** **Allow both running and stopped instances to be selected for AMI creation.**

**Rationale.**
- AWS supports creating AMIs from both running and stopped instances
- Stopped instances are common targets for AMI creation (clean state)
- Running instances might need AMIs created for backup purposes
- Matches the user's stated requirement

**Rejected alternatives.**
- *Running only* — excludes common use case of creating AMI from stopped instance
- *Stopped only* — excludes emergency backup scenarios
- *All states including terminated* — terminated instances cannot have AMIs created

**Consequences.**
- Need to filter out terminated instances from the pick list
- May want to warn user about creating AMI from running instance (potential inconsistency)
- For running instances, can offer no-reboot option

---

## 2026-06-30 — Refresh data after each operation

**Context.** After performing operations (create instance, create AMI, remove AMI), the displayed resource lists become stale. We must decide when to refresh.

**Decision.** **Refresh all resource data after each operation that modifies state, and provide a manual refresh option.**

**Rationale.**
- Ensures user always sees current state
- Automated refresh after modifications provides immediate feedback
- Manual refresh option allows user to see latest state at any time
- Simple to implement (re-run the listing functions)

**Rejected alternatives.**
- *Refresh only on demand* — user might not realize data is stale
- *Periodic auto-refresh* — adds complexity, unnecessary API calls
- *No refresh* — poor UX, user sees outdated information

**Consequences.**
- Each operation will include the cost of 8 API calls (4 regions x 2 resource types)
- Need to consider performance for slow connections
- Can add progress indicators during refresh
