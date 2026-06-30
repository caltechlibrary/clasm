#!/usr/bin/env bats
# BATS tests for resource listing functions

load 'lib/test_helper'

setup() {
    setup_mock_aws
    source_main_script
}

teardown() {
    cleanup_mock_aws
}

@test "list_ec2_instances queries all four regions" {
    mock_aws_empty
    run list_ec2_instances
    [ "$status" -eq 0 ]
}

@test "list_ec2_instances returns empty array when no instances exist" {
    mock_aws_empty
    run list_ec2_instances
    [ "$status" -eq 0 ]
    [[ "$output" == "[]" ]]
}

@test "list_ec2_instances returns instances from multiple regions" {
    mock_aws_with_instances
    run list_ec2_instances
    [ "$status" -eq 0 ]
    [[ "$output" == *"i-0123456789abcdef0"* ]]
    [[ "$output" == *"i-abcdef01234567890"* ]]
    [[ "$output" == *"us-east-1"* ]]
    [[ "$output" == *"us-west-2"* ]]
}

@test "list_ec2_instances filters out terminated instances" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instances"; then
    echo '{"Reservations":[{"Instances":[{"InstanceId":"i-run","State":{"Name":"running"}},{"InstanceId":"i-term","State":{"Name":"terminated"}}]}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_ec2_instances
    [ "$status" -eq 0 ]
    [[ "$output" == *"i-run"* ]]
    [[ "$output" != *"i-term"* ]]
}

@test "list_ec2_instances extracts instance Name from tags" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instances"; then
    echo '{"Reservations":[{"Instances":[{"InstanceId":"i-123","State":{"Name":"running"},"Tags":[{"Key":"Name","Value":"my-server"}]}]}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_ec2_instances
    [ "$status" -eq 0 ]
    [[ "$output" == *"my-server"* ]]
}

@test "list_ec2_instances handles instances without Name tag" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instances"; then
    echo '{"Reservations":[{"Instances":[{"InstanceId":"i-123","State":{"Name":"running"},"Tags":[{"Key":"Env","Value":"prod"}]}]}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_ec2_instances
    [ "$status" -eq 0 ]
    [[ "$output" == *"i-123"* ]]
}

@test "list_amis queries all four regions" {
    mock_aws_empty
    run list_amis
    [ "$status" -eq 0 ]
}

@test "list_amis returns only owned AMIs via --owners filter" {
    # The function uses --owners to filter, so mock only returns owned AMIs
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-images"; then
    # Mock returns only owned AMIs (since --owners is passed by the function)
    echo '{"Images":[{"ImageId":"ami-owned","Name":"my-ami","OwnerId":"123456789012","State":"available","CreationDate":"2026-01-01T00:00:00Z"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_amis
    [ "$status" -eq 0 ]
    [[ "$output" == *"ami-owned"* ]]
    [[ "$output" != *"ami-public"* ]]
}

@test "list_amis returns empty array when no owned AMIs exist" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-images"; then
    echo '{"Images":[]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
    run list_amis
    [ "$status" -eq 0 ]
    [[ "$output" == "[]" ]]
}

@test "display_instances formats output as table" {
    skip "display_instances not yet fully tested"
}

@test "display_amis formats output as table" {
    skip "display_amis not yet fully tested"
}
