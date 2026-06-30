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
INSTANCE_CACHE=""
AMI_CACHE=""

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
    printf "%-20s %-25s %-20s %s\n" "AMI ID" "NAME" "CREATION DATE" "REGION"
    echo "---------------------------------------------------------------------------------------"
    
    echo "$amis" | jq -r '.[] | 
        "\(.ImageId // "")\t\(.Name // "")\t\(.CreationDate // "")\t\(.Region // "")"
    ' | while IFS=$'\t' read -r ami_id name creation_date region; do
        printf "%-20s %-25s %-20s %s\n" "${ami_id:0:20}" "${name:0:25}" "${creation_date:0:19}" "$region"
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
    eval "items=(\"\${$arr_name}[@]\"\")"
    
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
    
    show_pick_list ami_items "Select an AMI:"
    if [[ $? -ne 0 ]]; then
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
    
    show_pick_list instance_items "Select an EC2 instance:"
    if [[ $? -ne 0 ]]; then
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
    response=$(aws_ec2 describe-iam-instance-profile-associations 2>/dev/null)
    
    if [[ -z "$response" ]]; then
        # Try alternative approach
        response=$(aws iam list-instance-profiles 2>/dev/null)
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
    local ami_id=$(echo "$INSTANCE_PARAMS" | jq -r '.ImageId')
    local instance_type=$(echo "$INSTANCE_PARAMS" | jq -r '.InstanceType')
    local key_name=$(echo "$INSTANCE_PARAMS" | jq -r '.KeyName // "(none)"')
    local sg_ids=$(echo "$INSTANCE_PARAMS" | jq -r '.SecurityGroupIds | join(", ") // "(none)"')
    local subnet_id=$(echo "$INSTANCE_PARAMS" | jq -r '.SubnetId // "(none)"')
    local profile_arn=$(echo "$INSTANCE_PARAMS" | jq -r '.IamInstanceProfile.Arn // "(none)"')
    local user_data=$(echo "$INSTANCE_PARAMS" | jq -r '.UserData // "(none)"')
    local tags=$(echo "$INSTANCE_PARAMS" | jq -r '.TagSpecifications[0].Tags // {} | to_entries | map("\n  \(.key): \(.value)") | join("")')
    
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
    response=$(AWS_REGION="$region" aws_ec2 run-instances "$(echo "$INSTANCE_PARAMS" | jq -c '.')" 2>&1)
    
    if [[ $? -ne 0 ]]; then
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
            local public_ip=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].PublicIpAddress // "N/A"')
            local private_ip=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].PrivateIpAddress // "N/A"')
            local instance_type=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].InstanceType')
            local key_name=$(echo "$details_response" | jq -r '.Reservations[0].Instances[0].KeyName // "N/A"')
            
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
    pick_ami
    if [[ $? -ne 0 ]]; then
        return 1
    fi
    
    # Collect parameters
    collect_instance_params "$PICKED_AMI"
    if [[ $? -ne 0 ]]; then
        return 1
    fi
    
    # Confirm and launch
    confirm_and_launch
    if [[ $? -ne 0 ]]; then
        return 1
    fi
    
    # Post-launch actions
    post_launch_actions
    
    return 0
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
