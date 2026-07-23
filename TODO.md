
# Action items

## Bugs 

- [x] Menu-loop output-visibility bug -- fixed, real-AWS-verified, released in v0.0.4. See DECISIONS.md, "Pause for acknowledgment before every menu-loop redraw," PLAN.md Phase 20.32.
- [x] SSM never came online for the resize-verification test instances (2026-07-22) -- root cause (no SSM-capable instance profile available at all) closed structurally by the SSM-enforcement work below. `growRootFilesystem`'s own OS-level `growpart`/`resize2fs` automation still hasn't been specifically re-verified end-to-end with a proper instance profile in place -- worth doing next time Resize Instance's Root Volume comes up.
- [x] `InvalidUserData.Malformed: User data is limited to 16384 bytes` -- fixed and real-AWS-verified 2026-07-22 (`test-clasm-granian` launch template + instance). See DECISIONS.md, "Always gzip-compress user-data before base64-encoding it," PLAN.md Phase 20.34. Not yet released -- part of v0.0.5.

## Requested features

- [x] SSM-capable instance profile enforcement at launch + associate/replace retrofit for running instances -- designed, implemented, unit-tested, and real-AWS-verified 2026-07-22. See DESIGN.md/DECISIONS.md, "SSM-Capable Instance Profile Enforcement + Retrofit", PLAN.md Phase 20.33. Not yet released -- part of v0.0.5.
- [x] arm64/aarch64 (Graviton) support + Ubuntu 26.04 LTS in the curated AMI/instance-type lists -- designed, implemented, unit-tested, and real-AWS-verified 2026-07-22. See DESIGN.md/DECISIONS.md, "ARM64 (Graviton) Support + Ubuntu 26.04 LTS", PLAN.md Phase 20.35. Not yet released -- part of v0.0.5.

**v0.0.5 release: Phases 20.33/20.34/20.35 are done and real-AWS-verified, but release is now deliberately held back** -- 2026-07-23, the user chose to bundle IAM Profile & Role Management (below) into this same release rather than ship it separately as v0.0.6. See DECISIONS.md, "IAM Profile & Role Management: seven scoping decisions, bundled into v0.0.5."

