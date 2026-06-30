#!/usr/bin/env bash
# AWS Tools Test Helper - Simplified version

set -o errexit
set -o nounset
set -o pipefail

TEST_HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$TEST_HELPER_DIR")")"

TEST_TMP_DIR=""
MOCK_AWS_DIR=""
ORIGINAL_PATH=""

# shellcheck disable=SC2034
REGIONS=("us-east-1" "us-east-2" "us-west-1" "us-west-2")

setup_mock_aws() {
    TEST_TMP_DIR="$(mktemp -d)"
    MOCK_AWS_DIR="$TEST_TMP_DIR/mock_aws"
    mkdir -p "$MOCK_AWS_DIR"
    ORIGINAL_PATH="$PATH"
    export PATH="$MOCK_AWS_DIR:$PATH"
    # Note: jq is NOT mocked - tests use the real jq
    create_mock_aws_default
}

cleanup_mock_aws() {
    export PATH="$ORIGINAL_PATH"
    [[ -n "$TEST_TMP_DIR" && -d "$TEST_TMP_DIR" ]] && rm -rf "$TEST_TMP_DIR"
    TEST_TMP_DIR="" ; MOCK_AWS_DIR="" ; ORIGINAL_PATH=""
}

create_mock_aws_default() {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instances"; then
    echo '{"Reservations": []}' ; exit 0
elif echo "$CMD" | grep -q "describe-images"; then
    echo '{"Images": []}' ; exit 0
elif echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012","UserId":"AIDA","Arn":"arn:aws:iam::123456789012:user/test"}' ; exit 0
fi
echo "ERROR: Unknown: $CMD" >&2 ; exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

mock_aws_empty() {
    create_mock_aws_default
}

mock_aws_with_instances() {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012","UserId":"AIDA","Arn":"arn:aws:iam::123456789012:user/test"}' ; exit 0
elif echo "$CMD" | grep -q "describe-instances"; then
    if echo "$CMD" | grep -q "us-east-1"; then
        echo '{"Reservations":[{"Instances":[{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Code":16,"Name":"running"},"ImageId":"ami-abc1234567890","Tags":[{"Key":"Name","Value":"web-server"}]}]}]}'
    elif echo "$CMD" | grep -q "us-west-2"; then
        echo '{"Reservations":[{"Instances":[{"InstanceId":"i-abcdef01234567890","InstanceType":"t2.small","State":{"Code":80,"Name":"stopped"},"ImageId":"ami-def4567890123","Tags":[{"Key":"Name","Value":"db-server"}]}]}]}'
    else
        echo '{"Reservations":[]}'
    fi ; exit 0
elif echo "$CMD" | grep -q "describe-images"; then
    if echo "$CMD" | grep -q "us-east-1"; then
        echo '{"Images":[{"ImageId":"ami-abc1234567890","Name":"base-ubuntu-2404","CreationDate":"2026-01-15T10:00:00Z","State":"available","OwnerId":"123456789012"},{"ImageId":"ami-ghi7890123456","Name":"custom-ami","CreationDate":"2026-03-10T10:00:00Z","State":"available","OwnerId":"123456789012"}]}'
    elif echo "$CMD" | grep -q "us-west-2"; then
        echo '{"Images":[{"ImageId":"ami-def4567890123","Name":"app-server-v2","CreationDate":"2026-02-20T10:00:00Z","State":"available","OwnerId":"123456789012"}]}'
    else
        echo '{"Images":[]}'
    fi ; exit 0
fi
echo "ERROR: Unknown: $CMD" >&2 ; exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

mock_aws_error() {
    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
CMD="$*"
echo "{\"__type\": \"$1\", \"message\": \"$2\"}" >&2 ; exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

mock_aws_run_instances_success() {
    local instance_id="$1"
    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "run-instances"; then
    echo "{\"Instances\":[{\"InstanceId\":\"$instance_id\",\"State\":{\"Code\":0,\"Name\":\"pending\"}}]}" ; exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}' ; exit 0
fi
echo "ERROR" >&2 ; exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"
}

source_main_script() {
    # shellcheck disable=SC1091
    source "$PROJECT_ROOT/ec2_ami_manager.bash"
}
