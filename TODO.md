
# Action items

## Bugs 

## Requested features

- [ ] Need to include 26.04 LTS in our Ubuntu image listed for EC2 instance, AMI and launch templates
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

- **Gap (found in production use, 2026-07-22):** no way to attach/associate
  an IAM instance profile to an EC2 instance that's *already running* --
  `promptIAMInstanceProfileOrCreate`/`create_instance_profile.go` is only
  invoked from the launch workflow (`launch_instance.go`), and
  `IAMInstanceProfile` is only ever consumed by `launch_execute.go`
  (`RunInstancesInput`) and `launch_template_create.go` (launch-template
  data). There is no `AssociateIamInstanceProfile` /
  `ReplaceIamInstanceProfileAssociation` call anywhere in the codebase --
  the only EC2 IAM-instance-profile API present is the read-only
  `DescribeIamInstanceProfileAssociations` (`internal/awsclient/ec2.go`),
  used just to display an instance's current profile. Separately (by
  design, per DECISIONS.md "2026-07-02 -- Support picking or creating an
  IAM instance profile from within awsops"): clasm only attaches an
  *existing* IAM role to a new instance profile
  (`iam:CreateInstanceProfile` + `iam:AddRoleToInstanceProfile`) -- it
  never creates the role or its permissions policy itself, so even the
  launch-time path requires the role/bucket-scoped policy to already
  exist, authored outside clasm.
  Surfaced when setting up an already-running InvenioRDM test instance
  (Granian-vs-Gunicorn experiment) that needed S3 access: the AWS
  best-practice approach (attach a role instead of putting static
  `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` in a `.env` file on disk)
  turned out to be unavailable for a running instance, so the workaround
  was a plain IAM user access key instead. Two separate improvements,
  either useful on its own: (1) support associating/replacing an instance
  profile on a running instance, not just at launch; (2) optionally
  support creating a minimal bucket-scoped role+policy from within clasm
  (currently explicitly out of scope) so instance-profile setup doesn't
  require a separate manual IAM detour.

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

