package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// VolumeInfo is one EBS volume attached to an instance being AMI'd.
type VolumeInfo struct {
	VolumeID    string
	SizeGB      int32
	HasSnapshot bool // true if this volume was created from a prior snapshot
}

// GatherVolumeInfo queries ec2:DescribeVolumes for every volume attached
// to instanceID, summing their sizes and detecting whether any has a
// prior snapshot (in which case AMI creation only copies changed blocks,
// so the actual time may be shorter than EstimateAMICreationTime's
// estimate) -- see DESIGN.md, "Domain Knowledge Carried Forward".
func GatherVolumeInfo(ctx context.Context, client awsclient.EC2API, instanceID string) (volumes []VolumeInfo, totalGB int32, hasPriorSnapshot bool, err error) {
	out, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: []types.Filter{{Name: aws.String("attachment.instance-id"), Values: []string{instanceID}}},
	})
	if err != nil {
		return nil, 0, false, err
	}

	for _, vol := range out.Volumes {
		size := aws.ToInt32(vol.Size)
		hasSnapshot := aws.ToString(vol.SnapshotId) != ""
		volumes = append(volumes, VolumeInfo{
			VolumeID:    aws.ToString(vol.VolumeId),
			SizeGB:      size,
			HasSnapshot: hasSnapshot,
		})
		totalGB += size
		if hasSnapshot {
			hasPriorSnapshot = true
		}
	}
	return volumes, totalGB, hasPriorSnapshot, nil
}

// EstimateAMICreationTime returns a human-readable estimate for how long
// ec2:CreateImage will take for a volume set totaling gb GiB -- the
// four-tier table carried forward from ec2_ami_manager.bash's
// estimate_ami_creation_time.
func EstimateAMICreationTime(gb int32) string {
	switch {
	case gb < 20:
		return "5-15 minutes"
	case gb < 100:
		return "15-45 minutes"
	case gb < 200:
		return "45-90 minutes"
	default:
		return "1.5-3+ hours"
	}
}
