# AWS Tools — Interactive EC2/AMI Manager — Implementation Plan

## Source Design

See `DESIGN.md` and `DECISIONS.md` for the complete design and decision rationale.

## Status (2026-06-30)

`ec2_ami_manager.bash` already implements Phases 0–7 in code (dependency
checks, region wrappers, listing, pick lists, instance creation, AMI
creation, AMI removal, main menu). Test coverage from Phase 9 lags behind:
Phases 0–4 have working BATS tests (`tests/test_dependencies.bats`,
`tests/test_listing.bats`, `tests/test_picklist.bats`,
`tests/test_instance_creation.bats`); Phases 5–7 (AMI creation, AMI removal,
main menu) have no tests yet (`test_create_ami.bats`, `test_remove_ami.bats`,
`test_menu.bats` do not exist). See `tests/README.md` for the current
function-by-function coverage table.

The checkboxes and phase descriptions below describe the original plan as
written; they have not been retroactively marked complete. Phase 5b below is
new — added after discovering `ami_copy.bash`'s capabilities were never
folded into this plan.

## Phases

This implementation follows a phased approach with tests written before code (TDD).

---

## Phase 0 — Project Setup & Test Infrastructure

**Effort:** ~2 hours
**Priority:** High

### Tasks

- [ ] Create `tests/` directory structure
- [ ] Install BATS (Bash Automated Testing System) if not present
- [ ] Create `test_helper.bash` with common test setup (mock AWS CLI responses)
- [ ] Create BATS test scaffold for main script
- [ ] Set up GitHub Actions or local CI for test execution

### Deliverables
- `tests/` directory with helper scripts
- Basic test infrastructure that can mock AWS CLI calls
- Documentation on running tests locally

### Dependencies
- BATS installation
- Mock AWS CLI response files for testing

---

## Phase 1 — AWS CLI Wrapper Layer

**Effort:** ~4 hours
**Priority:** High

### Work Items

#### W0 — Dependency Check

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `check_dependencies()` function
- Verify AWS CLI v2 is installed and configured
- Verify jq is installed
- Verify AWS credentials are available
- Exit with clear error message if dependencies missing

**Tests:**
- Test with all dependencies present (expect pass)
- Test with missing AWS CLI (expect clear error)
- Test with missing jq (expect clear error)
- Test with missing credentials (expect clear error)

**Dependency:** Phase 0

---

#### W1 — Region Configuration

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Define array of four regions: `REGIONS=(us-east-1 us-east-2 us-west-1 us-west-2)`
- Create `for_all_regions()` helper function to iterate across regions
- Create `aws_ec2()` wrapper that adds `--region` parameter

**Tests:**
- Test that all four regions are defined
- Test that region iteration works correctly

**Dependency:** Phase 0

---

#### W2 — AWS CLI Error Handling

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `aws_cli_call()` wrapper function
- Parse AWS CLI JSON output for errors
- Extract and display error messages clearly
- Handle common error types (AccessDenied, InvalidParameter, etc.)
- Implement retry logic with exponential backoff (max 3 attempts)

**Tests:**
- Test successful AWS CLI call (expect JSON output)
- Test AWS CLI error parsing (expect error message extraction)
- Test retry logic (mock failures then success)
- Test max retry exceeded (expect error exit)

**Dependency:** W0

---

## Phase 2 — Resource Listing Functions

**Effort:** ~6 hours
**Priority:** High

### Work Items

#### L0 — List EC2 Instances Across All Regions

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `list_ec2_instances()` function
- Call `aws ec2 describe-instances` for each of the four regions
- Filter for non-terminated instances
- Extract: InstanceId, InstanceType, State, ImageId, Tags (Name), Region
- Aggregate results from all regions into single array
- Sort by Region, then InstanceId

**Tests:**
- Test with no instances (expect empty list)
- Test with instances in multiple regions (expect aggregated list)
- Test with instances having Name tags (expect name displayed)
- Test with instances without Name tags (expect empty name)

**Dependency:** W2

---

#### L1 — List Owned AMIs Across All Regions

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `list_amis()` function
- Call `aws ec2 describe-images --owners self` for each region
- Filter for available AMIs
- Extract: ImageId, Name, CreationDate, State, Region
- Aggregate results from all regions into single array
- Sort by Region, then CreationDate (newest first)

**Tests:**
- Test with no owned AMIs (expect empty list)
- Test with AMIs in multiple regions (expect aggregated list)
- Test sorting (expect newest first within each region)

**Dependency:** W2

---

