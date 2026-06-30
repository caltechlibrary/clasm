# AWS Tools — Test Suite

## Overview

This directory contains BATS (Bash Automated Testing System) tests for the AWS Tools scripts.

## Directory Structure

```
tests/
├── README.md              ← This file
├── lib/
│   └── test_helper.bash   ← Common test setup, mocks, and helpers
├── test_dependencies.bats ← Tests for dependency checking and AWS wrappers
└── test_*.bats           ← Additional test files (to be created)
```

## Prerequisites

- **BATS**: Must be installed (see `../software_requirements.md`)
- **Bash**: 4.0+ recommended
- **AWS CLI v2**: For manual testing against real AWS
- **jq**: For JSON parsing

## Running Tests

### Run All Tests

```bash
# From project root
bats tests/

# Or from tests directory
cd tests
bats .
```

### Run Specific Test File

```bash
# Run dependency tests
bats tests/test_dependencies.bats

# With verbose output
bats -t tests/test_dependencies.bats
```

### Run Specific Test

```bash
# Run a single test by name
bats -f "test name pattern" tests/test_dependencies.bats

# Example
bats -f "REGIONS constant" tests/test_dependencies.bats
```

## Test Organization

Tests are organized by phase from the implementation plan:

- **Phase 0**: Project setup and test infrastructure (this directory)
- **Phase 1**: AWS CLI wrapper layer (test_dependencies.bats)
- **Phase 2**: Resource listing functions (TBD: test_listing.bats)
- **Phase 3**: Pick list implementation (TBD: test_picklist.bats)
- **Phase 4**: Create EC2 instance from AMI (TBD: test_create_instance.bats)
- **Phase 5**: Create AMI from EC2 instance (TBD: test_create_ami.bats)
- **Phase 6**: Remove AMI (TBD: test_remove_ami.bats)
- **Phase 7**: Main menu and integration (TBD: test_menu.bats)

## Mock AWS CLI

Tests use a mock AWS CLI to avoid hitting real AWS APIs. The mock:

1. Creates a temporary directory with `aws` and `jq` scripts
2. Adds this directory to the front of PATH
3. Each test can customize the mock responses
4. Cleanup happens automatically in teardown

### Creating Mock Responses

Use the helper functions in `lib/test_helper.bash`:

```bash
# Setup mock with no resources
mock_aws_empty

# Setup mock with sample instances and AMIs
mock_aws_with_instances

# Setup mock that returns an error
mock_aws_error "AccessDeniedException" "Access denied"

# Setup mock for successful run-instances
mock_aws_run_instances_success "i-1234567890abcdef0"
```

### Example Test Structure

```bash
#!/usr/bin/env bats

# Load test helper
load 'tests/lib/test_helper'

setup() {
    setup_mock_aws
    source_main_script
}

teardown() {
    cleanup_mock_aws
}

@test "example test" {
    # Create mock that returns specific data
    cat > "$MOCK_AWS_DIR/aws" << 'EOF'
#!/bin/bash
if [[ "$1" == "ec2" && "$2" == "describe-instances" ]]; then
    echo '{"Reservations": []}'
    exit 0
fi
exit 1
EOF
    chmod +x "$MOCK_AWS_DIR/aws"
    
    run list_ec2_instances
    [ "$status" -eq 0 ]
    # Add assertions...
}
```

## Test Coverage

| Function | Test File | Status |
|----------|-----------|--------|
| check_dependencies | test_dependencies.bats | ✅ Implemented |
| aws_cli_call | test_dependencies.bats | ✅ Implemented |
| aws_ec2 | test_dependencies.bats | ✅ Implemented |
| list_ec2_instances | test_listing.bats | ⏳ Not yet |
| list_amis | test_listing.bats | ⏳ Not yet |
| display_instances | test_listing.bats | ⏳ Not yet |
| display_amis | test_listing.bats | ⏳ Not yet |
| show_pick_list | test_picklist.bats | ⏳ Not yet |
| pick_ami | test_picklist.bats | ⏳ Not yet |
| pick_instance | test_picklist.bats | ⏳ Not yet |

## Tips for Writing Tests

1. **Start small**: Test one function at a time
2. **Use mocks**: Avoid hitting real AWS in tests
3. **Test edge cases**: Empty results, errors, invalid inputs
4. **Keep tests fast**: Tests should run in milliseconds
5. **Test both success and failure paths**

## Continuous Integration

For local development, run tests frequently:

```bash
# Run tests before committing
cd /Users/rsdoiel/WorkLab/aws_tools
bats tests/

# Run tests on file changes (requires entr or similar)
find tests -name "*.bats" | entr -r bats
```

## Debugging Tests

### Verbose Output

```bash
bats -t tests/test_dependencies.bats
```

### Show Only Failed Tests

```bash
bats --tap tests/ | grep -A2 "not ok"
```

### Run Single Test

```bash
# Use -f to filter by test name
bats -f "check_dependencies succeeds" tests/test_dependencies.bats
```

### Check BATS Version

```bash
bats --version
```

## Manual Testing Against Real AWS

For end-to-end testing against real AWS resources:

```bash
# Set AWS_REGION if needed
export AWS_REGION=us-east-1

# Run the main script
./ec2_ami_manager.bash

# Or test individual functions
source ec2_ami_manager.bash
check_dependencies
list_ec2_instances
```

**WARNING**: Manual testing against real AWS may incur costs and modify resources. Use with caution, preferably in a test AWS account.

## Adding New Test Files

1. Create a new `.bats` file in the `tests/` directory
2. Load the test helper: `load 'tests/lib/test_helper'`
3. Implement `setup()` and `teardown()` functions
4. Write test functions with `@test` annotation
5. Add the file to the test coverage table above

## Test Naming Conventions

- Test files: `test_<feature>.bats` (e.g., `test_listing.bats`)
- Test functions: Describe what they test (e.g., "list_ec2_instances aggregates across all regions")
- Use present tense for test descriptions
- Group related tests together in test files
