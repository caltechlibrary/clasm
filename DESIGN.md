# AWS Tools — Interactive EC2/AMI Manager — Design

## Overview

An interactive Bash script for managing AWS EC2 instances and AMIs across four regions (us-east-1, us-east-2, us-west-1, us-west-2). The script provides a unified view of resources and a menu-driven interface for common operations.

## User Experience Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  AWS EC2/AMI Manager                                            │
│  Regions: us-east-1, us-east-2, us-west-1, us-west-2            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ===== CURRENT EC2 INSTANCES =====                              │
│  ID           Name        State    AMI ID        Region         │
│  i-012345...  web-server  running  ami-abc123...  us-east-1     │
│  i-67890...   db-server   stopped  ami-def456...  us-west-2     │
│                                                                 │
│  ===== AVAILABLE AMIs (owned by account) =====                  │
│  AMI ID          Name              Creation Date    Region      │
│  ami-abc123...  base-ubuntu-2404  2026-01-15      us-east-1     │
│  ami-def456...  app-server-v2     2026-02-20      us-west-2     │
│  ami-ghi789...  custom-ami        2026-03-10      us-east-1     │
│                                                                 │
│  ===== MAIN MENU =====                                          │
│  1) Create EC2 instance from AMI                                │
│  2) Create AMI from EC2 instance (running or stopped)           │
│  3) Remove AMI                                                  │
│  4) Refresh resource lists                                      │
│  5) Exit                                                        │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Core Features

### 1. Unified Resource Listing

On startup, the script fetches and displays:
- All EC2 instances across the four configured regions
- For each instance: ID, Name (from tags), State, AMI ID, Region
- All AMIs owned by the current AWS account across the four regions
- For each AMI: ID, Name, Creation Date, Region

### 2. Create EC2 Instance from AMI

Interactive workflow:
1. Display pick list of available AMIs (filtered by owned-by-account)
2. User selects an AMI
3. Prompt for required parameters:
   - Instance type (with sensible default suggestion)
   - Key pair name (list available key pairs)
   - Security group IDs (list available security groups)
   - Subnet ID (list available subnets)
   - IAM instance profile (optional)
   - User data (optional)
   - Tags (Name, Environment, etc.)
4. Confirm all parameters before launching
5. Launch instance and display results

### 3. Create AMI from EC2 Instance

Interactive workflow:
1. Display pick list of EC2 instances (running or stopped)
2. User selects an instance
3. Prompt for:
   - AMI name (user-provided, required)
   - AMI description (optional)
   - No-reboot flag (default: false)
   - Tags (optional)
4. Confirm before creating
5. Create AMI and display new AMI ID

### 4. Remove AMI

Safety-first workflow:
1. Display pick list of owned AMIs
2. User selects an AMI
3. **Dry-run first**: Show what would be deleted
4. **Show dependencies**: List any instances currently using this AMI
5. **Type to confirm**: User must type the AMI ID or name exactly to proceed
6. Execute deletion
7. Confirm successful removal

## Architecture

```
┌─────────────────────────┐
│  main.bash              │  ← Entry point
│  - Main menu loop       │
│  - Coordinate workflows │
└─────────┬───────────────┘
          │
          ▼
┌──────────────────────────┐
│  Functions               │
│  ├── list_ec2_instances()  ← Query all 4 regions, aggregate
│  ├── list_ami()            ← Query owned AMIs across 4 regions
│  ├── create_instance()     ← Launch from selected AMI
│  ├── create_ami()          ← Create AMI from selected instance
│  └── remove_ami()          ← Delete with safety checks
└──────────────────────────┘
          │
          ▼
┌─────────────────────────┐
│  AWS CLI Wrappers       │
│  ├── aws_ec2()            ← Wrapper with region parameter
│  ├── aws_describe()       ← Generic describe wrapper
│  └── error handling       ← Parse and display AWS errors
└─────────────────────────┘
```

## Data Flow

```
User Interaction
     │
     ▼
Menu Selection (1-5)
     │
     ▼
┌─────────────────────────────────────┐
│  For each operation:                │
│  1. Fetch current resource data     │ ← aws ec2 describe-instances/describe-images
│  2. Filter/sort for display         │ ← Owned AMIs only, aggregate regions
│  3. Present pick list to user       │ ← Numbered menu with formatting
│  4. Collect additional parameters   │ ← Interactive prompts with validation
│  5. Perform AWS API call            │ ← aws ec2 run-instances/create-image/deregister-image
│  6. Display results                 │ ← Success/failure with details
│  7. Refresh displays                │ ← Return to main menu with updated data
└─────────────────────────────────────┘
```

## File Structure

```
aws_tools/
├── DESIGN.md          ← This document
├── DECISIONS.md       ← Architecture and UX decisions
├── PLAN.md            ← Implementation plan
├── ec2_ami_manager.bash   ← Main interactive script
├── check_ami.bash     ← Existing: List AMIs
├── check_ec2_instances.bash ← Existing: List instances
└── tests/
    ├── test_listing.bats     ← BATS tests for resource listing
    ├── test_create_instance.bats ← BATS tests for instance creation
    ├── test_create_ami.bats   ← BATS tests for AMI creation
    └── test_remove_ami.bats   ← BATS tests for AMI removal
```

## Dependencies

- **AWS CLI**: v2 required, configured with credentials for the target account
- **jq**: For JSON parsing of AWS CLI output
- **Bash**: 4.0+ recommended (for associative arrays if needed)
- **BATS**: For testing (optional but recommended)

## Assumptions

1. AWS credentials are already configured (`~/.aws/credentials` or environment variables)
2. User has permissions for: `ec2:DescribeInstances`, `ec2:DescribeImages`, `ec2:RunInstances`, `ec2:CreateImage`, `ec2:DeregisterImage`
3. Default VPC and subnet exist in each region, or user will provide specific values
4. Key pairs exist in each region, or user will create them separately

## Error Handling Strategy

1. **AWS API errors**: Parse and display the AWS error message clearly
2. **Validation errors**: Prompt user to re-enter invalid inputs
3. **Network/timeouts**: Retry with exponential backoff (max 3 attempts)
4. **Missing dependencies**: Clear error message with installation instructions
5. **Permission errors**: Display required IAM permissions and exit

## Security Considerations

1. Never store AWS credentials in the script
2. Always confirm destructive operations (AMI removal)
3. Display instance costs/estimates when creating (if possible)
4. Warn about public AMIs vs private AMIs
5. For AMI creation from instances: warn about any sensitive data on the instance