#### L2 — Display Formatting

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `display_instances()` function
- Format instance list as table with columns: ID, Name, State, AMI ID, Region
- Truncate long values with ellipsis (e.g., IDs to 12 chars)
- Create `display_amis()` function
- Format AMI list as table with columns: AMI ID, Name, Creation Date, Region
- Add visual separators between sections

**Tests:**
- Test display with empty lists
- Test display with multiple items
- Test truncation of long values

**Dependency:** L0, L1

---

## Phase 3 — Pick List Implementation

**Effort:** ~4 hours
**Priority:** High

### Work Items

#### P0 — Generic Pick List Function

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `show_pick_list()` function
- Accept array of items as input
- Display numbered list (1-N)
- Accept user input with validation
- Return selected index or exit on invalid/cancel
- Handle empty list case

**Tests:**
- Test with empty list (expect error message, return to menu)
- Test with single item (expect auto-select or prompt)
- Test with multiple items (expect correct selection)
- Test with invalid input (expect re-prompt)
- Test with cancel input (expect return to menu)

**Dependency:** Phase 2

---

#### P1 — AMI Pick List

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `pick_ami()` function
- Call `list_amis()` to get current AMI list
- Pass to `show_pick_list()` with appropriate display formatting
- Return selected AMI (full object with all fields)

**Tests:**
- Test selection from AMI list
- Test cancel behavior

**Dependency:** P0, L1

---

#### P2 — Instance Pick List

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `pick_instance()` function
- Call `list_ec2_instances()` to get current instance list
- Filter by state if needed (running, stopped, or both)
- Pass to `show_pick_list()` with appropriate display formatting
- Return selected instance (full object with all fields)

**Tests:**
- Test selection from instance list
- Test filtering by state
- Test cancel behavior

**Dependency:** P0, L0

---

## Phase 4 — Create EC2 Instance from AMI

**Effort:** ~8 hours
**Priority:** High

### Work Items

#### C0 — Parameter Collection

**Effort:** ~3 hours
**Files:** `ec2_ami_manager.bash`

- Create `collect_instance_params()` function
- After AMI selection, prompt for:
  - Instance type: list available types, allow user selection
  - Key pair name: list available key pairs, allow selection
  - Security group IDs: list available security groups, allow multi-select
  - Subnet ID: list available subnets, allow selection
  - IAM instance profile: list available, optional
  - User data: text input, optional
  - Tags: key=value pairs, optional
- Validate each parameter
- Allow back/redo for any parameter

**Helper functions needed:**
- `list_instance_types()` — call AWS for available types in region
- `list_key_pairs()` — call AWS for available key pairs in region
- `list_security_groups()` — call AWS for available security groups
- `list_subnets()` — call AWS for available subnets
- `list_instance_profiles()` — call AWS for available IAM instance profiles

**Tests:**
- Test parameter collection with valid inputs
- Test validation of invalid instance types
- Test validation of non-existent key pairs
- Test cancel behavior at any prompt

**Dependency:** P1, W2

---

#### C1 — Confirmation and Launch

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `confirm_and_launch()` function
- Display all collected parameters in readable format
- Ask for final confirmation
- Call `aws ec2 run-instances` with all parameters
- Parse response for new InstanceId
- Display success message with new instance details

**Tests:**
- Test confirmation flow
- Test cancel at confirmation
- Test successful launch (mock AWS response)
- Test failed launch (mock AWS error)

**Dependency:** C0

---

#### C2 — Post-Launch Actions

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- After successful launch:
  - Wait for instance to reach running state (optional, with timeout)
  - Display connection information (public IP, SSH command)
  - Refresh resource lists
  - Return to main menu

**Tests:**
- Test post-launch refresh
- Test connection info display

**Dependency:** C1

---

## Phase 5 — Create AMI from EC2 Instance

**Effort:** ~6 hours
**Priority:** Medium

### Work Items

#### A0 — Instance Selection and Validation

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Use `pick_instance()` with filter for running/stopped states
- Validate instance is not terminated
- Display selected instance details for confirmation
- Warn if instance is running (potential inconsistency in AMI)

**Tests:**
- Test selection of running instance
- Test selection of stopped instance
- Test that terminated instances are excluded

**Dependency:** P2

---

#### A1 — AMI Creation Parameters

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Prompt for:
  - AMI name (required, validate length/characters)
  - AMI description (optional)
  - No-reboot flag (default: false, only for running instances)
  - Tags (optional)
- Validate AMI name meets AWS requirements

**Tests:**
- Test valid AMI name
- Test invalid AMI name (too long, invalid characters)
- Test no-reboot flag only offered for running instances

