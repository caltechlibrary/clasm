
# Action items

## Bugs 

- [x] Each screen should fill the terminal window consistantly, still a problem in current version (commit a3c07051cf83899ca4b7dd333a55830870f183dc)
  - I don't think when a new screen is written that we're actually checking the window's height. Many screens clear but then ownly draw in about 1/3 of the window height
  - Confirmed fixed 2026-07-20 across all four TUI tiers (Menu/Picker/List/Manager) -- see TUI_REFERENCE.md
- [x] The bahavior of the filter and picker filter needs to be uniform, right now some filters treat "q" as quick filter rather than the default huh uses (empty string), the "q" doesn't exit the whole clasm but returns to the prior screen.
- [x] The screen for adding, updating, and removing tags is missing a "show tags" menu option and the tags shown at the top of he screen don't update on change
- [x] When creating a new EC2 instance the IDMSv2 metadata value should be set to true per AWS Security recommendations

## Requested features

- [ ] When doing the Archive backups, the S3 target bucket should be saved as a default but I'm not sure how this works with the bucket picker approach we have now. This needs to be explored.
- [x] A top level menu item for managing tags across resources (EC2, AMI, S3, etc)
  - All five resource types (Instance, AMI, Launch Template, Key Pair, S3 Bucket) done and confirmed against real AWS 2026-07-20 -- see PLAN.md Phase 20.30.
- [x] My work group uses launch templates instances for EC2 we need to support managing those (list, show a template, add, update and remove)
- [x] We need to way to sync a launch template with the updates from a cloud init YAML file
  - The flow is cloud init yaml -> launch template -> EC2 instance
- [ ] Need a show instances details, ami details, launch template details

## Nice to have

- [ ] Be able to list the CloudFront distribution ID for S3 static websites
- [x] More color usage will make the interface easier to read, we can show relationship between menu items using color to group
  - Color usage is substantially more extensive now that the whole UI runs on huh/lipgloss/bubbletea (2026-07-13 chrome standardization): one shared indigo accent across every field/box border/spinner, plus color-coded instance state (green=running, red=stopped, yellow=pending/stopping) in the List-tier tables. The specific "group menu items by color to show relationship" idea, though, was superseded by a deliberate opposite choice -- DECISIONS.md, "Chrome standardization: one shared indigo accent via lipgloss" -- one uniform accent everywhere, not a different color per domain/group, to keep every screen reading as the same visual language. Checking this off as addressed in spirit (color usage substantially increased, and reads more clearly), not as a literal per-group color scheme.
- [x] For actions that take more than a few minutes, a spinner that shows progress would be nice
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

- Change EBS storage properties (volume size, type, IOPS, throughput)
  in a launch template. AWS supports this fully via
  `RequestLaunchTemplateData.BlockDeviceMappings` -- confirmed against
  the SDK, 2026-07-20 -- but the current template model deliberately
  excludes it (DESIGN.md, "Launch Templates," curated field set), and
  the plain instance-launch wizard (Features 2/3) doesn't set
  `BlockDeviceMappings` either, so every instance clasm launches today
  just inherits the source AMI's default EBS config. Anticipated need:
  launch templates are expected to become the way development EC2
  instances get spun up, and instance type/CPU/RAM/storage will likely
  need refining over time based on cost -- not urgent yet, but flagged
  so it isn't lost.

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

