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

# Cached resource data (as strings, not associative arrays - for Bash 3.2 compatibility)
# INSTANCE_CACHE=""
# AMI_CACHE=""

# =============================================================================
# DEPENDENCY CHECKING
# =============================================================================

# -----------------------------------------------------------------------------
# Check that all required dependencies are installed and configured
# Exit with error message if any are missing
# -----------------------------------------------------------------------------
check_dependencies() {
    local missing=0
    
    # Check Bash version (3.2+ is minimum, 4.0+ recommended)
    if [[ "${BASH_VERSINFO[0]}" -lt 3 ]] || [[ "${BASH_VERSINFO[0]}" -eq 3 && "${BASH_VERSINFO[1]}" -lt 2 ]]; then
        echo "ERROR: Bash 3.2 or higher is required. You have ${BASH_VERSION}" >&2
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

# -----------------------------------------------------------------------------
# AWS SSM wrapper with region support
# All arguments are passed directly to aws ssm command
# Region can be overridden via AWS_REGION environment variable
# Defaults to first region in REGIONS array
# -----------------------------------------------------------------------------
aws_ssm() {
    local region="${AWS_REGION:-${REGIONS[0]}}"

    aws_cli_call ssm --region "$region" "$@"
}

# =============================================================================
# RESOURCE LISTING FUNCTIONS
# =============================================================================

# -----------------------------------------------------------------------------
# List all EC2 instances across all configured regions
# Returns: JSON array of instances with selected fields
# Each instance includes: InstanceId, InstanceType, State, ImageId, Name, Region
# -----------------------------------------------------------------------------
list_ec2_instances() {
    local all_instances="[]"
    
    for region in "${REGIONS[@]}"; do
        # Get instances for this region
        local response
        response=$(AWS_REGION="$region" aws_ec2 describe-instances 2>/dev/null)
        
        if [[ -n "$response" ]]; then
            # Extract non-terminated instances and add region field
            local region_instances
            region_instances=$(echo "$response" | jq --arg r "$region" '
                [.Reservations[]?.Instances[]? | select(.State.Name != "terminated") |
                {
                    InstanceId: .InstanceId,
                    InstanceType: .InstanceType,
                    State: .State.Name,
                    ImageId: .ImageId,
                    Name: ([.Tags[]? | select(.Key == "Name") | .Value] | first // ""),
                    Region: $r
                }]
            ')
            
            # Combine with all instances
            if [[ -n "$region_instances" && "$region_instances" != "null" ]]; then
                all_instances=$(echo "$all_instances" | jq --argjson new "$region_instances" '. + $new')
            fi
        fi
    done
    
    echo "$all_instances"
}

# -----------------------------------------------------------------------------
# List all AMIs owned by the current account across all configured regions
# Returns: JSON array of AMIs with selected fields
# Each AMI includes: ImageId, Name, CreationDate, State, Region
# -----------------------------------------------------------------------------
list_amis() {
    local all_amis="[]"
    local account_id
    
    # Get current account ID
    account_id=$(aws sts get-caller-identity --query "Account" --output text 2>/dev/null)
    
    if [[ -z "$account_id" ]]; then
        echo "[]"
        return
    fi
    
    for region in "${REGIONS[@]}"; do
        # Get AMIs for this region owned by our account
        local response
        response=$(AWS_REGION="$region" aws_ec2 describe-images --owners "$account_id" 2>/dev/null)
        
        if [[ -n "$response" ]]; then
            # Extract available AMIs and add region field
            local region_amis
            region_amis=$(echo "$response" | jq --arg r "$region" '
                [.Images[]? | select(.State == "available") |
                {
                    ImageId: .ImageId,
                    Name: .Name,
                    CreationDate: .CreationDate,
                    State: .State,
                    Region: $r
                }]
            ')
            
            # Combine with all AMIs
            if [[ -n "$region_amis" && "$region_amis" != "null" ]]; then
                all_amis=$(echo "$all_amis" | jq --argjson new "$region_amis" '. + $new')
            fi
        fi
    done
    
    echo "$all_amis"
}

# -----------------------------------------------------------------------------
# Display formatted table of EC2 instances
# Arguments: JSON array from list_ec2_instances()
# -----------------------------------------------------------------------------
display_instances() {
    local instances="$1"
    
    if [[ -z "$instances" || "$instances" == "[]" ]]; then
        echo "No EC2 instances found."
        return
    fi
    
    echo "===== CURRENT EC2 INSTANCES ====="
    echo ""
    printf "%-20s %-20s %-12s %-20s %s\n" "INSTANCE ID" "NAME" "STATE" "AMI ID" "REGION"
    echo "--------------------------------------------------------------------------------------------------"
    
    echo "$instances" | jq -r '.[] | 
        "\(.InstanceId // "")\t\(.Name // "")\t\(.State // "")\t\(.ImageId // "")\t\(.Region // "")"
    ' | while IFS=$'\t' read -r id name state ami region; do
        printf "%-20s %-20s %-12s %-20s %s\n" "${id:0:20}" "${name:0:20}" "${state:0:12}" "${ami:0:20}" "$region"
    done
    echo ""
}

# -----------------------------------------------------------------------------
# Display formatted table of AMIs
# Arguments: JSON array from list_amis()
# -----------------------------------------------------------------------------
display_amis() {
    local amis="$1"
    
    if [[ -z "$amis" || "$amis" == "[]" ]]; then
        echo "No AMIs found."
        return
    fi
    
    echo "===== AVAILABLE AMIs ====="
    echo ""
    printf "%-20s %-28s %-20s %s\n" "AMI ID" "NAME" "CREATION DATE" "REGION"
    echo "------------------------------------------------------------------------------------------"

    echo "$amis" | jq -r '.[] |
        "\(.ImageId // "")\t\(.Name // "")\t\(.CreationDate // "")\t\(.Region // "")"
    ' | while IFS=$'\t' read -r ami_id name creation_date region; do
        printf "%-20s %-28s %-20s %s\n" "${ami_id:0:20}" "${name:0:28}" "${creation_date:0:19}" "$region"
    done
    echo ""
}

# =============================================================================
# PICK LIST FUNCTIONS
# =============================================================================

# -----------------------------------------------------------------------------
# Display a numbered pick list and return the selected index
# Arguments:
#   $1: Array name (e.g., "items")
#   $2: Description for the list (optional)
# Returns:
#   The selected index (0-based) via the PICK_LIST_RESULT variable
#   Or exits on cancel
# -----------------------------------------------------------------------------
show_pick_list() {
    local arr_name="$1"
    local description="${2:-Select an item}"
    local items
    eval "items=(\"\${${arr_name}[@]}\")"
    
    if [[ ${#items[@]} -eq 0 ]]; then
        echo "No items available."
        return 1
    fi
    
    echo ""
    echo "$description"
    echo ""
    
    local i=1
    for item in "${items[@]}"; do
        echo "  [$i] $item"
        i=$((i + 1))
    done
    
    echo ""
    echo -n "Enter your choice (1-${#items[@]}), or 'q' to cancel: "
    local choice
    read -r choice
    
    if [[ "$choice" == "q" || "$choice" == "Q" ]]; then
        echo "Cancelled."
        return 1
    fi
    
    # Validate input
    if ! [[ "$choice" =~ ^[0-9]+$ ]]; then
        echo "Invalid input. Please enter a number."
        return 1
    fi
    
    local index=$((choice - 1))
    if [[ $index -lt 0 || $index -ge ${#items[@]} ]]; then
        echo "Invalid selection. Please try again."
        return 1
    fi
    
    PICK_LIST_RESULT=$index
    return 0
}

# -----------------------------------------------------------------------------
# Prompt user to pick an AMI from the list
# Returns:
#   The selected AMI (as JSON string) via PICKED_AMI variable
#   Or returns 1 on cancel/error
# -----------------------------------------------------------------------------
pick_ami() {
    local amis_json
    amis_json=$(list_amis)
    
    if [[ "$amis_json" == "[]" ]]; then
        echo "No AMIs available to select from."
        return 1
    fi
    
    # Convert JSON array to bash array of formatted strings
    local ami_items=()
    local count
    count=$(echo "$amis_json" | jq 'length')
    
    for ((i=0; i<count; i++)); do
        local ami_id name creation_date region
        ami_id=$(echo "$amis_json" | jq -r ".[${i}].ImageId // \"\"")
        name=$(echo "$amis_json" | jq -r ".[${i}].Name // \"\"")
        creation_date=$(echo "$amis_json" | jq -r ".[${i}].CreationDate // \"\"")
        region=$(echo "$amis_json" | jq -r ".[${i}].Region // \"\"")
        
        # Format: AMI ID - Name (Region) - Created
        ami_items+=("${ami_id} - ${name} (${region}) - ${creation_date:0:10}")
    done
    
    if ! show_pick_list ami_items "Select an AMI:"; then
        return 1
    fi
    
    # Return the full JSON object for the selected AMI
    PICKED_AMI=$(echo "$amis_json" | jq ".[${PICK_LIST_RESULT}]")
    return 0
}

# -----------------------------------------------------------------------------
# Prompt user to pick an EC2 instance from the list
# Arguments:
#   $1: Filter by state (optional: "running", "stopped", or "all" - default is all non-terminated)
# Returns:
#   The selected instance (as JSON string) via PICKED_INSTANCE variable
#   Or returns 1 on cancel/error
# -----------------------------------------------------------------------------
pick_instance() {
    local filter_state="${1:-all}"
    local instances_json
    instances_json=$(list_ec2_instances)
    
    if [[ "$instances_json" == "[]" ]]; then
        echo "No instances available to select from."
        return 1
    fi
    
    # Filter by state if specified
    local filtered_json="$instances_json"
    if [[ "$filter_state" != "all" ]]; then
        filtered_json=$(echo "$instances_json" | jq --arg state "$filter_state" '
            [.[] | select(.State == $state)]
        ')
    fi
    
    if [[ "$filtered_json" == "[]" ]]; then
        echo "No instances match the filter (state: $filter_state)."
        return 1
    fi
    
    # Convert JSON array to bash array of formatted strings
    local instance_items=()
    local count
    count=$(echo "$filtered_json" | jq 'length')
    
    for ((i=0; i<count; i++)); do
        local instance_id name state ami_id region
        instance_id=$(echo "$filtered_json" | jq -r ".[${i}].InstanceId // \"\"")
        name=$(echo "$filtered_json" | jq -r ".[${i}].Name // \"\"")
        state=$(echo "$filtered_json" | jq -r ".[${i}].State // \"\"")
        ami_id=$(echo "$filtered_json" | jq -r ".[${i}].ImageId // \"\"")
        region=$(echo "$filtered_json" | jq -r ".[${i}].Region // \"\"")
        
        # Format: Instance ID - Name - State - AMI ID (Region)
        instance_items+=("${instance_id} - ${name:-<unnamed>} - ${state} - ${ami_id} (${region})")
    done
    
    if ! show_pick_list instance_items "Select an EC2 instance:"; then
        return 1
    fi
    
    # Return the full JSON object for the selected instance
    PICKED_INSTANCE=$(echo "$filtered_json" | jq ".[${PICK_LIST_RESULT}]")
    return 0
}

# =============================================================================
# INSTANCE CREATION FUNCTIONS
# =============================================================================

# -----------------------------------------------------------------------------
# List available instance types in a region
# Arguments:
#   $1: Region (optional, defaults to first in REGIONS)
# Returns:
#   JSON array of instance type info
# -----------------------------------------------------------------------------
list_instance_types() {
    local region="${1:-${REGIONS[0]}}"
    
    # Get available instance types
    local response
    response=$(AWS_REGION="$region" aws_ec2 describe-instance-types 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        echo "[]"
        return
    fi
    
    # Extract instance type names and filter for current-generation
    echo "$response" | jq '
        [.InstanceTypes[]? | select(.CurrentGeneration == true) |
        {
            InstanceType: .InstanceType,
            MemoryInfo: .MemoryInfo,
            VCpuInfo: .VCpuInfo
        }]
    '
}

# -----------------------------------------------------------------------------
# List available key pairs in a region
# Arguments:
#   $1: Region (optional, defaults to first in REGIONS)
# Returns:
#   JSON array of key pair names
# -----------------------------------------------------------------------------
list_key_pairs() {
    local region="${1:-${REGIONS[0]}}"
    
    local response
    response=$(AWS_REGION="$region" aws_ec2 describe-key-pairs 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        echo "[]"
        return
    fi
    
    echo "$response" | jq '[.KeyPairs[]?.KeyName]'
}

# -----------------------------------------------------------------------------
# List available security groups in a region
# Arguments:
#   $1: Region (optional, defaults to first in REGIONS)
# Returns:
#   JSON array of security group info
# -----------------------------------------------------------------------------
list_security_groups() {
    local region="${1:-${REGIONS[0]}}"
    
    local response
    response=$(AWS_REGION="$region" aws_ec2 describe-security-groups 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        echo "[]"
        return
    fi
    
    echo "$response" | jq '
        [.SecurityGroups[]? |
        {
            GroupId: .GroupId,
            GroupName: .GroupName,
            Description: .Description,
            VpcId: .VpcId
        }]
    '
}

# -----------------------------------------------------------------------------
# List available subnets in a region
# Arguments:
#   $1: Region (optional, defaults to first in REGIONS)
# Returns:
#   JSON array of subnet info
# -----------------------------------------------------------------------------
list_subnets() {
    local region="${1:-${REGIONS[0]}}"
    
    local response
    response=$(AWS_REGION="$region" aws_ec2 describe-subnets 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        echo "[]"
        return
    fi
    
    echo "$response" | jq '
        [.Subnets[]? |
        {
            SubnetId: .SubnetId,
            CidrBlock: .CidrBlock,
            VpcId: .VpcId,
            AvailabilityZone: .AvailabilityZone,
            AvailableIpAddressCount: .AvailableIpAddressCount
        }]
    '
}

# -----------------------------------------------------------------------------
# List available IAM instance profiles
# Returns:
#   JSON array of instance profile info
# -----------------------------------------------------------------------------
list_instance_profiles() {
    local response
    response=$(aws iam list-instance-profiles 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        # Try alternative approach
        response=$(aws_ec2 describe-iam-instance-profile-associations 2>/dev/null)
    fi
    
    if [[ -z "$response" ]]; then
        echo "[]"
        return
    fi
    
    echo "$response" | jq '[.InstanceProfiles[]?.InstanceProfileName] // [.InstanceProfiles[]?.Arn] // []'
}

# -----------------------------------------------------------------------------
# Collect parameters for creating an EC2 instance from an AMI
# Arguments:
#   $1: The AMI JSON object (from pick_ami)
# Returns:
#   Sets global variables with collected parameters:
#   - INSTANCE_PARAMS: JSON object with all parameters
#   Or returns 1 on cancel
# -----------------------------------------------------------------------------
collect_instance_params() {
    local ami_json="$1"
    local ami_id
    ami_id=$(echo "$ami_json" | jq -r '.ImageId')
    local ami_region
    ami_region=$(echo "$ami_json" | jq -r '.Region')
    
    echo ""
    echo "=== Create EC2 Instance from AMI ==="
    echo "AMI: $(echo "$ami_json" | jq -r '.Name') (ID: $ami_id)"
    echo "Region: $ami_region"
    echo ""
    
    # Collect instance type
    echo "Available instance types in $ami_region:"
    local types_json
    types_json=$(list_instance_types "$ami_region")
    local types_count
    types_count=$(echo "$types_json" | jq 'length')
    
    if [[ $types_count -eq 0 ]]; then
        echo "  No instance types found. Using default: t2.micro"
        local instance_type="t2.micro"
    else
        # Display first 10 types
        echo "$types_json" | jq -r '.[] | .InstanceType' | head -10 | nl -w2
        echo -n "Enter instance type (or press Enter for t2.micro): "
        local instance_type
        read -r instance_type
        
        if [[ -z "$instance_type" ]]; then
            instance_type="t2.micro"
        fi
    fi
    
    # Collect key pair
    echo ""
    echo "Available key pairs in $ami_region:"
    local keypairs_json
    keypairs_json=$(list_key_pairs "$ami_region")
    local keypairs_count
    keypairs_count=$(echo "$keypairs_json" | jq 'length')
    
    if [[ $keypairs_count -eq 0 ]]; then
        echo "  No key pairs found."
        local key_name=""
    else
        echo "$keypairs_json" | jq -r '.[]' | nl -w2
        echo -n "Enter key pair name: "
        local key_name
        read -r key_name
    fi
    
    # Collect security group
    echo ""
    echo "Available security groups in $ami_region:"
    local sgs_json
    sgs_json=$(list_security_groups "$ami_region")
    local sgs_count
    sgs_count=$(echo "$sgs_json" | jq 'length')
    
    if [[ $sgs_count -eq 0 ]]; then
        echo "  No security groups found."
        local sg_ids=()
    else
        echo "$sgs_json" | jq -r '.[] | "\(.GroupId) - \(.GroupName)"' | nl -w2
        echo -n "Enter security group ID (or comma-separated list): "
        local sg_input
        read -r sg_input
        
        # Split by comma
        IFS=',' read -ra sg_ids <<< "$sg_input"
    fi
    
    # Collect subnet
    echo ""
    echo "Available subnets in $ami_region:"
    local subnets_json
    subnets_json=$(list_subnets "$ami_region")
    local subnets_count
    subnets_count=$(echo "$subnets_json" | jq 'length')
    
    if [[ $subnets_count -eq 0 ]]; then
        echo "  No subnets found."
        local subnet_id=""
    else
        echo "$subnets_json" | jq -r '.[] | "\(.SubnetId) - \(.CidrBlock) - \(.AvailabilityZone)"' | nl -w2
        echo -n "Enter subnet ID: "
        local subnet_id
        read -r subnet_id
    fi
    
    # Collect IAM instance profile (optional)
    echo ""
    echo "Available IAM instance profiles:"
    local profiles_json
    profiles_json=$(list_instance_profiles)
    local profiles_count
    profiles_count=$(echo "$profiles_json" | jq 'length')
    
    if [[ $profiles_count -eq 0 ]]; then
        echo "  No instance profiles found."
        local instance_profile_arn=""
    else
        echo "$profiles_json" | jq -r '.[]' | nl -w2
        echo -n "Enter IAM instance profile name/ARN (optional, press Enter to skip): "
        local instance_profile_arn
        read -r instance_profile_arn
    fi
    
    # Collect user data (optional)
    echo ""
    echo -n "Enter user data script (optional, press Enter to skip): "
    local user_data
    read -r user_data
    
    # Collect tags
    echo ""
    echo "Enter tags (optional):"
    echo "  Format: Key1=Value1,Key2=Value2"
    echo -n "  Tags: "
    local tags_input
    read -r tags_input
    
    # Build tags JSON
    local tags_json="{}"
    if [[ -n "$tags_input" ]]; then
        IFS=',' read -ra tag_pairs <<< "$tags_input"
        for tag_pair in "${tag_pairs[@]}"; do
            if [[ -n "$tag_pair" ]]; then
                local key="${tag_pair%%=*}"
                local value="${tag_pair#*=}"
                tags_json=$(echo "$tags_json" | jq --arg k "$key" --arg v "$value" '. + {($k): $v}')
            fi
        done
    fi
    
    # Build the final parameters JSON
    INSTANCE_PARAMS=$(jq -n \
        --arg it "$instance_type" \
        --arg kn "$key_name" \
        --argjson sg "$(printf '%s\n' "${sg_ids[@]}" | jq -R 'split("\n") | map(select(. != ""))')" \
        --arg sub "$subnet_id" \
        --arg ip "$instance_profile_arn" \
        --arg ud "$user_data" \
        --arg ami "$ami_id" \
        --arg region "$ami_region" \
        --argjson tags "$tags_json" \
        '{
            ImageId: $ami,
            InstanceType: $it,
            KeyName: $kn,
            SecurityGroupIds: $sg,
            SubnetId: $sub,
            IamInstanceProfile: {"Arn": $ip},
            UserData: $ud,
            TagSpecifications: [{
                ResourceType: "instance",
                Tags: $tags
            }],
            MinCount: 1,
            MaxCount: 1
        }')
    
    return 0
}

# -----------------------------------------------------------------------------
# Display confirmation and launch EC2 instance
# Uses INSTANCE_PARAMS global variable
# Returns:
#   The new InstanceId via CREATED_INSTANCE_ID variable
#   Or returns 1 on cancel/error
# -----------------------------------------------------------------------------
confirm_and_launch() {
    if [[ -z "$INSTANCE_PARAMS" ]]; then
        echo "ERROR: No instance parameters collected."
        return 1
    fi
    
    echo ""
    echo "=== Confirm Instance Creation ==="
    echo ""
    
    # Display parameters in readable format
    local ami_id
    ami_id=$(echo "$INSTANCE_PARAMS" | jq -r '.ImageId')
    local instance_type
    instance_type=$(echo "$INSTANCE_PARAMS" | jq -r '.InstanceType')
    local key_name
    key_name=$(echo "$INSTANCE_PARAMS" | jq -r '.KeyName // "(none)"')
    local sg_ids
    sg_ids=$(echo "$INSTANCE_PARAMS" | jq -r '.SecurityGroupIds | join(", ") // "(none)"')
    local subnet_id
    subnet_id=$(echo "$INSTANCE_PARAMS" | jq -r '.SubnetId // "(none)"')
    local profile_arn
    profile_arn=$(echo "$INSTANCE_PARAMS" | jq -r '.IamInstanceProfile.Arn // "(none)"')
    local user_data
    user_data=$(echo "$INSTANCE_PARAMS" | jq -r '.UserData // "(none)"')
    local tags
    tags=$(echo "$INSTANCE_PARAMS" | jq -r '.TagSpecifications[0].Tags // {} | to_entries | map("\n  \(.key): \(.value)") | join("")')
    
    echo "AMI ID: $ami_id"
    echo "Instance Type: $instance_type"
    echo "Key Name: $key_name"
    echo "Security Groups: $sg_ids"
    echo "Subnet ID: $subnet_id"
    echo "IAM Instance Profile: $profile_arn"
    echo "User Data: ${user_data:0:50}${user_data:50:+...}"
    if [[ -n "$tags" ]]; then
        echo -e "Tags:$tags"
    fi
    
    echo ""
    echo -n "Launch this instance? (y/N): "
    local confirm
    read -r confirm
    
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
        echo "Cancelled."
        return 1
    fi
    
    # Launch the instance
    # Extract the region from the parameters or use default
    local region
    region=$(echo "$INSTANCE_PARAMS" | jq -r --arg default "${REGIONS[0]}" '.Region // $default')
    
    local response
    if ! response=$(AWS_REGION="$region" aws_ec2 run-instances "$(echo "$INSTANCE_PARAMS" | jq -c '.')" 2>&1); then
        echo "ERROR: Failed to launch instance: $response"
        return 1
    fi
    
    # Extract the new instance ID
    CREATED_INSTANCE_ID=$(echo "$response" | jq -r '.Instances[0].InstanceId')
    
    if [[ -z "$CREATED_INSTANCE_ID" ]]; then
        echo "ERROR: No instance ID returned from AWS."
        return 1
    fi
    
    echo "Instance launched successfully!"
    echo "Instance ID: $CREATED_INSTANCE_ID"
    
    return 0
}

# -----------------------------------------------------------------------------
# Post-launch actions: wait for instance to be running, display connection info
# Uses CREATED_INSTANCE_ID global variable
# -----------------------------------------------------------------------------
post_launch_actions() {
    if [[ -z "$CREATED_INSTANCE_ID" ]]; then
        echo "ERROR: No instance ID for post-launch actions."
        return 1
    fi
    
    local region="${REGIONS[0]}"
    local instance_state="pending"
    local max_wait=300  # 5 minutes
    local wait_interval=5
    local elapsed=0
    
    echo ""
    echo "Waiting for instance to be running..."
    
    while [[ "$instance_state" != "running" && $elapsed -lt $max_wait ]]; do
        sleep "$wait_interval"
        elapsed=$((elapsed + wait_interval))
        
        local state_response
        state_response=$(AWS_REGION="$region" aws_ec2 describe-instances --instance-ids "$CREATED_INSTANCE_ID" 2>/dev/null)
        
        if [[ -n "$state_response" ]]; then
            instance_state=$(echo "$state_response" | jq -r '.Reservations[0].Instances[0].State.Name // "unknown"')
            echo "  Current state: $instance_state (waited ${elapsed}s)"
        fi
    done
    
    if [[ "$instance_state" == "running" ]]; then
        echo "Instance is now running!"
        
        # Get instance details for connection info
        local details_response
        details_response=$(AWS_REGION="$region" aws_ec2 describe-instances --instance-ids "$CREATED_INSTANCE_ID" 2>/dev/null)
        
        if [[ -n "$details_response" ]]; then
            local public_ip
            public_ip=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].PublicIpAddress // "N/A"')
            local private_ip
            private_ip=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].PrivateIpAddress // "N/A"')
            local instance_type
            instance_type=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].InstanceType')
            local key_name
            key_name=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].KeyName // "N/A"')
            
            echo ""
            echo "=== Connection Information ==="
            echo "Public IP: $public_ip"
            echo "Private IP: $private_ip"
            echo "Instance Type: $instance_type"
            echo ""
            
            if [[ "$public_ip" != "N/A" && -n "$key_name" ]]; then
                echo "SSH Command:"
                echo "  ssh -i ~/.ssh/${key_name}.pem ec2-user@${public_ip}"
            fi
        fi
    else
        echo "Instance did not reach running state within ${max_wait} seconds."
        echo "You can check the instance status manually."
    fi
    
    echo ""
}

# -----------------------------------------------------------------------------
# Full workflow: Create EC2 instance from AMI
# -----------------------------------------------------------------------------
create_instance_from_ami() {
    # Pick an AMI
    if ! pick_ami; then
        return 1
    fi
    
    # Collect parameters
    if ! collect_instance_params "$PICKED_AMI"; then
        return 1
    fi
    
    # Confirm and launch
    if ! confirm_and_launch; then
        return 1
    fi
    
    # Post-launch actions
    post_launch_actions
    
    return 0
}

# =============================================================================
# PHASE 5: CREATE AMI FROM EC2 INSTANCE
# =============================================================================

# -----------------------------------------------------------------------------
# Estimate AMI creation time from total attached volume size.
# Ported from ami_copy.bash's estimate_time() (PLAN.md Phase 5b / DECISIONS.md
# "AMI-from-instance: fold ami_copy.bash capabilities into Phase 5").
# Arguments:
#   $1: total volume size in GB
# Returns:
#   Human-readable time estimate string
# -----------------------------------------------------------------------------
estimate_ami_creation_time() {
    local gb="$1"
    if   (( gb < 20  )); then echo "5–15 minutes"
    elif (( gb < 100 )); then echo "15–45 minutes"
    elif (( gb < 200 )); then echo "45–90 minutes"
    else                       echo "1.5–3+ hours"
    fi
}

# -----------------------------------------------------------------------------
# Gather attached EBS volume info for an instance and aggregate size.
# Ported from ami_copy.bash's volume-gathering loop (PLAN.md Phase 5b).
# Arguments:
#   $1: Instance ID
#   $2: Region
# Returns:
#   JSON object: {Volumes: [...], TotalGB: N, HasPriorSnapshot: bool}
# -----------------------------------------------------------------------------
gather_volume_info() {
    local instance_id="$1"
    local region="$2"

    local response
    response=$(AWS_REGION="$region" aws_ec2 describe-volumes \
        --filters "Name=attachment.instance-id,Values=${instance_id}" 2>/dev/null)

    if [[ -z "$response" ]]; then
        echo '{"Volumes":[],"TotalGB":0,"HasPriorSnapshot":false}'
        return
    fi

    echo "$response" | jq '
        {
            Volumes: [.Volumes[]? | {VolumeId, Size, VolumeType, SnapshotId}],
            TotalGB: ([.Volumes[]?.Size] | add // 0),
            HasPriorSnapshot: ([.Volumes[]? | select(.SnapshotId != null)] | length > 0)
        }
    '
}

# -----------------------------------------------------------------------------
# Check SSM availability for an instance.
# Ported from ami_copy.bash's ssm_ping check (PLAN.md Phase 5b).
# Arguments:
#   $1: Instance ID
#   $2: Region
# Returns:
#   Echoes the SSM PingStatus (e.g. "Online"), or "None" if unavailable
# -----------------------------------------------------------------------------
check_ssm_availability() {
    local instance_id="$1"
    local region="$2"

    local response
    response=$(AWS_REGION="$region" aws_ssm describe-instance-information \
        --filters "Key=InstanceIds,Values=${instance_id}" 2>/dev/null)

    if [[ -z "$response" ]]; then
        echo "None"
        return
    fi

    echo "$response" | jq -r '.InstanceInformationList[0].PingStatus // "None"'
}

# -----------------------------------------------------------------------------
# Run fstrim on an instance via SSM and poll until the command completes.
# Ported from ami_copy.bash's fstrim send-command/poll loop (PLAN.md Phase 5b).
# Arguments:
#   $1: Instance ID
#   $2: Region
# Returns:
#   0 and prints fstrim output on Success
#   1 on Failed/Cancelled/TimedOut, or if the command could not be sent
# Environment:
#   SSM_POLL_INTERVAL: seconds between polls (default 10; tests set this to 0)
# -----------------------------------------------------------------------------
run_fstrim_via_ssm() {
    local instance_id="$1"
    local region="$2"
    local poll_interval="${SSM_POLL_INTERVAL:-10}"

    local send_response command_id
    send_response=$(AWS_REGION="$region" aws_ssm send-command \
        --instance-ids "$instance_id" \
        --document-name "AWS-RunShellScript" \
        --parameters 'commands=["sudo fstrim -av"]' 2>/dev/null)
    command_id=$(echo "$send_response" | jq -r '.Command.CommandId // empty')

    if [[ -z "$command_id" ]]; then
        echo "ERROR: Failed to send fstrim command via SSM." >&2
        return 1
    fi

    echo "  fstrim command sent (Command ID: ${command_id})"

    while true; do
        local invocation status
        invocation=$(AWS_REGION="$region" aws_ssm get-command-invocation \
            --command-id "$command_id" --instance-id "$instance_id" 2>/dev/null)
        status=$(echo "$invocation" | jq -r '.Status // "Pending"')

        case "$status" in
            Success)
                echo "  fstrim completed:"
                echo "$invocation" | jq -r '.StandardOutputContent // ""' | sed 's/^/    /'
                return 0
                ;;
            Failed|Cancelled|TimedOut)
                echo "  Warning: fstrim did not complete (status: ${status})."
                echo "  The snapshot will proceed but may copy more blocks than necessary."
                return 1
                ;;
            *)
                sleep "$poll_interval"
                ;;
        esac
    done
}

# -----------------------------------------------------------------------------
# Offer to run fstrim via SSM before snapshotting, or confirm proceeding
# without it. Ported from ami_copy.bash (PLAN.md Phase 5b).
# Arguments:
#   $1: Instance ID
#   $2: Region
# Returns:
#   0 to proceed with AMI creation, 1 if the user cancels
# -----------------------------------------------------------------------------
offer_fstrim_before_snapshot() {
    local instance_id="$1"
    local region="$2"

    local ssm_status
    ssm_status=$(check_ssm_availability "$instance_id" "$region")

    if [[ "$ssm_status" == "Online" ]]; then
        echo ""
        echo "SSM is available on this instance."
        echo "Running fstrim before snapshotting tells the EBS volume which blocks"
        echo "are free, so the snapshot skips them. This can reduce copy time"
        echo "significantly on instances with Docker churn or deleted data."
        echo ""
        echo -n "Run fstrim via SSM before snapshotting? (y/N): "
        local run_fstrim
        read -r run_fstrim
        if [[ "$run_fstrim" == "y" || "$run_fstrim" == "Y" ]]; then
            echo ""
            run_fstrim_via_ssm "$instance_id" "$region"
        fi
        return 0
    fi

    echo ""
    echo "SSM is not available on this instance (status: ${ssm_status})."
    echo "fstrim cannot be run automatically. The snapshot will proceed without it,"
    echo "which may result in a larger snapshot and longer copy time."
    echo ""
    echo -n "Continue without fstrim? (y/N): "
    local skip_fstrim
    read -r skip_fstrim
    if [[ "$skip_fstrim" != "y" && "$skip_fstrim" != "Y" ]]; then
        echo "Cancelled."
        return 1
    fi
    return 0
}

# -----------------------------------------------------------------------------
# Select an EC2 instance for AMI creation
# Validates that the instance is in a valid state (running or stopped)
# Sets PICKED_INSTANCE global variable
# Returns:
#   0 on success, PICKED_INSTANCE contains the selected instance JSON
#   1 on cancel/error
# -----------------------------------------------------------------------------
select_instance_for_ami() {
    echo ""
    echo "=== Create AMI from EC2 Instance ==="
    echo ""
    
    # List instances and allow user to pick
    if ! pick_instance; then
        return 1
    fi
    
    # Validate instance state
    local instance_state
    instance_state=$(echo "$PICKED_INSTANCE" | jq -r '.State.Name')
    
    if [[ "$instance_state" == "terminated" || "$instance_state" == "terminating" ]]; then
        echo "ERROR: Cannot create AMI from a $instance_state instance."
        return 1
    fi
    
    # Warn if instance is running.
    # Ported from ami_copy_basic_steps.md's crash-consistency guidance
    # (PLAN.md Phase 5b) -- AMI creation uses --no-reboot, so the result is
    # crash-consistent rather than application-consistent.
    if [[ "$instance_state" == "running" ]]; then
        echo "WARNING: Creating an AMI from a running instance produces a"
        echo "crash-consistent snapshot (equivalent to pulling the power cord at"
        echo "the snapshot moment), not an application-consistent one:"
        echo "  - PostgreSQL replays its WAL automatically on first boot"
        echo "  - OpenSearch replays its transaction log on first boot"
        echo "  - Redis session/cache data may be lost (ephemeral by design)"
        echo "  - Docker container images on disk are unaffected"
        echo "This is acceptable for upgrade testing. Stop the instance first if"
        echo "you need a fully consistent snapshot."
        echo ""
        echo -n "Continue with running instance? (y/N): "
        local confirm
        read -r confirm

        if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
            echo "Cancelled."
            return 1
        fi
    fi
    
    # Display selected instance details
    echo ""
    echo "Selected Instance:"
    echo "  ID: $(echo "$PICKED_INSTANCE" | jq -r '.InstanceId')"
    echo "  Type: $(echo "$PICKED_INSTANCE" | jq -r '.InstanceType')"
    echo "  State: $instance_state"
    echo "  Region: $(echo "$PICKED_INSTANCE" | jq -r '.Region')"
    echo ""

    # Volume size summary and AMI creation time estimate.
    # Ported from ami_copy.bash (PLAN.md Phase 5b).
    local instance_id region
    instance_id=$(echo "$PICKED_INSTANCE" | jq -r '.InstanceId')
    region=$(echo "$PICKED_INSTANCE" | jq -r '.Region')

    local volume_info total_gb has_prior_snapshot
    volume_info=$(gather_volume_info "$instance_id" "$region")
    total_gb=$(echo "$volume_info" | jq -r '.TotalGB')
    has_prior_snapshot=$(echo "$volume_info" | jq -r '.HasPriorSnapshot')

    echo "Attached volumes:"
    echo "$volume_info" | jq -r '.Volumes[] | "  \(.VolumeId)  \(.Size) GB  \(.VolumeType)"'
    echo "  Total volume size  : ${total_gb} GB"
    echo "  Estimated copy time: $(estimate_ami_creation_time "$total_gb")"

    if [[ "$has_prior_snapshot" == "true" ]]; then
        echo ""
        echo "  Note: one or more volumes have a prior snapshot — only changed blocks"
        echo "        will be copied. Actual time may be significantly shorter."
    fi
    echo ""

    # Offer to fstrim via SSM before snapshotting. Ported from ami_copy.bash
    # (PLAN.md Phase 5b).
    if ! offer_fstrim_before_snapshot "$instance_id" "$region"; then
        return 1
    fi

    return 0
}

# -----------------------------------------------------------------------------
# Collect parameters for creating an AMI from an instance
# Sets AMI_CREATION_PARAMS global variable with JSON parameters
# Returns:
#   0 on success
#   1 on cancel/error
# -----------------------------------------------------------------------------
collect_ami_params() {
    local instance_json="$1"
    local instance_id
    instance_id=$(echo "$instance_json" | jq -r '.InstanceId')
    local instance_state
    instance_state=$(echo "$instance_json" | jq -r '.State.Name')
    local region
    region=$(echo "$instance_json" | jq -r '.Region')
    
    echo ""
    echo "=== AMI Creation Parameters ==="
    echo "Source Instance: $instance_id (State: $instance_state, Region: $region)"
    echo ""
    
    # AMI name (required) — suggest a default based on the instance name/ID and today's date
    local instance_name
    instance_name=$(echo "$instance_json" | jq -r '.Name // ""')
    local name_base="${instance_name:-$instance_id}"
    name_base=$(echo "$name_base" | tr -cs 'a-zA-Z0-9._()/-' '-')
    name_base="${name_base%-}"
    local default_ami_name
    default_ami_name="${name_base}-copy-$(date +%Y-%m-%d)"

    local ami_name=""
    while [[ -z "$ami_name" ]]; do
        echo -n "AMI Name (required, 3-128 chars, alphanumeric + -_./()) [${default_ami_name}]: "
        read -r ami_name

        if [[ -z "$ami_name" ]]; then
            ami_name="$default_ami_name"
        fi

        # Validate AMI name
        if [[ ${#ami_name} -lt 3 || ${#ami_name} -gt 128 ]]; then
            echo "ERROR: AMI name must be between 3 and 128 characters."
            ami_name=""
            continue
        fi
        
        if ! echo "$ami_name" | LC_ALL=C grep -qE '^[a-zA-Z0-9._()/-]+$'; then
            echo "ERROR: AMI name contains invalid characters. Allowed: alphanumeric, -_.()/"
            ami_name=""
            continue
        fi
    done
    
    # AMI description (optional)
    echo -n "AMI Description (optional, press Enter to skip): "
    local ami_description
    read -r ami_description
    
    # No-reboot flag (only for running instances)
    local no_reboot=false
    if [[ "$instance_state" == "running" ]]; then
        echo -n "Skip reboot after AMI creation? (y/N): "
        local reboot_input
        read -r reboot_input
        
        if [[ "$reboot_input" == "y" || "$reboot_input" == "Y" ]]; then
            no_reboot=true
        fi
    fi
    
    # Tags (optional)
    echo ""
    echo "Enter tags for the AMI (optional):"
    echo "  Format: Key1=Value1,Key2=Value2"
    echo -n "  Tags: "
    local tags_input
    read -r tags_input
    
    # Build tags JSON
    local tags_json="{}"
    if [[ -n "$tags_input" ]]; then
        IFS=',' read -ra tag_pairs <<< "$tags_input"
        for tag_pair in "${tag_pairs[@]}"; do
            if [[ -n "$tag_pair" ]]; then
                local key="${tag_pair%%=*}"
                local value="${tag_pair#*=}"
                tags_json=$(echo "$tags_json" | jq --arg k "$key" --arg v "$value" '. + {($k): $v}')
            fi
        done
    fi
    
    # Build the final parameters JSON
    AMI_CREATION_PARAMS=$(jq -n \
        --arg in "$instance_id" \
        --arg name "$ami_name" \
        --arg desc "$ami_description" \
        --argjson nr "$no_reboot" \
        --argjson tags "$tags_json" \
        --arg region "$region" \
        '{
            InstanceId: $in,
            Name: $name,
            Description: $desc,
            NoReboot: $nr,
            TagSpecifications: [{
                ResourceType: "image",
                Tags: $tags
            }],
            Region: $region
        }')
    
    return 0
}

# -----------------------------------------------------------------------------
# Create AMI from an EC2 instance
# Uses PICKED_INSTANCE and AMI_CREATION_PARAMS global variables
# Returns:
#   The new ImageId via CREATED_AMI_ID variable
#   Or returns 1 on error
# -----------------------------------------------------------------------------
create_ami_from_instance() {
    if [[ -z "$PICKED_INSTANCE" ]]; then
        echo "ERROR: No instance selected for AMI creation."
        return 1
    fi
    
    if [[ -z "$AMI_CREATION_PARAMS" ]]; then
        echo "ERROR: No AMI creation parameters collected."
        return 1
    fi
    
    echo ""
    echo "=== Creating AMI ==="
    echo ""
    
    local instance_id
    instance_id=$(echo "$AMI_CREATION_PARAMS" | jq -r '.InstanceId')
    local ami_name
    ami_name=$(echo "$AMI_CREATION_PARAMS" | jq -r '.Name')
    local region
    region=$(echo "$AMI_CREATION_PARAMS" | jq -r '.Region')
    
    echo "Creating AMI from instance $instance_id..."
    echo "AMI Name: $ami_name"
    echo ""
    
    # Build the AWS CLI command
    local cmd_args=("create-image")
    cmd_args+=("--instance-id" "$instance_id")
    cmd_args+=("--name" "$ami_name")
    
    local description
    description=$(echo "$AMI_CREATION_PARAMS" | jq -r '.Description')
    if [[ -n "$description" && "$description" != "null" ]]; then
        cmd_args+=("--description" "$description")
    fi
    
    local no_reboot
    no_reboot=$(echo "$AMI_CREATION_PARAMS" | jq -r '.NoReboot')
    if [[ "$no_reboot" == "true" ]]; then
        cmd_args+=("--no-reboot")
    fi
    
    # Add tags
    local tags_json
    tags_json=$(echo "$AMI_CREATION_PARAMS" | jq -c '.TagSpecifications[0].Tags')
    if [[ "$tags_json" != "{}" && "$tags_json" != "null" ]]; then
        # Convert tags JSON to AWS CLI format
        local tags_args=()
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            local key="${line%%=*}"
            local value="${line#*=}"
            tags_args+=("Key=${key},Value=${value}")
        done < <(echo "$tags_json" | jq -r 'to_entries | map("key=\u0027" + .key + "\u0027,value=\u0027" + .value + "\u0027") | join("\n")')
        
        if [[ ${#tags_args[@]} -gt 0 ]]; then
            cmd_args+=("--tag-specifications" "ResourceType=image,Tags=[${tags_args[*]}]")
        fi
    fi
    
    # Execute the command
    local response
    if ! response=$(AWS_REGION="$region" aws_ec2 "${cmd_args[@]}" 2>&1); then
        echo "ERROR: Failed to create AMI: $response"
        return 1
    fi
    
    # Extract the new AMI ID
    CREATED_AMI_ID=$(echo "$response" | jq -r '.ImageId')
    
    if [[ -z "$CREATED_AMI_ID" ]]; then
        echo "ERROR: No AMI ID returned from AWS."
        return 1
    fi
    
    echo "AMI created successfully!"
    echo "AMI ID: $CREATED_AMI_ID"
    
    return 0
}

# -----------------------------------------------------------------------------
# Format a duration in seconds as "Xh MMm SSs".
# Ported from ami_copy.bash's elapsed() (PLAN.md Phase 5b).
# Arguments:
#   $1: duration in seconds
# -----------------------------------------------------------------------------
format_elapsed() {
    local total="$1"
    printf "%dh %02dm %02ds" $(( total/3600 )) $(( (total%3600)/60 )) $(( total%60 ))
}

# -----------------------------------------------------------------------------
# Wait for AMI to reach available state and display details
# Uses CREATED_AMI_ID global variable
# -----------------------------------------------------------------------------
post_ami_creation_actions() {
    if [[ -z "$CREATED_AMI_ID" ]]; then
        echo "ERROR: No AMI ID for post-creation actions."
        return 1
    fi

    local region
    region=$(echo "$AMI_CREATION_PARAMS" | jq -r '.Region')
    local poll_interval="${AMI_POLL_INTERVAL:-30}"
    local ami_state="pending"
    local elapsed=0

    echo ""
    echo "Waiting for AMI to be available."
    echo "Polling every ${poll_interval} seconds. Large volumes (Docker images +"
    echo "database + OpenSearch) may take 20-60+ minutes."
    echo "Ctrl-C to stop polling -- creation will continue in AWS regardless."
    echo ""

    # Unbounded polling: AMI creation has no fixed deadline (PLAN.md Phase 5b
    # / DECISIONS.md "AMI-from-instance: fold ami_copy.bash capabilities into
    # Phase 5" -- a 600s timeout gave up before real Invenio RDM AMIs finish).
    while true; do
        local state_response
        state_response=$(AWS_REGION="$region" aws_ec2 describe-images --image-ids "$CREATED_AMI_ID" 2>/dev/null)

        if [[ -n "$state_response" ]]; then
            ami_state=$(echo "$state_response" | jq -r '.Images[0].State // "unknown"')
        fi

        echo "  [elapsed: $(format_elapsed "$elapsed")]  $CREATED_AMI_ID  state: $ami_state"

        if [[ "$ami_state" == "available" ]]; then
            break
        fi

        if [[ "$ami_state" == "failed" ]]; then
            echo ""
            echo "ERROR: AMI creation failed (state: failed) after $(format_elapsed "$elapsed")."
            echo "Check the AWS Console for details."
            return 1
        fi

        sleep "$poll_interval"
        elapsed=$((elapsed + poll_interval))
    done

    echo ""
    echo "AMI is now available! Total time: $(format_elapsed "$elapsed")"

    # Get AMI details
    local details_response
    details_response=$(AWS_REGION="$region" aws_ec2 describe-images --image-ids "$CREATED_AMI_ID" 2>/dev/null)

    if [[ -n "$details_response" ]]; then
        local ami_name
        ami_name=$(echo "$details_response" | jq -r '.Images[0].Name')
        local creation_date
        creation_date=$(echo "$details_response" | jq -r '.Images[0].CreationDate')

        echo ""
        echo "=== AMI Details ==="
        echo "AMI ID: $CREATED_AMI_ID"
        echo "Name: $ami_name"
        echo "State: $ami_state"
        echo "Creation Date: $creation_date"
        echo "Region: $region"
        echo ""
    fi
}

# -----------------------------------------------------------------------------
# Full workflow: Create AMI from EC2 Instance
# -----------------------------------------------------------------------------
create_ami_from_instance_workflow() {
    # Select instance
    if ! select_instance_for_ami; then
        return 1
    fi
    
    # Collect AMI parameters
    if ! collect_ami_params "$PICKED_INSTANCE"; then
        return 1
    fi
    
    # Create the AMI
    if ! create_ami_from_instance; then
        return 1
    fi
    
    # Post-creation actions
    post_ami_creation_actions
    
    return 0
}

# =============================================================================
# PHASE 6: REMOVE AMI
# =============================================================================

# -----------------------------------------------------------------------------
# Select an AMI for removal
# Sets PICKED_AMI global variable
# Returns:
#   0 on success, PICKED_AMI contains the selected AMI JSON
#   1 on cancel/error
# -----------------------------------------------------------------------------
select_ami_for_removal() {
    echo ""
    echo "=== Remove AMI ==="
    echo ""
    
    # List AMIs and allow user to pick
    if ! pick_ami; then
        return 1
    fi
    
    # Display selected AMI details
    echo ""
    echo "Selected AMI for removal:"
    echo "  ID: $(echo "$PICKED_AMI" | jq -r '.ImageId')"
    echo "  Name: $(echo "$PICKED_AMI" | jq -r '.Name')"
    echo "  Creation Date: $(echo "$PICKED_AMI" | jq -r '.CreationDate')"
    echo "  Region: $(echo "$PICKED_AMI" | jq -r '.Region')"
    echo ""
    
    return 0
}

# -----------------------------------------------------------------------------
# Display dry run information for AMI removal
# Uses PICKED_AMI global variable
# Returns:
#   0 to proceed to dependency check
#   1 to cancel
# -----------------------------------------------------------------------------
show_removal_dry_run() {
    if [[ -z "$PICKED_AMI" ]]; then
        echo "ERROR: No AMI selected for removal."
        return 1
    fi
    
    local ami_id
    ami_id=$(echo "$PICKED_AMI" | jq -r '.ImageId')
    local ami_name
    ami_name=$(echo "$PICKED_AMI" | jq -r '.Name')
    local region
    region=$(echo "$PICKED_AMI" | jq -r '.Region')
    
    echo ""
    echo "=== DRY RUN: AMI Removal ==="
    echo ""
    echo "The following AMI will be permanently deleted:"
    echo ""
    echo "  AMI ID: $ami_id"
    echo "  Name: $ami_name"
    echo "  Region: $region"
    echo ""
    echo "This action CANNOT be undone!"
    echo ""
    echo -n "Proceed to dependency check? (y/N): "
    local confirm
    read -r confirm
    
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
        echo "Cancelled."
        return 1
    fi
    
    return 0
}

# -----------------------------------------------------------------------------
# Check for dependencies before removing an AMI
# Uses PICKED_AMI global variable
# Returns:
#   0 to proceed with removal
#   1 to cancel
# -----------------------------------------------------------------------------
check_ami_dependencies() {
    if [[ -z "$PICKED_AMI" ]]; then
        echo "ERROR: No AMI selected for dependency check."
        return 1
    fi
    
    local ami_id
    ami_id=$(echo "$PICKED_AMI" | jq -r '.ImageId')
    local ami_region
    ami_region=$(echo "$PICKED_AMI" | jq -r '.Region')
    
    echo ""
    echo "=== Checking for Dependencies ==="
    echo ""
    echo "Checking all regions for instances using AMI: $ami_id"
    echo ""
    
    local has_dependencies=false
    local dependency_message=""
    
    # Check all configured regions
    for region in "${REGIONS[@]}"; do
        local response
        response=$(AWS_REGION="$region" aws_ec2 describe-instances \
            --filters "Name=image-id,Values=$ami_id" 2>/dev/null)
        
        if [[ -n "$response" ]]; then
            local instance_count
            instance_count=$(echo "$response" | jq '.Reservations | length')
            
            if [[ "$instance_count" -gt 0 ]]; then
                has_dependencies=true
                
                # Get instance details
                local instances_info
                instances_info=$(echo "$response" | jq -r '
                    .Reservations[] | 
                    .Instances[] | 
                    "  - Region: " + .Placement.AvailabilityZone + 
                    " Instance: " + .InstanceId + 
                    " State: " + .State.Name + 
                    "\n"
                ')
                
                if [[ -n "$instances_info" ]]; then
                    dependency_message+="\n$region:\n$instances_info"
                fi
            fi
        fi
    done
    
    if [[ "$has_dependencies" == true ]]; then
        echo "WARNING: The following instances are using this AMI:"
        echo -e "$dependency_message"
        echo ""
        echo "Removing this AMI will cause these instances to fail on reboot."
        echo "You should update these instances to use a different AMI first."
        echo ""
        echo -n "Continue with removal anyway? (y/N): "
        local confirm
        read -r confirm
        
        if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
            echo "Cancelled."
            return 1
        fi
    else
        echo "No instances currently using this AMI."
    fi
    
    echo ""
    return 0
}

# -----------------------------------------------------------------------------
# Type-to-confirm for destructive actions
# Arguments:
#   $1: Resource identifier to confirm (e.g., AMI ID)
#   $2: Resource type description (e.g., "AMI")
#   $3: Action description (e.g., "remove")
# Returns:
#   0 on successful confirmation
#   1 on mismatch or cancel
# -----------------------------------------------------------------------------
type_to_confirm() {
    local resource_id="$1"
    local resource_type="$2"
    local action="$3"
    
    if [[ -z "$resource_id" ]]; then
        echo "ERROR: No resource identifier provided for confirmation."
        return 1
    fi
    
    echo ""
    echo "=== CONFIRMATION REQUIRED ==="
    echo ""
    echo "To $action this $resource_type, type the exact identifier:"
    echo "  $resource_id"
    echo ""
    echo -n "Enter identifier: "
    local input
    read -r input
    
    if [[ "$input" != "$resource_id" ]]; then
        echo "ERROR: Input does not match. Aborting."
        return 1
    fi
    
    return 0
}

# -----------------------------------------------------------------------------
# Remove an AMI after all confirmations
# Uses PICKED_AMI global variable
# Returns:
#   0 on success
#   1 on error
# -----------------------------------------------------------------------------
remove_ami() {
    if [[ -z "$PICKED_AMI" ]]; then
        echo "ERROR: No AMI selected for removal."
        return 1
    fi
    
    local ami_id
    ami_id=$(echo "$PICKED_AMI" | jq -r '.ImageId')
    local region
    region=$(echo "$PICKED_AMI" | jq -r '.Region')
    local ami_name
    ami_name=$(echo "$PICKED_AMI" | jq -r '.Name')
    
    echo ""
    echo "=== Removing AMI ==="
    echo ""
    echo "Removing AMI: $ami_id ($ami_name)"
    echo "Region: $region"
    echo ""
    
    # Execute the removal
    local response
    if ! response=$(AWS_REGION="$region" aws_ec2 deregister-image --image-id "$ami_id" 2>&1); then
        echo "ERROR: Failed to remove AMI: $response"
        return 1
    fi
    
    # Check if removal was successful
    local success
    success=$(echo "$response" | jq -r '.Success // "false"')
    
    if [[ "$success" != "true" ]]; then
        echo "ERROR: AMI removal was not successful."
        return 1
    fi
    
    echo "AMI removed successfully!"
    echo "AMI ID: $ami_id"
    echo "Name: $ami_name"
    echo ""
    
    return 0
}

# -----------------------------------------------------------------------------
# Full workflow: Remove AMI
# -----------------------------------------------------------------------------
remove_ami_workflow() {
    # Select AMI for removal
    if ! select_ami_for_removal; then
        return 1
    fi
    
    # Show dry run
    if ! show_removal_dry_run; then
        return 1
    fi
    
    # Check dependencies
    if ! check_ami_dependencies; then
        return 1
    fi
    
    # Type-to-confirm
    local ami_id
    ami_id=$(echo "$PICKED_AMI" | jq -r '.ImageId')
    if ! type_to_confirm "$ami_id" "AMI" "remove"; then
        return 1
    fi
    
    # Execute removal
    if ! remove_ami; then
        return 1
    fi
    
    return 0
}

# =============================================================================
# PHASE 7: MAIN MENU AND INTEGRATION
# =============================================================================

# -----------------------------------------------------------------------------
# Display header, current EC2 instances/AMIs, and menu options, then prompt
# for a selection until a valid one (1-5) is entered.
# Sets MENU_CHOICE global variable to the validated selection.
# -----------------------------------------------------------------------------
show_main_menu() {
    echo "=============================================================================="
    echo "$SCRIPT_NAME"
    echo "Regions: ${REGIONS[*]}"
    echo "=============================================================================="
    echo ""

    display_instances "$(list_ec2_instances)"
    display_amis "$(list_amis)"

    echo "===== MAIN MENU ====="
    echo ""
    echo "  1) Create EC2 instance from AMI"
    echo "  2) Create AMI from EC2 instance"
    echo "  3) Remove AMI"
    echo "  4) Refresh resource lists"
    echo "  5) Exit"
    echo ""

    local choice
    while true; do
        echo -n "Select an option [1-5]: "
        read -r choice
        case "$choice" in
            1|2|3|4|5)
                MENU_CHOICE="$choice"
                return 0
                ;;
            *)
                echo "Invalid selection: '$choice'. Please enter a number from 1 to 5."
                ;;
        esac
    done
}

# =============================================================================
# MAIN SCRIPT
# =============================================================================

main() {
    # Check dependencies
    check_dependencies
    echo ""

    # Ctrl+C exits cleanly instead of dumping a mid-workflow stack trace
    trap 'echo ""; echo "Interrupted. Exiting."; exit 130' INT

    while true; do
        show_main_menu

        case "$MENU_CHOICE" in
            1)
                create_instance_from_ami || echo "Create-instance workflow ended without launching an instance."
                ;;
            2)
                create_ami_from_instance_workflow || echo "Create-AMI workflow ended without creating an AMI."
                ;;
            3)
                remove_ami_workflow || echo "Remove-AMI workflow ended without removing an AMI."
                ;;
            4)
                # No-op: show_main_menu refreshes resource lists every iteration
                ;;
            5)
                echo "Exiting."
                exit 0
                ;;
        esac
        echo ""
    done
}

# Run main if script is executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
