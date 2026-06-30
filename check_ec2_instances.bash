#!/bin/bash

for region in us-west-1 us-west-2 us-east-1 us-east-2; do # $(aws ec2 describe-regions --query "Regions[].RegionName" --output text); do
  echo "=== Region: $region ==="
  aws ec2 describe-instances --region "$region" \
    --query "Reservations[].Instances[].[Tags[?Key=='Name'].Value | [0], InstanceId, State.Name]" \
    --output table
done