- [ ] IAM Profile & Role Management -- discovery (Roles/Instance Profiles/Policies, each row showing its `Origin` tag's literal value or "(unset)", sorted by recency), a config-driven `Origin` tag (key + which value means DLD-owned, both configurable in `~/.clasm`, left unset until the user's group reacts to a demo -- no hardcoded vocabulary), a read-only guard on permission-changing actions for resources not recognized as DLD-owned (tagging itself is always allowed, for support-contact purposes, on IMSS- or AWS-owned resources too), IAM resources becoming taggable via the Tag Management domain (no separate backfill action needed), a detail view (trust policy, attached policies, cross-referenced usage), curated per-use-case role/policy creation templates (Static Website, RDM Repository, Bridge Service, Patron-Facing Service, Data Processing), and CRUD completion for DLD-owned roles (Delete Role, Attach/Detach Policy). See `aim_management_and_support_proposal.md`, DESIGN.md "IAM Profile & Role Management Domain" and "CRUD completion for DLD-owned roles", DECISIONS.md "IAM Profile & Role Management: seven scoping decisions, bundled into v0.0.5", "...Origin tag revision...", and "...support CRUD for DLD-owned roles", PLAN.md Phases 20.36-20.40. Part of v0.0.5.
  - [x] Phase 20.36 (discovery, `Origin` tag config, IAM domain menu, `RequireDLDOwned` guard predicate) implemented, unit-tested, and real-AWS-verified 2026-07-23. Real bug found via live use the same day and fixed -- `ListRoles`/`ListInstanceProfiles`/`ListPolicies` don't return `Tags` inline (contrary to the original design's assumption); fixed via per-resource `ListRoleTags`/`ListInstanceProfileTags`/`ListPolicyTags` calls. See DECISIONS.md, "Real bug: ListRoles/ListInstanceProfiles/ListPolicies don't return tags inline." User confirmed the fix and all six suggested manual test items pass.
  - [x] Phase 20.37 (Tag Management domain extension for IAM resources -- Role/Instance Profile/Policy all taggable via TagRole/TagInstanceProfile/TagPolicy and their Untag counterparts, plus "Show all tags") implemented, unit-tested, and real-AWS-verified 2026-07-23 (add/update/remove on a role, instance profile, and policy; Show all tags for all three; editing `Origin` itself through the ordinary tag flow). Second real bug found via the same live testing and fixed same day -- the IAM Role/Instance Profile/Policy pickers were slow to open (sequential per-resource tag fetches); fixed by parallelizing with a bounded worker pool. See DECISIONS.md, "Parallelize per-resource IAM tag fetches." User confirmed both the tagging behavior and the speed fix.
  - [x] Phase 20.38 (IAM detail view -- View Role Detail: trust policy, attached/inline policies with on-demand document viewing, tags, SSM-capable, referenced-by-profiles; View Instance Profile Detail: tags, contained role(s), each role's SSM-capable status) implemented, unit-tested, and real-AWS-verified 2026-07-23. The "which running instance is using this profile" cross-reference was deferred per the user's own scoping call (no direct API filter exists for it).
  - [x] Phase 20.39 (curated per-use-case role/policy creation templates -- Static Website, RDM Repository Instance, Bridge Service, Patron-Facing Service, Data Processing; guided flow: pick template, supply ARN params, confirm, create role+policy, attach, auto-tag if `origin_tag.dld_value` is configured) implemented and unit-tested 2026-07-23, test-first end to end. Real-AWS testing in progress -- RDM Repository Instance confirmed fully, Static Website confirmed in read-only mode; a real usability bug (hand-typed ARNs) found and fixed same day, see DECISIONS.md, "Phase 20.39 templates collect resource names/IDs, not ARNs."
  - [x] Phase 20.40 (CRUD completion for DLD-owned roles -- Delete Role with type-to-confirm and dedicated-policy cascade-delete; Attach Policy to Role and Detach Policy from Role with a plain confirm) implemented and unit-tested 2026-07-23, test-first throughout. Not yet real-AWS-verified. **All five IAM phases (20.36-20.40) now implemented.**
- [ ] When doing the Archive backups, the S3 target bucket should be saved as a default but I'm not sure how this works with the bucket picker approach we have now. This needs to be explored.
- [x] Set the root EBS volume size when creating an instance/launch template (instead of always inheriting the AMI's default, e.g. 8GB), and resize a running instance's root volume after the fact.
  - Designed and scoped 2026-07-21 -- see DESIGN.md, "Configurable EBS Root Volume Size", DECISIONS.md, "Configurable EBS root volume size: scope, flow coverage, and resize automation depth", and PLAN.md Phase 20.31.
  - Implemented and unit-tested 2026-07-21, both parts: setting the size at creation (instances and launch templates) and the new "Resize instance's root volume" menu entry, including automated OS-level `growpart`/`resize2fs`/`xfs_growfs` via SSM with a manual-instructions fallback for any layout it doesn't recognize (e.g. LVM). Not yet verified against real AWS.
- [ ] Need a show instances details, ami details, launch template details
  - Show Launch Template already exists (Compute domain, since 2026-07-20) and covers the launch-template half: AMI, instance type, root volume size (added 2026-07-21), key pair, IAM instance profile, security groups, subnet, Project/Environment tags, IMDSv2 status. What's still missing is the equivalent single-resource detail view for an individual EC2 instance and an individual AMI -- today those are only ever seen as a row in the list-tier table (Show instances/Show AMIs), not a dedicated detail screen. Should show the same shape of settings the launch template view already does: instance type, EBS volume size(s) (`volume_info.go`'s `GatherVolumeInfo` already fetches this for AMI-creation-time estimates, so the data path exists), security groups, subnet, IAM instance profile, key pair, tags, and (for AMIs) block device mappings/root device name.

## Nice to have

- [ ] A means of reporting the details of our an EC2 Instance, AMI instance or launch template, that can be downloaded as Markdown report document
- [ ] Be able to list the CloudFront distribution ID for S3 static websites
- [ ] Bulk object delete (the file manager's Delete/Sync actions, `internal/filemanager`) currently loops one `s3:DeleteObject` call per key. `github.com/peak/s5cmd/v2`'s `storage.S3.MultiDelete` batches keys into groups of up to 1000 and calls the batch `s3:DeleteObjects` API in parallel chunks -- not importable directly (aws-sdk-go v1 + urfave/cli coupling, vs. this project's aws-sdk-go-v2), but `aws-sdk-go-v2`'s `s3` package already exposes `DeleteObjects`, so the same batching pattern could be reimplemented natively without a new dependency. Evaluated 2026-07-09, flagged again as an open question in PLAN.md Phase 20.1's work items, still not started.
- [ ] Retry-on-launch-failure (general case): instead of bouncing back to
      the main menu on any `RunInstances` error, keep the already-collected
      params and let the operator re-enter just the field that's likely
      wrong instead of re-doing the whole launch flow. Granularity not yet
      decided (just Instance type, or any collected field). NOTE: the
      instance-type/AZ pre-flight check above already does a scoped version
      of this (change instance type / pick a different subnet / abort) for
      that one failure class, pre-flight rather than reactive -- this item
      is about generalizing to *any* RunInstances failure, not just that one.


## Someday/maybe (not on the active roadmap)

- Container management: AWS container registry support as a top-level
  menu item, plus SSM-based interaction with Docker containers running
  inside an EC2 instance (list the Docker services on an instance;
  image a running Docker service and save that image to the private
  container registry or S3). Moved here from Requested Features
  2026-07-20 -- no design work done, no committed timeline. The two
  items are grouped together (not split back into two separate
  entries) since the Docker-service imaging workflow's own save target
  is the container registry this would introduce -- picking this back
  up means designing both together, not the registry alone.

- CloudFront domain (PLAN.md Phase 21): designed in DESIGN.md/PLAN.md,
  no code written yet. Postponed by the user (2026-07-09), then further
  demoted the same day from "postponed to a later version" to
  someday/maybe -- no committed timeline (see DECISIONS.md, "Demote
  CloudFront to someday/maybe..."). Removed from `DomainActions`/the
  domain picker rather than left as a "not yet implemented" placeholder
  entry, so 0.0.1 doesn't expose a menu item that goes nowhere. The
  design in DESIGN.md/PLAN.md stays valid reference for if this is ever
  picked back up. Phase 22 (real-AWS testing for Key Management/S3) no
  longer depends on it.

- A compliance/audit-style report across the Tag Management domain's
  five resource types (EC2 instance, AMI, launch template, key pair, S3
  bucket): which resources are missing tags entirely (or missing the
  Project/Environment convention specifically). Raised 2026-07-20
  during Tag Management's design, expected to come up again later --
  distinct from "Show all tags" (Phase 20.30, per-resource-type,
  showing what each resource *has*), this would be a cross-type view
  answering "what's missing," which is a different query shape and
  likely needs its own design pass (e.g., does "missing tags" mean zero
  tags at all, or specifically missing Project/Environment?). Not
  scoped or started.

