#!/usr/bin/env bats
# BATS tests for AMI creation from EC2 instance (Phase 5 / Phase 5b)
# Phase 5b ports ami_copy.bash's volume-size estimate, prior-snapshot
# detection, SSM fstrim step, and unbounded creation polling into this
# workflow. See DECISIONS.md "AMI-from-instance: fold ami_copy.bash
# capabilities into Phase 5" and PLAN.md Phase 5b.

load 'lib/test_helper'

setup() {
    setup_mock_aws
    source_main_script
}

teardown() {
    cleanup_mock_aws
}

# =============================================================================
# estimate_ami_creation_time — pure function, ports ami_copy.bash's
# estimate_time() boundaries
# =============================================================================

@test "estimate_ami_creation_time: under 20GB" {
    run estimate_ami_creation_time 10
    [ "$status" -eq 0 ]
    [[ "$output" == *"5"*"15"*"minutes"* ]]
}

@test "estimate_ami_creation_time: 20 to 100GB" {
    run estimate_ami_creation_time 50
    [ "$status" -eq 0 ]
    [[ "$output" == *"15"*"45"*"minutes"* ]]
}

@test "estimate_ami_creation_time: boundary at exactly 20GB falls into 20-100 bucket" {
    run estimate_ami_creation_time 20
    [ "$status" -eq 0 ]
    [[ "$output" == *"15"*"45"*"minutes"* ]]
}

@test "estimate_ami_creation_time: 100 to 200GB" {
    run estimate_ami_creation_time 150
    [ "$status" -eq 0 ]
    [[ "$output" == *"45"*"90"*"minutes"* ]]
}

@test "estimate_ami_creation_time: boundary at exactly 100GB falls into 100-200 bucket" {
    run estimate_ami_creation_time 100
    [ "$status" -eq 0 ]
    [[ "$output" == *"45"*"90"*"minutes"* ]]
}

@test "estimate_ami_creation_time: 200GB and over" {
    run estimate_ami_creation_time 250
    [ "$status" -eq 0 ]
    [[ "$output" == *"hour"* ]]
}

@test "estimate_ami_creation_time: boundary at exactly 200GB falls into 200+ bucket" {
    run estimate_ami_creation_time 200
    [ "$status" -eq 0 ]
    [[ "$output" == *"hour"* ]]
}

# =============================================================================
# gather_volume_info — aggregates attached EBS volume sizes for an instance
# =============================================================================

@test "gather_volume_info sums sizes across multiple attached volumes" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[{"VolumeId":"vol-111","Size":20,"VolumeType":"gp3","SnapshotId":null},{"VolumeId":"vol-222","Size":80,"VolumeType":"gp3","SnapshotId":null}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run gather_volume_info "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    total_gb=$(echo "$output" | jq -r '.TotalGB')
    [ "$total_gb" -eq 100 ]
    has_snapshot=$(echo "$output" | jq -r '.HasPriorSnapshot')
    [ "$has_snapshot" == "false" ]
}

@test "gather_volume_info detects a prior snapshot on an attached volume" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[{"VolumeId":"vol-111","Size":20,"VolumeType":"gp3","SnapshotId":"snap-abc123"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run gather_volume_info "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    has_snapshot=$(echo "$output" | jq -r '.HasPriorSnapshot')
    [ "$has_snapshot" == "true" ]
}

@test "select_instance_for_ami surfaces a prior-snapshot note when one volume has a snapshot" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[{"VolumeId":"vol-111","Size":20,"VolumeType":"gp3","SnapshotId":"snap-abc123"}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    pick_instance() {
        export PICKED_INSTANCE='{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Name":"stopped"},"Region":"us-east-1"}'
        return 0
    }
    offer_fstrim_before_snapshot() { return 0; }

    run select_instance_for_ami
    [ "$status" -eq 0 ]
    [[ "$output" == *"prior snapshot"* ]]
}

@test "select_instance_for_ami omits the prior-snapshot note when no volume has a snapshot" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[{"VolumeId":"vol-111","Size":20,"VolumeType":"gp3","SnapshotId":null}]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    pick_instance() {
        export PICKED_INSTANCE='{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Name":"stopped"},"Region":"us-east-1"}'
        return 0
    }
    offer_fstrim_before_snapshot() { return 0; }

    run select_instance_for_ami
    [ "$status" -eq 0 ]
    [[ "$output" != *"prior snapshot"* ]]
}

