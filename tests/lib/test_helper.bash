#!/usr/bin/env bash
# AWS Tools Test Helper
# Common setup and mock functions for BATS tests

# This file is sourced by BATS test files to provide:
# - Mock AWS CLI commands
# - Common test data
# - Helper functions for test setup/teardown

set -o errexit
set -o nounset
set -o pipefail

# Directory where this helper is located
TEST_HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Project root directory (two levels up from tests/lib/)
PROJECT_ROOT="$(dirname "$(dirname "$TEST_HELPER_DIR")")"

# Temporary directory for test files
TEST_TMP_DIR=""

# Array of regions used by the project
REGIONS=("us-east-1" "us-east-2" "us-west-1" "us-west-2")

# =============================================================================
# MOCK AWS CLI SETUP
# =============================================================================

# Directory to store mock AWS CLI scripts
MOCK_AWS_DIR=""

# Original PATH backup
ORIGINAL_PATH=""

# -----------------------------------------------------------------------------
# Initialize mock AWS CLI environment
# Creates a temporary directory with mock 'aws' and 'jq' commands
# -----------------------------------------------------------------------------
setup_mock_aws() {
    TEST_TMP_DIR="$(mktemp -d)"
    MOCK_AWS_DIR="$TEST_TMP_DIR/mock_aws"
    mkdir -p "$MOCK_AWS_DIR"
    
    # Backup original PATH
    ORIGINAL_PATH="$PATH"
    
    # Add mock directory to PATH (at the beginning to take precedence)
    export PATH="$MOCK_AWS_DIR:$PATH"
    
    # Create mock jq that just passes through or does basic parsing
    cat > "$MOCK_AWS_DIR/jq" << 'JQEOF'
#!/bin/bash
# Mock jq - minimal implementation for testing
# For now, just cat (most tests will use AWS CLI mocks that return valid JSON)
cat
JQEOF
    chmod +x "$MOCK_AWS_DIR/jq"
    
    # Create initial mock aws command (will be overwritten by individual tests)
    create_mock_aws_default
}

# -----------------------------------------------------------------------------
# Cleanup mock AWS CLI environment
# -----------------------------------------------------------------------------
cleanup_mock_aws() {
    # Restore original PATH
    export PATH="$ORIGINAL_PATH"
    
    # Remove temporary directory
    if [[ -n "$TEST_TMP_DIR" && -d "$TEST_TMP_DIR" ]]; then
        rm -rf "$TEST_TMP_DIR"
    fi
    
    TEST_TMP_DIR=""
    MOCK_AWS_DIR=""
    ORIGINAL_PATH=""
}

# -----------------------------------------------------------------------------
# Create default mock aws command
# Returns empty JSON for describe calls, error for others
# -----------------------------------------------------------------------------
create_mock_aws_default() {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
# Default mock AWS CLI - returns empty results

COMMAND="$1"

case "$COMMAND" in
    ec2)
        SUBCOMMAND="$2"
        case "$SUBCOMMAND" in
            describe-instances)
                echo '{"Reservations": []}'
                exit 0
                ;;
            describe-images)
                echo '{"Images": []}'
                exit 0
                ;;
            describe-key-pairs)
                echo '{"KeyPairs": []}'
                exit 0
                ;;
            describe-security-groups)
                echo '{"SecurityGroups": []}'
                exit 0
                ;;
            describe-subnets)
                echo '{"Subnets": []}'
                exit 0
                ;;
            *)
                echo "ERROR: Unknown subcommand: $SUBCOMMAND" >&2
                exit 1
                ;;
        esac
        ;;
    *)
        echo "ERROR: Unknown command: $COMMAND" >&2
        exit 1
        ;;
esac
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

# =============================================================================
# MOCK AWS RESPONSES
# =============================================================================

# -----------------------------------------------------------------------------
# Mock: EC2 instances in multiple regions
# -----------------------------------------------------------------------------
mock_aws_with_instances() {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
COMMAND="$1"
SUBCOMMAND="$2"

# Check for region flag
REGION=""
for arg in "$@"; do
    if [[ "$arg" == --region* ]]; then
        REGION="${arg#--region=}"
    fi
done

if [[ "$COMMAND" == "ec2" ]]; then
    case "$SUBCOMMAND" in
        describe-instances)
            if [[ "$REGION" == "us-east-1" ]]; then
                echo '{
                  "Reservations": [
                    {
                      "Instances": [
                        {
                          "InstanceId": "i-0123456789abcdef0",
                          "InstanceType": "t2.micro",
                          "State": {"Code": 16, "Name": "running"},
                          "ImageId": "ami-abc1234567890",
                          "Tags": [{"Key": "Name", "Value": "web-server"}]
                        }
                      ]
                    }
                  ]
                }'
            elif [[ "$REGION" == "us-west-2" ]]; then
                echo '{
                  "Reservations": [
                    {
                      "Instances": [
                        {
                          "InstanceId": "i-abcdef01234567890",
                          "InstanceType": "t2.small",
                          "State": {"Code": 80, "Name": "stopped"},
                          "ImageId": "ami-def4567890123",
                          "Tags": [{"Key": "Name", "Value": "db-server"}]
                        }
                      ]
                    }
                  ]
                }'
            else
                echo '{"Reservations": []}'
            fi
            exit 0
            ;;
        describe-images)
            if [[ "$REGION" == "us-east-1" ]]; then
                echo '{
                  "Images": [
                    {
                      "ImageId": "ami-abc1234567890",
                      "Name": "base-ubuntu-2404",
                      "CreationDate": "2026-01-15T10:00:00Z",
                      "State": "available",
                      "OwnerId": "123456789012"
                    },
                    {
                      "ImageId": "ami-ghi7890123456",
                      "Name": "custom-ami",
                      "CreationDate": "2026-03-10T10:00:00Z",
                      "State": "available",
                      "OwnerId": "123456789012"
                    }
                  ]
                }'
            elif [[ "$REGION" == "us-west-2" ]]; then
                echo '{
                  "Images": [
                    {
                      "ImageId": "ami-def4567890123",
                      "Name": "app-server-v2",
                      "CreationDate": "2026-02-20T10:00:00Z",
                      "State": "available",
                      "OwnerId": "123456789012"
                    }
                  ]
                }'
            else
                echo '{"Images": []}'
            fi
            exit 0
            ;;
        *)
            echo "ERROR: Unknown subcommand: $SUBCOMMAND" >&2
            exit 1
            ;;
    esac
