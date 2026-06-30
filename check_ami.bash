#!/bin/bash

# Define regions
REGIONS=("us-east-1" "us-east-2" "us-west-1" "us-west-2")

# Function to list instances with Name tags
list_instances() {
    local region=$1
    echo "========================================="
    echo " INSTANCES in $region"
    echo "========================================="
    aws ec2 describe-instances --region "$region" \
        --query "Reservations[].Instances[].[Tags[?Key=='Name'].Value | [0], InstanceId, State.Name, LaunchTime]" \
        --output table
    echo
}

# Function to list AMIs you own
list_amis() {
    local region=$1
    echo "========================================="
    echo " AMIs in $region"
    echo "========================================="
    aws ec2 describe-images --region "$region" --owners self \
        --query "Images[*].[Name, ImageId, CreationDate, State]" \
        --output table
    echo
}

# Main
for region in "${REGIONS[@]}"; do
    list_instances "$region"
    list_amis "$region"
done