# =============================================================================
# check_ssm_availability / run_fstrim_via_ssm — ported from ami_copy.bash's
# SSM fstrim optimization step (PLAN.md Phase 5b)
# =============================================================================

@test "check_ssm_availability returns Online when SSM ping status is Online" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-information"; then
    echo '{"InstanceInformationList":[{"PingStatus":"Online"}]}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run check_ssm_availability "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    [ "$output" == "Online" ]
}

@test "check_ssm_availability returns None when instance has no SSM agent registered" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-instance-information"; then
    echo '{"InstanceInformationList":[]}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run check_ssm_availability "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    [ "$output" == "None" ]
}

@test "check_ssm_availability returns None when the describe call fails outright" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run check_ssm_availability "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    [ "$output" == "None" ]
}

@test "run_fstrim_via_ssm returns success and prints output after polling past Pending" {
    export SSM_POLL_INTERVAL=0
    local state_file="$TEST_TMP_DIR/fstrim_poll_count"
    echo 0 > "$state_file"

    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
CMD="\$*"
if echo "\$CMD" | grep -q "send-command"; then
    echo '{"Command":{"CommandId":"cmd-123"}}'
    exit 0
fi
if echo "\$CMD" | grep -q "get-command-invocation"; then
    count=\$(cat "$state_file")
    count=\$((count + 1))
    echo "\$count" > "$state_file"
    if [[ "\$count" -lt 2 ]]; then
        echo '{"Status":"InProgress"}'
    else
        echo '{"Status":"Success","StandardOutputContent":"fstrim done"}'
    fi
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run run_fstrim_via_ssm "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    [[ "$output" == *"fstrim done"* ]]
}

@test "run_fstrim_via_ssm returns failure when the command status is Failed" {
    export SSM_POLL_INTERVAL=0

    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "send-command"; then
    echo '{"Command":{"CommandId":"cmd-123"}}'
    exit 0
fi
if echo "$CMD" | grep -q "get-command-invocation"; then
    echo '{"Status":"Failed"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run run_fstrim_via_ssm "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 1 ]
    [[ "$output" == *"Failed"* ]]
}

@test "run_fstrim_via_ssm returns failure when send-command yields no CommandId" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "send-command"; then
    echo '{}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run run_fstrim_via_ssm "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 1 ]
}

@test "select_instance_for_ami aborts when the user cancels at the fstrim step" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[]}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    pick_instance() {
        export PICKED_INSTANCE='{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Name":"stopped"},"Region":"us-east-1"}'
        return 0
    }
    offer_fstrim_before_snapshot() { echo "Cancelled."; return 1; }

    run select_instance_for_ami
    [ "$status" -eq 1 ]
    [[ "$output" == *"Cancelled."* ]]
}

@test "offer_fstrim_before_snapshot interactive prompts [SKIP]" {
    skip "Interactive y/N prompts require stdin mocking - TODO, consistent with other interactive functions in this suite"
}

@test "gather_volume_info reports zero total when instance has no volumes" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[]}'
    exit 0
fi
if echo "$CMD" | grep -q "get-caller-identity"; then
    echo '{"Account":"123456789012"}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run gather_volume_info "i-0123456789abcdef0" "us-east-1"
    [ "$status" -eq 0 ]
    total_gb=$(echo "$output" | jq -r '.TotalGB')
    [ "$total_gb" -eq 0 ]
}