**Dependency:** A0

---

#### A2 — Create and Verify AMI

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `create_ami_from_instance()` function
- Call `aws ec2 create-image` with parameters
- Parse response for new ImageId
- Wait for AMI to reach available state (with timeout)
- Display success message with new AMI details
- Refresh resource lists
- Return to main menu

**Tests:**
- Test successful AMI creation (mock response)
- Test AMI creation failure (mock error)
- Test timeout waiting for available state

**Dependency:** A1

---

## Phase 5b — Fold in `ami_copy.bash` capabilities

**Effort:** ~4 hours
**Priority:** High
**Source:** `ami_copy.bash`, `ami_copy_basic_steps.md` (merged from the separate `ami_copy` repo; see DECISIONS.md "AMI-from-instance: fold ami_copy.bash capabilities into Phase 5")

`ami_copy.bash` duplicates this phase's instance→AMI workflow but single-region,
and includes capabilities this phase's workflow lacks. Port them into the
multi-region workflow, then retire `ami_copy.bash`.

### Work Items

- [ ] Volume-size gathering: call `describe-volumes` for the selected
      instance, sum attached volume sizes, and show the same time-estimate
      table as `ami_copy_basic_steps.md` (<20GB: 5–15min, 20–100GB:
      15–45min, 100–200GB: 45–90min, 200+GB: 1.5–3+hrs)
- [ ] Prior-snapshot detection: note when an attached volume already has a
      snapshot, since only changed blocks will be copied
- [ ] SSM `fstrim` step: check SSM availability for the selected instance;
      if online, offer to run `fstrim -av` via `ssm send-command` before
      `create-image` to reduce copy time/size
- [ ] Fix the AMI-creation wait: `post_ami_creation_actions()` currently
      gives up after a 600-second (10 min) timeout. Per the timing table
      above, even small volumes take 5–15 minutes, and an Invenio RDM
      instance (Docker + PostgreSQL + OpenSearch) is estimated at 20–60+
      minutes — this timeout will fail on real usage. Replace with
      unbounded polling (elapsed-time display, Ctrl-C-safe — AMI creation
      continues in AWS regardless of whether the script is watching)
- [ ] Running-instance warning: carry over the Postgres/OpenSearch
      crash-consistency guidance from `ami_copy_basic_steps.md` into the
      running-instance warning shown by `select_instance_for_ami`/`A0`
- [ ] Retire `ami_copy.bash` once the above are folded in and verified
      equivalent (or better) across all four regions
- [ ] Remove `ami_copy_basic_steps.md`'s standalone-script framing, or fold
      its content into this script's `--help`/usage text

**Tests:**
- Test volume-size aggregation and time-estimate boundaries
- Test prior-snapshot detection messaging
- Test SSM-unavailable path (skips fstrim, proceeds with warning)
- Test that AMI-creation polling does not exit early before `available`/`failed`

**Dependency:** A2

---

## Phase 6 — Remove AMI

**Effort:** ~6 hours
**Priority:** Medium

### Work Items

#### R0 — AMI Selection

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Use `pick_ami()` to select AMI to remove
- Display selected AMI details

**Tests:**
- Test AMI selection for removal

**Dependency:** P1

---

#### R1 — Dry Run Display

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `show_removal_dry_run()` function
- Display what would be deleted (AMI ID, Name, Region)
- Ask user to confirm they want to proceed to dependency check

**Tests:**
- Test dry run display
- Test cancel at dry run

**Dependency:** R0

---

#### R2 — Dependency Check

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `check_ami_dependencies()` function
- Query all regions for instances using this AMI (by ImageId)
- Display list of dependent instances with details
- If dependencies exist, warn user and ask if they want to continue
- If no dependencies, display "No instances currently using this AMI"

**Tests:**
- Test with no dependencies (expect clean message)
- Test with dependencies in same region (expect warning)
- Test with dependencies in different regions (expect warning with all regions)

**Dependency:** R1

---

#### R3 — Type-to-Confirm

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `type_to_confirm()` function
- Prompt user: "Type the AMI ID to confirm deletion: "
- Compare input exactly (case-sensitive) with selected AMI ID
- On match, proceed to deletion
- On mismatch, display error and return to previous step
- Allow cancel option

**Tests:**
- Test correct AMI ID input (expect proceed)
- Test incorrect AMI ID input (expect error, re-prompt)
- Test cancel at confirmation

**Dependency:** R2

---

#### R4 — Execute Removal

**Effort:** ~1 hour
**Files:** `ec2_ami_manager.bash`

