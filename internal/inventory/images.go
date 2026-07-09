package inventory

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Image is an AMI owned by the account, as displayed/managed by awsops,
// aggregated across regions. Project and Environment are empty if the
// image isn't tagged that way -- see Instance's doc comment.
type Image struct {
	ImageID      string
	Name         string
	CreationDate string
	Region       string
	Project      string
	Environment  string
	// EnaSupport reports whether the AMI is enabled for Enhanced
	// Networking (ENA), for the instance-type-vs-AMI ENA pre-flight
	// check (see internal/workflow/instance_type_ena_check.go). False
	// when the SDK doesn't report a value, matching AWS's own default.
	EnaSupport bool
}

// ListImages queries ec2:DescribeImages (scoped to Owners: [self]) in
// each region concurrently, aggregates the results, and filters to
// images in the "available" state -- see DECISIONS.md, "AMI scope
// limited to account-owned only".
func ListImages(ctx context.Context, clients map[string]awsclient.EC2API) ([]Image, error) {
	type result struct {
		region string
		images []Image
		err    error
	}

	results := make(chan result, len(clients))
	var wg sync.WaitGroup
	for region, client := range clients {
		wg.Add(1)
		go func(region string, client awsclient.EC2API) {
			defer wg.Done()
			images, err := listImagesInRegion(ctx, client, region)
			results <- result{region: region, images: images, err: err}
		}(region, client)
	}
	wg.Wait()
	close(results)

	var all []Image
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", r.region, r.err)
		}
		all = append(all, r.images...)
	}
	return all, nil
}

func listImagesInRegion(ctx context.Context, client awsclient.EC2API, region string) ([]Image, error) {
	var images []Image
	input := &ec2.DescribeImagesInput{Owners: []string{"self"}}
	for {
		out, err := client.DescribeImages(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, img := range out.Images {
			if img.State != types.ImageStateAvailable {
				continue
			}
			images = append(images, imageFromSDK(img, region))
		}
		if out.NextToken == nil {
			break
		}
		input.NextToken = out.NextToken
	}
	return images, nil
}

func imageFromSDK(img types.Image, region string) Image {
	_, project, environment := tagValues(img.Tags)
	return Image{
		ImageID:      aws.ToString(img.ImageId),
		Name:         aws.ToString(img.Name),
		CreationDate: aws.ToString(img.CreationDate),
		Region:       region,
		Project:      project,
		Environment:  environment,
		EnaSupport:   aws.ToBool(img.EnaSupport),
	}
}
