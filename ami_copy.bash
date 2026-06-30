#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# clone_ami.sh — interactively clone a stopped or running EC2 instance as a new AMI
# See basic_steps.md for background and timing expectations.
#
# Stopped instances produce a clean, application-consistent snapshot.
# Running instances are snapshotted with --no-reboot (no downtime) but the
# result is crash-consistent: PostgreSQL and OpenSearch will run their
# recovery process on first boot. Suitable for upgrade testing.
# ---------------------------------------------------------------------------

die() { echo "Error: $*" >&2; exit 1; }

# --- preflight --------------------------------------------------------------

command -v aws &>/dev/null || die "aws CLI not found. Install it first."

# Confirm the CLI has credentials configured
aws sts get-caller-identity &>/dev/null \
    || die "aws CLI credentials not configured or not reachable."

# --- fetch stopped and running instances ------------------------------------

echo "Fetching stopped and running EC2 instances..."
echo ""

instance_ids=()
instance_names=()
instance_amis=()
instance_states=()

while IFS=$'\t' read -r iid name ami state; do
    instance_ids+=("$iid")
    # AWS CLI returns the string "None" when a tag is absent
    [[ "$name" == "None" ]] && name="<no name>"
    instance_names+=("$name")
    instance_amis+=("$ami")
    instance_states+=("$state")
done < <(
    aws ec2 describe-instances \
        --filters "Name=instance-state-name,Values=stopped,running" \
        --query "Reservations[*].Instances[*].[InstanceId,Tags[?Key=='Name']|[0].Value,ImageId,State.Name]" \
        --output text
)

count="${#instance_ids[@]}"

if [[ "$count" -eq 0 ]]; then
    die "No stopped or running instances found in the current region."
fi

# --- display numbered list --------------------------------------------------

printf "  %-4s  %-22s  %-10s  %-30s  %s\n" "Num" "Instance ID" "State" "Name" "Source AMI"
printf "  %-4s  %-22s  %-10s  %-30s  %s\n" "---" "-----------" "-----" "----" "----------"

for i in "${!instance_ids[@]}"; do
    printf "  %-4s  %-22s  %-10s  %-30s  %s\n" \
        "$((i+1))" \
        "${instance_ids[$i]}" \
        "${instance_states[$i]}" \
        "${instance_names[$i]}" \
        "${instance_amis[$i]}"
done

echo ""

# --- select instance --------------------------------------------------------

while true; do
    read -rp "Select instance [1-${count}]: " sel
    if [[ "$sel" =~ ^[0-9]+$ ]] && (( sel >= 1 && sel <= count )); then
        break
    fi
    echo "  Please enter a number between 1 and ${count}."
done

idx=$(( sel - 1 ))
selected_id="${instance_ids[$idx]}"
selected_name="${instance_names[$idx]}"
selected_state="${instance_states[$idx]}"

echo ""
echo "Selected: ${selected_id} (${selected_name})  state: ${selected_state}"

if [[ "$selected_state" == "running" ]]; then
    echo ""
    echo "  Warning: this instance is running."
    echo "  The snapshot will use --no-reboot (no downtime), but the result is"
    echo "  crash-consistent. PostgreSQL and OpenSearch will run their recovery"
    echo "  process on first boot of the clone. This is fine for upgrade testing."
    echo ""
    read -rp "  Continue with running instance? [y/N]: " running_confirm
    [[ "$running_confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }
fi

echo ""

# --- gather volume size info -------------------------------------------------

echo "Gathering volume information..."
echo ""

total_gb=0
has_prior_snapshot=false

while IFS=$'\t' read -r vol_id size_gb vol_type snapshot_id; do
    if [[ "$snapshot_id" != "None" && -n "$snapshot_id" ]]; then
        has_prior_snapshot=true
        snap_note="yes (${snapshot_id})"
    else
        snap_note="no"
    fi
    printf "  %-24s  %5d GB  %-8s  prior snapshot: %s\n" \
        "$vol_id" "$size_gb" "$vol_type" "$snap_note"
    total_gb=$(( total_gb + size_gb ))
done < <(
    aws ec2 describe-volumes \
        --filters "Name=attachment.instance-id,Values=${selected_id}" \
        --query "Volumes[*].[VolumeId,Size,VolumeType,SnapshotId]" \
        --output text
)

echo ""
printf "  Total volume size : %d GB\n" "$total_gb"

estimate_time() {
    local gb=$1
    if   (( gb < 20  )); then echo "5–15 minutes"
    elif (( gb < 100 )); then echo "15–45 minutes"
    elif (( gb < 200 )); then echo "45–90 minutes"
    else                       echo "1.5–3+ hours"
    fi
}

printf "  Estimated copy time: %s\n" "$(estimate_time "$total_gb")"

if [[ "$has_prior_snapshot" == "true" ]]; then
    echo ""
    echo "  Note: one or more volumes have a prior snapshot — only changed blocks"
    echo "        will be copied. Actual time may be significantly shorter."
fi

echo ""

# --- check SSM availability and offer fstrim --------------------------------

echo "Checking SSM availability..."

ssm_ping=$(
    aws ssm describe-instance-information \
        --filters "Key=InstanceIds,Values=${selected_id}" \
        --query "InstanceInformationList[0].PingStatus" \
        --output text 2>/dev/null || echo "None"
)

if [[ "$ssm_ping" == "Online" ]]; then
    echo "  SSM is available on this instance."
    echo ""
    echo "  Running fstrim before snapshotting tells the EBS volume which blocks"
    echo "  are free, so the snapshot skips them. This can reduce copy time"
    echo "  significantly on instances with Docker churn or deleted data."
    echo ""
    read -rp "  Run fstrim via SSM before snapshotting? [y/N]: " run_fstrim
    if [[ "$run_fstrim" =~ ^[Yy]$ ]]; then
        echo ""
        echo "  Sending fstrim command via SSM..."
        command_id=$(
            aws ssm send-command \
                --instance-ids "$selected_id" \
                --document-name "AWS-RunShellScript" \
                --parameters 'commands=["sudo fstrim -av"]' \
                --query "Command.CommandId" \
                --output text
        )
        echo "  Command ID: ${command_id}"
        echo "  Waiting for fstrim to complete..."

        while true; do
            fstrim_status=$(
                aws ssm get-command-invocation \
                    --command-id "$command_id" \
                    --instance-id "$selected_id" \
                    --query "Status" \
                    --output text 2>/dev/null || echo "Pending"
            )
            case "$fstrim_status" in
                Success)
                    fstrim_output=$(
                        aws ssm get-command-invocation \
                            --command-id "$command_id" \
                            --instance-id "$selected_id" \
                            --query "StandardOutputContent" \
                            --output text
                    )
                    echo ""
                    echo "  fstrim completed:"
                    echo "$fstrim_output" | sed 's/^/    /'
                    break
                    ;;
                Failed|Cancelled|TimedOut)
                    echo ""
                    echo "  Warning: fstrim did not complete (status: ${fstrim_status})."
                    echo "  The snapshot will proceed but may copy more blocks than necessary."
                    break
                    ;;
                *)
                    printf "  [%s]  fstrim status: %s\n" "$(date +%H:%M:%S)" "$fstrim_status"
                    sleep 10
                    ;;
            esac
        done
    fi