- Create `remove_ami()` function
- Call `aws ec2 deregister-image` for the AMI
- Handle potential errors (AMI in use, permissions, etc.)
- Verify removal was successful
- Display confirmation message
- Refresh resource lists
- Return to main menu

**Tests:**
- Test successful removal (mock response)
- Test removal failure (mock error - AMI in use)
- Test removal failure (mock error - permissions)

**Dependency:** R3

---

## Phase 7 — Main Menu and Integration

**Effort:** ~4 hours
**Priority:** High

### Work Items

#### M0 — Main Menu Display

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create `show_main_menu()` function
- Display header with script name and regions
- Display current EC2 instances section
- Display current AMIs section
- Display menu options:
  1. Create EC2 instance from AMI
  2. Create AMI from EC2 instance
  3. Remove AMI
  4. Refresh resource lists
  5. Exit
- Accept user input with validation

**Tests:**
- Test menu display with data
- Test menu display with no data
- Test valid menu selection
- Test invalid menu selection (expect re-prompt)

**Dependency:** Phase 2, Phase 3

---

#### M1 — Main Loop

**Effort:** ~2 hours
**Files:** `ec2_ami_manager.bash`

- Create main loop that:
  - Calls `show_main_menu()`
  - Dispatches to appropriate function based on selection
  - Handles errors gracefully
  - Refreshes data after state-changing operations
  - Exits cleanly on option 5
- Implement signal handling (Ctrl+C) for graceful exit

**Tests:**
- Test menu navigation
- Test operation dispatch
- Test refresh after operations
- Test clean exit

**Dependency:** M0, all Phase 4-6 items

---

## Phase 8 — Polish and Error Handling

**Effort:** ~4 hours
**Priority:** Medium

### Work Items

- [ ] Add loading indicators for long operations
- [ ] Add color coding for states (green=running, red=stopped, etc.)
- [ ] Add comprehensive error messages with actionable advice
- [ ] Add input validation for all user inputs
- [ ] Add timeout handling for AWS CLI calls
- [ ] Add pagination for large lists (>50 items)

---

## Phase 9 — Testing

**Effort:** ~8 hours
**Priority:** High

### Work Items

- [ ] Write BATS tests for all functions
- [ ] Create mock AWS CLI responses for testing
- [ ] Test error scenarios
- [ ] Test edge cases (empty lists, missing resources, etc.)
- [ ] Run full test suite against real AWS (with test account)
- [ ] Performance testing with many resources

---

## Phase 10 — Documentation

**Effort:** ~2 hours
**Priority:** Medium

### Work Items

- [ ] Add usage instructions to script header
- [ ] Create README.md with:
  - Overview
  - Prerequisites
  - Installation
  - Usage
  - Examples
  - Troubleshooting
- [ ] Update DESIGN.md, DECISIONS.md, PLAN.md with any changes from implementation

---

## Priority Order for Implementation

| Phase | Priority | Effort | Dependencies |
|-------|----------|--------|---------------|
| Phase 0 | High | 2h | None |
| Phase 1 | High | 4h | Phase 0 |
| Phase 2 | High | 6h | Phase 1 |
| Phase 3 | High | 4h | Phase 2 |
| Phase 4 | High | 8h | Phase 3 |
| Phase 7 | High | 4h | Phase 4 |
| Phase 5 | Medium | 6h | Phase 3 |
| Phase 6 | Medium | 6h | Phase 3 |
| Phase 8 | Medium | 4h | Phase 7 |
| Phase 9 | High | 8h | All |
| Phase 10 | Medium | 2h | All |

## Total Estimated Effort

**Total:** ~54 hours
- Phase 0-4, 7: Core functionality (~28 hours)
- Phase 5-6: AMI creation/removal (~12 hours)
- Phase 8-10: Polish, testing, docs (~14 hours)

## Milestones

1. **Milestone 1 (MVP):** Phase 0-4, 7 complete — Basic listing and instance creation (~32 hours)
2. **Milestone 2:** Phase 5-6 complete — Full AMI lifecycle management (~40 hours total)
3. **Milestone 3:** Phase 8-10 complete — Production ready (~54 hours total)

## Testing Strategy

All code follows TDD approach:
1. Write tests first for each function
2. Implement function to pass tests
3. Refactor as needed
4. Maintain >80% test coverage

Test types:
- Unit tests: Individual functions with mocked AWS CLI
- Integration tests: Function interactions with mocked AWS CLI
- End-to-end tests: Full workflows with mocked AWS CLI
- Manual tests: Against real AWS with test account
