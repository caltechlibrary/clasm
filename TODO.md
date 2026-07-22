
# Action items

## Bugs 

- [x] Menu-loop output-visibility bug (confirmed 2026-07-22, live real-AWS testing): any plain-printed status/error text immediately before a domain menu's next full-height `Select` redraw gets wiped before it can be read -- found via Resize Instance's Root Volume's own output, then confirmed as a systemic gap (same two print sites duplicated in all four domain menu loops) via a live typo's error message during instance cleanup. See DECISIONS.md, "Pause for acknowledgment before every menu-loop redraw," PLAN.md Phase 20.32. Implemented and unit-tested 2026-07-22, test-first throughout. Not yet re-verified live against real AWS or released. Targeted for v0.0.4.
- [ ] SSM never came online for either real-AWS test instance during Resize Instance's Root Volume verification (2026-07-22) -- both were launched via cloud-init with `IamInstanceProfile: null` (no instance profile at all), so `growRootFilesystem`'s automated OS-level growth (Part 2 of Phase 20.31) correctly fell back to manual instructions but remains unverified end-to-end against real AWS. Root cause and fix folded into the SSM-enforcement design below, not a bug in `growRootFilesystem` itself.

## Requested features

- [ ] Insist on SSM support (instance profile with SSM permissions) at launch time -- mirrors IMDSv2's unconditional enforcement -- across instance creation, cloud-init launch, and launch templates. Also cover associating/replacing an SSM-capable profile on an already-running instance (retrofit), since both of 2026-07-22's test instances have no profile at all and can't become SSM-manageable without relaunching otherwise. Supersedes/absorbs the IAM-instance-profile item below and the "Gap (found in production use, 2026-07-22)" entry in someday/maybe. Scoped 2026-07-22, not yet designed in DESIGN.md/PLAN.md. Targeted for v0.0.5.
- [ ] Support arm64/aarch64 (Graviton) alongside amd64 in the curated Ubuntu LTS AMI list and instance-type list -- a parallel Graviton `curatedInstanceTypes` family (t4g/m6g/c6g/r6g etc.), arm64 variants in `curatedUbuntuReleases`, and a new AMI-arch-vs-instance-type-arch pre-flight compatibility check (mirrors `ensureInstanceTypeENACompatible`). Raised 2026-07-22 in a conversation with colleagues: cost savings using ARM instances outside RDM. Bundle with the Ubuntu 26.04 LTS item below (same files). Targeted for v0.0.6.

- [ ] Need an improved way to interact with AIM profiles, see what they are, when to use them and apply them to resources (merged into the SSM-support item above, 2026-07-22)
- [ ] Need to include 26.04 LTS in our Ubuntu image listed for EC2 instance, AMI and launch templates (merged into the arm64/Graviton item above, 2026-07-22)
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

- **Gap (found in production use, 2026-07-22), merged into "Requested
  features"' SSM-support item above:** no way to attach/associate an
  IAM instance profile to an EC2 instance that's *already running* --
  surfaced twice the same day (once setting up an InvenioRDM test
  instance for S3 access, once as the reason SSM never came online
  for either Resize Instance's Root Volume test instance). Detail
  preserved here: `promptIAMInstanceProfileOrCreate`/
  `create_instance_profile.go` is only invoked from the launch
  workflow (`launch_instance.go`); `IAMInstanceProfile` is only ever
  consumed by `launch_execute.go` (`RunInstancesInput`) and
  `launch_template_create.go` (launch-template data); no
  `AssociateIamInstanceProfile`/`ReplaceIamInstanceProfileAssociation`
  call exists anywhere in the codebase, only the read-only
  `DescribeIamInstanceProfileAssociations`. Also, by design (per
  DECISIONS.md "2026-07-02 -- Support picking or creating an IAM
  instance profile from within awsops"), clasm only attaches an
  *existing* IAM role to a new instance profile -- it never creates
  the role or its permissions policy itself, so even the launch-time
  path requires the role/bucket-scoped policy to already exist,
  authored outside clasm. No longer someday/maybe: promoted to
  "Requested features" as part of the SSM-enforcement design.

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