# =============================================================================
# format_elapsed / post_ami_creation_actions — unbounded polling, ported from
# ami_copy.bash's elapsed()/polling loop (PLAN.md Phase 5b). Replaces the
# 600-second timeout, which is too short for real Invenio RDM AMI sizes.
# =============================================================================

@test "format_elapsed renders zero seconds" {
    run format_elapsed 0
    [ "$status" -eq 0 ]
    [ "$output" == "0h 00m 00s" ]
}

@test "format_elapsed renders minutes and seconds" {
    run format_elapsed 90
    [ "$status" -eq 0 ]
    [ "$output" == "0h 01m 30s" ]
}

@test "format_elapsed renders hours, minutes, and seconds" {
    run format_elapsed 3661
    [ "$status" -eq 0 ]
    [ "$output" == "1h 01m 01s" ]
}

@test "post_ami_creation_actions polls past many pending states before available" {
    export AMI_POLL_INTERVAL=0
    export CREATED_AMI_ID="ami-0123456789abcdef0"
    export AMI_CREATION_PARAMS='{"Region":"us-east-1"}'

    local state_file="$TEST_TMP_DIR/ami_poll_count"
    echo 0 > "$state_file"

    cat > "$MOCK_AWS_DIR/aws" << AWSEOF
#!/bin/bash
CMD="\$*"
if echo "\$CMD" | grep -q "describe-images"; then
    count=\$(cat "$state_file")
    count=\$((count + 1))
    echo "\$count" > "$state_file"
    if [[ "\$count" -lt 8 ]]; then
        echo '{"Images":[{"State":"pending"}]}'
    else
        echo '{"Images":[{"State":"available","Name":"test-ami","CreationDate":"2026-06-30T00:00:00Z"}]}'
    fi
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run post_ami_creation_actions
    [ "$status" -eq 0 ]
    [[ "$output" == *"available"* ]]
    # confirms it polled past the old 600s/short-iteration cutoff, not just 1-2 tries
    polls=$(cat "$state_file")
    [ "$polls" -ge 8 ]
}

@test "post_ami_creation_actions returns failure immediately on failed state, without waiting it out" {
    export AMI_POLL_INTERVAL=0
    export CREATED_AMI_ID="ami-0123456789abcdef0"
    export AMI_CREATION_PARAMS='{"Region":"us-east-1"}'

    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-images"; then
    echo '{"Images":[{"State":"failed"}]}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    run post_ami_creation_actions
    [ "$status" -eq 1 ]
    [[ "$output" == *"failed"* ]]
}

# =============================================================================
# select_instance_for_ami running-instance warning -- ported from
# ami_copy_basic_steps.md's crash-consistency guidance (PLAN.md Phase 5b)
# =============================================================================

@test "select_instance_for_ami warns about crash-consistency for running instances and continues on y" {
    cat > "$MOCK_AWS_DIR/aws" << 'AWSEOF'
#!/bin/bash
CMD="$*"
if echo "$CMD" | grep -q "describe-volumes"; then
    echo '{"Volumes":[]}'
    exit 0
fi
exit 1
AWSEOF
    chmod +x "$MOCK_AWS_DIR/aws"

    pick_instance() {
        export PICKED_INSTANCE='{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Name":"running"},"Region":"us-east-1"}'
        return 0
    }
    offer_fstrim_before_snapshot() { return 0; }

    run select_instance_for_ami <<< "y"
    [ "$status" -eq 0 ]
    [[ "$output" == *"PostgreSQL"* ]]
    [[ "$output" == *"OpenSearch"* ]]
}

@test "select_instance_for_ami cancels when the user declines the running-instance warning" {
    pick_instance() {
        export PICKED_INSTANCE='{"InstanceId":"i-0123456789abcdef0","InstanceType":"t2.micro","State":{"Name":"running"},"Region":"us-east-1"}'
        return 0
    }

    run select_instance_for_ami <<< "n"
    [ "$status" -eq 1 ]
    [[ "$output" == *"Cancelled."* ]]
}