else
    echo "ERROR: Unknown command: $COMMAND" >&2
    exit 1
fi
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

# -----------------------------------------------------------------------------
# Mock: Empty results (no instances, no AMIs)
# -----------------------------------------------------------------------------
mock_aws_empty() {
    create_mock_aws_default
}

# -----------------------------------------------------------------------------
# Mock: Error responses
# -----------------------------------------------------------------------------
mock_aws_error() {
    local error_type="$1"
    local error_message="$2"
    
    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
COMMAND="$1"
echo '{"__type": "$error_type", "message": "$error_message"}' >&2
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

# -----------------------------------------------------------------------------
# Mock: run-instances success
# -----------------------------------------------------------------------------
mock_aws_run_instances_success() {
    local new_instance_id="$1"
    
    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
COMMAND="$1"
SUBCOMMAND="$2"

if [[ "$COMMAND" == "ec2" && "$SUBCOMMAND" == "run-instances" ]]; then
    echo '{
      "Instances": [
        {
          "InstanceId": "$new_instance_id",
          "InstanceType": "t2.micro",
          "State": {"Code": 0, "Name": "pending"},
          "ImageId": "ami-1234567890",
          "PrivateIpAddress": "10.0.0.1",
          "PublicIpAddress": "203.0.113.1"
        }
      ]
    }'
    exit 0
fi

# Fallback to empty defaults
create_mock_aws_default
exit 0
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

# =============================================================================
# TEST DATA GENERATORS
# =============================================================================

# -----------------------------------------------------------------------------
# Generate mock EC2 instance data
# -----------------------------------------------------------------------------
generate_mock_instance() {
    local region="$1"
    local instance_id="$2"
    local name="$3"
    local state="$4"
    local ami_id="$5"
    local instance_type="${6:-t2.micro}"
    
    cat <<EOF
{
  "InstanceId": "$instance_id",
  "InstanceType": "$instance_type",
  "State": {"Code": $(get_state_code "$state"), "Name": "$state"},
  "ImageId": "$ami_id",
  "Tags": [{"Key": "Name", "Value": "$name"}]
}
EOF
}

get_state_code() {
    case "$1" in
        pending) echo 0 ;;
        running) echo 16 ;;
        shutting-down) echo 32 ;;
        terminated) echo 48 ;;
        stopping) echo 64 ;;
        stopped) echo 80 ;;
        *) echo 0 ;;
    esac
}

# -----------------------------------------------------------------------------
# Generate mock AMI data
# -----------------------------------------------------------------------------
generate_mock_ami() {
    local region="$1"
    local ami_id="$2"
    local name="$3"
    local creation_date="$4"
    
    cat <<EOF
{
  "ImageId": "$ami_id",
  "Name": "$name",
  "CreationDate": "$creation_date",
  "State": "available",
  "OwnerId": "123456789012"
}
EOF
}

# =============================================================================
# ASSERTION HELPERS
# =============================================================================

# -----------------------------------------------------------------------------
# Assert that a string contains a substring
# Usage: assert_contains "$output" "expected substring" "message"
# -----------------------------------------------------------------------------
assert_contains() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-String does not contain expected substring}"
    
    if [[ "$haystack" != *"$needle"* ]]; then
        echo "FAIL: $message" >&2
        echo "  Expected to find: $needle" >&2
        echo "  In: $haystack" >&2
        return 1
    fi
    return 0
}

# -----------------------------------------------------------------------------
# Assert that a string does NOT contain a substring
# Usage: assert_not_contains "$output" "unexpected substring" "message"
# -----------------------------------------------------------------------------
assert_not_contains() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-String contains unexpected substring}"
    
    if [[ "$haystack" == *"$needle"* ]]; then
        echo "FAIL: $message" >&2
        echo "  Did not expect to find: $needle" >&2
        echo "  In: $haystack" >&2
        return 1
    fi
    return 0
}

# -----------------------------------------------------------------------------
# Assert that a string equals another string
# Usage: assert_equals "$actual" "$expected" "message"
# -----------------------------------------------------------------------------
assert_equals() {
    local actual="$1"
    local expected="$2"
    local message="${3:-Strings are not equal}"
    
    if [[ "$actual" != "$expected" ]]; then
        echo "FAIL: $message" >&2
        echo "  Expected: $expected" >&2
        echo "  Actual:   $actual" >&2
        return 1
    fi
    return 0
}

# =============================================================================
# SOURCE THE MAIN SCRIPT
# =============================================================================

# -----------------------------------------------------------------------------
# Source the main script under test
# Usage: source_main_script
# -----------------------------------------------------------------------------
source_main_script() {
    source "$PROJECT_ROOT/ec2_ami_manager.bash"
}
