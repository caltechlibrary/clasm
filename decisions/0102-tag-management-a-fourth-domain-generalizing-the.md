---
id: "0102"
title: "Tag Management: a fourth domain, generalizing the Manage Tags loop across five resource types"
date: "2026-07-20"
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
uuid: "084b1836-6158-4896-8e1a-2ec29d7cc511"
origin_host: "MACMINI-RD.local"
---

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