else
    echo ""
    echo "  SSM is not available on this instance (status: ${ssm_ping})."
    echo "  fstrim cannot be run automatically. The snapshot will proceed without it,"
    echo "  which may result in a larger snapshot and longer copy time."
    echo ""
    read -rp "  Continue without fstrim? [y/N]: " skip_fstrim
    [[ "$skip_fstrim" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }
fi

echo ""

# --- prompt for new AMI name and description --------------------------------

# Build a safe default name from the instance name + today's date
safe_name=$(echo "$selected_name" | tr ' ' '-' | tr -cd '[:alnum:]-_.')
default_name="${safe_name:-instance}-clone-$(date +%Y-%m-%d)"
default_desc="Cloned from ${selected_id} (${selected_name}) on $(date +%Y-%m-%d)"

read -rp "New AMI name [${default_name}]: " ami_name
ami_name="${ami_name:-$default_name}"

read -rp "Description  [${default_desc}]: " ami_desc
ami_desc="${ami_desc:-$default_desc}"

# --- confirm before proceeding ----------------------------------------------

echo ""
echo "  Source instance : ${selected_id} (${selected_name})"
echo "  New AMI name    : ${ami_name}"
echo "  Description     : ${ami_desc}"
echo ""
read -rp "Proceed? [y/N]: " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

# --- create the AMI ---------------------------------------------------------

echo ""
echo "Creating AMI..."

image_id=$(
    aws ec2 create-image \
        --instance-id "$selected_id" \
        --name "$ami_name" \
        --description "$ami_desc" \
        --no-reboot \
        --query "ImageId" \
        --output text
)

echo "AMI ID  : ${image_id}"
echo "Name    : ${ami_name}"
echo ""
echo "Polling for completion every 30 seconds."
echo "Large volumes (Docker images + database + OpenSearch) may take 20-60+ minutes."
echo "Ctrl-C to stop polling — creation will continue in AWS regardless."
echo ""

# --- poll until available or failed -----------------------------------------

start_seconds=$(date +%s)

elapsed() {
    local total=$(( $(date +%s) - start_seconds ))
    printf "%dh %02dm %02ds" $(( total/3600 )) $(( (total%3600)/60 )) $(( total%60 ))
}

while true; do
    state=$(
        aws ec2 describe-images \
            --image-ids "$image_id" \
            --query "Images[0].State" \
            --output text 2>/dev/null || echo "unknown"
    )

    printf "[%s]  elapsed: %-12s  %s  state: %s\n" \
        "$(date +%H:%M:%S)" "$(elapsed)" "$image_id" "$state"

    case "$state" in
        available)
            echo ""
            echo "Done. AMI ${image_id} (${ami_name}) is ready to launch."
            echo "Total time: $(elapsed)"
            exit 0
            ;;
        failed)
            echo ""
            echo "Total time before failure: $(elapsed)"
            die "AMI creation failed. Check the AWS Console for details."
            ;;
    esac

    sleep 30
done
