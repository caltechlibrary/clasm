#!/usr/bin/env bash
# EC2/AMI Manager - Interactive AWS Resource Management
#
# Manages EC2 instances and AMIs across multiple AWS regions
# with an interactive menu-driven interface.

set -o errexit
set -o nounset
set -o pipefail

# =============================================================================
# CONSTANTS
# =============================================================================

# Supported AWS regions
readonly REGIONS=(us-east-1 us-east-2 us-west-1 us-west-2)

# Script name for display
readonly SCRIPT_NAME="EC2/AMI Manager"

# =============================================================================
# GLOBAL VARIABLES
# =============================================================================

# Cached resource data
declare -A INSTANCE_CACHE
declare -A AMI_CACHE

# =============================================================================
# DEPENDENCY CHECKING
# =============================================================================

# -----------------------------------------------------------------------------
# Check that all required dependencies are installed and configured
# Exit with error message if any are missing
# -----------------------------------------------------------------------------
check_dependencies() {
    local missing=0
    
    # Check Bash version
    if [[ "${BASH_VERSINFO[0]}" -lt 4 ]]; then
        echo "ERROR: Bash 4.0 or higher is required. You have ${BASH_VERSION}" >&2
        missing=$((missing + 1))
    fi
    
    # Check AWS CLI
    if ! command -v aws &>/dev/null; then
        echo "ERROR: AWS CLI v2 is not installed or not in PATH" >&2
        echo "  Install via MacPorts: sudo port install awscli" >&2
        echo "  Or see: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html" >&2
        missing=$((missing + 1))
    else
        local aws_version
        aws_version=$(aws --version 2>&1 | head -1 | cut -d' ' -f1 | cut -d'/' -f2)
        if [[ "$aws_version" != 2* ]]; then
            echo "ERROR: AWS CLI v2 is required. You have version $aws_version" >&2
            echo "  Upgrade to v2: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html" >&2
            missing=$((missing + 1))
        fi
    fi
    
    # Check jq
    if ! command -v jq &>/dev/null; then
        echo "ERROR: jq is not installed or not in PATH" >&2
        echo "  Install via MacPorts: sudo port install jq" >&2
        echo "  Or see: https://stedolan.github.io/jq/" >&2
        missing=$((missing + 1))
    fi
    
    # Check AWS credentials
    if ! aws sts get-caller-identity &>/dev/null; then
        echo "ERROR: AWS credentials are not configured or are invalid" >&2
        echo "  Configure with: aws configure" >&2
        echo "  Or set environment variables AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY" >&2
        missing=$((missing + 1))
    fi
    
    if [[ $missing -gt 0 ]]; then
        echo "" >&2
        echo "Please install the missing dependencies and try again." >&2
        exit 1
    fi
    
    echo "All dependencies OK"
}

# =============================================================================
# AWS CLI WRAPPERS
# =============================================================================

# -----------------------------------------------------------------------------
# Generic AWS CLI call with error handling and retry logic
# Arguments:
#   All arguments are passed directly to aws command
# Returns:
#   stdout from aws command, or exits on error after retries
# -----------------------------------------------------------------------------
aws_cli_call() {
    local max_retries=${AWS_RETRIES:-3}
    local retry_delay=1
    local attempt=1
    local last_exit_code=0
    local output=""
    
    while [[ $attempt -le $max_retries ]]; do
        output=$("aws" "$@" 2>&1)
        last_exit_code=$?
        
        if [[ $last_exit_code -eq 0 ]]; then
            echo "$output"
            return 0
        fi
        
        # Check for throttling errors
        if [[ "$output" == *"Throttling"* ]] || [[ "$output" == *"Rate exceeded"* ]]; then
            if [[ $attempt -lt $max_retries ]]; then
                echo "AWS rate limit hit, retrying in ${retry_delay}s (attempt $attempt/$max_retries)..." >&2
                sleep "$retry_delay"
                retry_delay=$((retry_delay * 2))  # Exponential backoff
                attempt=$((attempt + 1))
                continue
            fi
        fi
        
        # Not a retryable error, break immediately
        break
    done
    
    echo "$output" >&2
    echo "ERROR: AWS CLI command failed after $attempt attempts" >&2
    return $last_exit_code
}

# -----------------------------------------------------------------------------
# AWS EC2 wrapper with region support
# All arguments are passed directly to aws ec2 command
# Region can be overridden via AWS_REGION environment variable
# Defaults to first region in REGIONS array
# -----------------------------------------------------------------------------
aws_ec2() {
    local region="${AWS_REGION:-${REGIONS[0]}}"
    
    aws_cli_call ec2 --region "$region" "$@"
}

# =============================================================================
# MAIN SCRIPT
# =============================================================================

main() {
    # Check dependencies
    check_dependencies
    
    echo "$SCRIPT_NAME - AWS EC2/AMI Manager"
    echo "Regions: ${REGIONS[*]}"
    echo ""
    
    # TODO: Implement main menu and functionality
    echo "Main menu implementation coming soon..."
}

# Run main if script is executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
