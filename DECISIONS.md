# AWS Tools — Architecture & UX Decision Log

This file records significant architectural and UX decisions for the interactive EC2/AMI manager, their rationale, and known trade-offs. New decisions are added at the top.

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
