package inventory

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// BlockDeviceMappingDetail is one device entry in an AMI's block device
// mappings -- SnapshotID/VolumeSizeGB are zero-value if the mapping has
// no Ebs section (e.g. an instance-store/ephemeral device).
type BlockDeviceMappingDetail struct {
	DeviceName   string
	VolumeSizeGB int32
	SnapshotID   string
}

// ImageDetail is one AMI's curated detail fields, fetched on demand for
// the Show AMI Detail workflow -- deliberately a separate,
// single-resource fetch rather than added fields on Image itself, same
// rationale as InstanceDetail (DESIGN.md, "Instance/AMI Detail Views").
type ImageDetail struct {
	ImageID             string
	Name                string
	CreationDate        string
	Region              string
	Architecture        string
	EnaSupport          bool
	RootDeviceName      string
	BlockDeviceMappings []BlockDeviceMappingDetail
	Project             string
	Environment         string
	Tags                map[string]string
}

// DescribeImageDetail fetches one AMI's curated detail fields via a
// single ec2:DescribeImages call scoped to imageID. client must already
// be scoped to region.
func DescribeImageDetail(ctx context.Context, client awsclient.EC2API, region, imageID string) (ImageDetail, error) {
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{imageID}})
	if err != nil {
		return ImageDetail{}, err
	}
	for _, img := range out.Images {
		if aws.ToString(img.ImageId) == imageID {
			return imageDetailFromSDK(img, region), nil
		}
	}
	return ImageDetail{}, fmt.Errorf("AMI %s not found", imageID)
}

func imageDetailFromSDK(img types.Image, region string) ImageDetail {
	_, project, environment := tagValues(img.Tags)

	mappings := make([]BlockDeviceMappingDetail, len(img.BlockDeviceMappings))
	for i, bdm := range img.BlockDeviceMappings {
		detail := BlockDeviceMappingDetail{DeviceName: aws.ToString(bdm.DeviceName)}
		if bdm.Ebs != nil {
			detail.VolumeSizeGB = aws.ToInt32(bdm.Ebs.VolumeSize)
			detail.SnapshotID = aws.ToString(bdm.Ebs.SnapshotId)
		}
		mappings[i] = detail
	}

	return ImageDetail{
		ImageID:             aws.ToString(img.ImageId),
		Name:                aws.ToString(img.Name),
		CreationDate:        aws.ToString(img.CreationDate),
		Region:              region,
		Architecture:        string(img.Architecture),
		EnaSupport:          aws.ToBool(img.EnaSupport),
		RootDeviceName:      aws.ToString(img.RootDeviceName),
		BlockDeviceMappings: mappings,
		Project:             project,
		Environment:         environment,
		Tags:                tagsToMap(img.Tags),
	}
}
