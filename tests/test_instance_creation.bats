#!/usr/bin/env bats
# BATS tests for EC2 instance creation from AMI (Phase 4)

load '/Users/rsdoiel/WorkLab/aws_tools/tests/lib/test_helper'

setup() {
    setup_mock_aws
    source_main_script
}

teardown() {
    cleanup_mock_aws
}

# =============================================================================
# Test Helper Functions for Parameter Collection
# =============================================================================

@test "list_instance_types returns instance types for a region" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-types"; then
    echo '{"InstanceTypes":[{"InstanceType":"t2.micro","CurrentGeneration":true,"MemoryInfo":{"SizeInMiB":1024},"VCpuInfo":{"DefaultVCpus":1}},{"InstanceType":"t2.small","CurrentGeneration":true,"MemoryInfo":{"SizeInMiB":2048},"VCpuInfo":{"DefaultVCpus":1}}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_instance_types "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"t2.micro"* ]]
    [[ "$output" == *"t2.small"* ]]
}

@test "list_instance_types filters to current generation" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-types"; then
    echo '{"InstanceTypes":[{"InstanceType":"t2.micro","CurrentGeneration":true},{"InstanceType":"t1.micro","CurrentGeneration":false}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_instance_types "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"t2.micro"* ]]
    [[ "$output" != *"t1.micro"* ]]
}

@test "list_instance_types returns empty array on error" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-types"; then
    echo "" >&2
    exit 1
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_instance_types "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == "[]" ]]
}

@test "list_key_pairs returns key pair names for a region" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-key-pairs"; then
    echo '{"KeyPairs":[{"KeyName":"my-key"},{"KeyName":"backup-key"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_key_pairs "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"my-key"* ]]
    [[ "$output" == *"backup-key"* ]]
}

@test "list_key_pairs returns empty array when no key pairs exist" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-key-pairs"; then
    echo '{"KeyPairs":[]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_key_pairs "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == "[]" ]]
}

@test "list_security_groups returns security groups for a region" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-security-groups"; then
    echo '{"SecurityGroups":[{"GroupId":"sg-123","GroupName":"default"},{"GroupId":"sg-456","GroupName":"web-sg"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_security_groups "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"sg-123"* ]]
    [[ "$output" == *"sg-456"* ]]
}

@test "list_subnets returns subnets for a region" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-subnets"; then
    echo '{"Subnets":[{"SubnetId":"subnet-123","CidrBlock":"10.0.1.0/24","AvailabilityZone":"us-east-1a"},{"SubnetId":"subnet-456","CidrBlock":"10.0.2.0/24","AvailabilityZone":"us-east-1b"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_subnets "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"subnet-123"* ]]
    [[ "$output" == *"subnet-456"* ]]
}

@test "list_instance_profiles returns IAM instance profiles" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "list-instance-profiles"; then
    echo '{"InstanceProfiles":[{"InstanceProfileName":"my-profile","Arn":"arn:aws:iam::123456789012:instance-profile/my-profile"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_instance_profiles
    [ "$status" -eq 0 ]
    [[ "$output" == *"my-profile"* ]]
}

# =============================================================================
# Test Parameter Collection Function
# =============================================================================

@test "collect_instance_params builds valid JSON with minimal parameters" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-types"; then
    echo '{"InstanceTypes":[]}'
    exit 0
elif echo "$CMD" | grep -q "describe-key-pairs"; then
    echo '{"KeyPairs":[]}'
    exit 0
elif echo "$CMD" | grep -q "describe-security-groups"; then
    echo '{"SecurityGroups":[]}'
    exit 0
elif echo "$CMD" | grep -q "describe-subnets"; then
    echo '{"Subnets":[]}'
    exit 0
elif echo "$CMD" | grep -q "list-instance-profiles-for-association"; then
    echo '{"InstanceProfiles":[]}'
    exit 0
elif echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    
    # Mock AMI JSON
    # shellcheck disable=SC2034
    local ami_json='{"ImageId":"ami-12345678","Name":"test-ami","Region":"us-east-1"}'
    
    # We need to mock stdin for the read commands
    # For now, we'll test that the function doesn't crash with empty inputs
    # Full interactive testing would require more complex mocking
    skip "Interactive parameter collection requires stdin mocking - TODO"
}

# =============================================================================
# Test Confirmation and Launch
# =============================================================================

@test "confirm_and_launch succeeds with valid parameters" {
    skip "Interactive confirmation requires stdin mocking - TODO"
}

@test "confirm_and_launch fails when INSTANCE_PARAMS is empty" {
    # shellcheck disable=SC2030,SC2031
    export INSTANCE_PARAMS=""
    run confirm_and_launch
    [ "$status" -eq 1 ]
    [[ "$output" == *"ERROR: No instance parameters collected"* ]]
}

# =============================================================================
# Test Full Workflow
# =============================================================================

@test "create_instance_from_ami workflow orchestrates correctly" {
    # This tests the overall workflow structure
    # Mock all the helper functions
    
    # Mock pick_ami to set PICKED_AMI
    pick_ami() {
        export PICKED_AMI='{"ImageId":"ami-12345","Name":"test-ami","Region":"us-east-1"}'
        return 0
    }
    
    # Mock collect_instance_params to set INSTANCE_PARAMS
    collect_instance_params() {
        # shellcheck disable=SC2030,SC2031
        export INSTANCE_PARAMS='{"ImageId":"ami-12345","InstanceType":"t2.micro","KeyName":"","SecurityGroupIds":[],"SubnetId":"","IamInstanceProfile":{"Arn":""},"UserData":"","TagSpecifications":[{"ResourceType":"instance","Tags":{}}],"MinCount":1,"MaxCount":1,"Region":"us-east-1"}'
        return 0
    }
    
    # Mock confirm_and_launch to set CREATED_INSTANCE_ID
    confirm_and_launch() {
        # shellcheck disable=SC2030,SC2031
        export CREATED_INSTANCE_ID="i-1234567890abcdef0"
        return 0
    }
    
    # Mock post_launch_actions to do nothing
    post_launch_actions() {
        return 0
    }
    
    run create_instance_from_ami
    [ "$status" -eq 0 ]
}
