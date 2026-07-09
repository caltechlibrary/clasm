// Package inventory aggregates EC2 instance and AMI listings across the
// four configured regions.
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

// Instance is an EC2 instance as displayed/managed by awsops, aggregated
// across regions. Project and Environment are empty if the instance
// isn't tagged that way -- see DECISIONS.md, "Introduce a light
// Project/Environment tagging convention". PublicIP/PrivateIP are empty
// if the instance has none assigned (e.g. stopped, or no public IP/EIP)
// -- see DECISIONS.md, "Show instance IP addresses in the main
// listing". Rendering an empty value as "unknown"/"none" is the display
// layer's job, not this package's.
type Instance struct {
	InstanceID  string
	Name        string
	State       string
	ImageID     string
	Region      string
	Project     string
	Environment string
	PublicIP    string
	PrivateIP   string
	// KeyName is the EC2 key pair name the instance was launched with, if
	// any -- used by Key Management's Delete Key Pair to detect dependent
	// instances (see internal/workflow/keypair_delete.go).
	KeyName string
}

// ListInstances queries ec2:DescribeInstances in each region concurrently,
// aggregates the results, and excludes terminated instances.
func ListInstances(ctx context.Context, clients map[string]awsclient.EC2API) ([]Instance, error) {
	type result struct {
		region    string
		instances []Instance
		err       error
	}

	results := make(chan result, len(clients))
	var wg sync.WaitGroup
	for region, client := range clients {
		wg.Add(1)
		go func(region string, client awsclient.EC2API) {
			defer wg.Done()
			instances, err := listInstancesInRegion(ctx, client, region)
			results <- result{region: region, instances: instances, err: err}
		}(region, client)
	}
	wg.Wait()
	close(results)

	var all []Instance
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", r.region, r.err)
		}
		all = append(all, r.instances...)
	}
	return all, nil
}

func listInstancesInRegion(ctx context.Context, client awsclient.EC2API, region string) ([]Instance, error) {
	var instances []Instance
	input := &ec2.DescribeInstancesInput{}
	for {
		out, err := client.DescribeInstances(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, reservation := range out.Reservations {
			for _, inst := range reservation.Instances {
				if inst.State != nil && inst.State.Name == types.InstanceStateNameTerminated {
					continue
				}
				instances = append(instances, instanceFromSDK(inst, region))
			}
		}
		if out.NextToken == nil {
			break
		}
		input.NextToken = out.NextToken
	}
	return instances, nil
}

func instanceFromSDK(inst types.Instance, region string) Instance {
	name, project, environment := tagValues(inst.Tags)
	state := ""
	if inst.State != nil {
		state = string(inst.State.Name)
	}
	return Instance{
		InstanceID:  aws.ToString(inst.InstanceId),
		Name:        name,
		State:       state,
		ImageID:     aws.ToString(inst.ImageId),
		Region:      region,
		Project:     project,
		Environment: environment,
		PublicIP:    aws.ToString(inst.PublicIpAddress),
		PrivateIP:   aws.ToString(inst.PrivateIpAddress),
		KeyName:     aws.ToString(inst.KeyName),
	}
}

func tagValues(tags []types.Tag) (name, project, environment string) {
	for _, tag := range tags {
		switch aws.ToString(tag.Key) {
		case "Name":
			name = aws.ToString(tag.Value)
		case "Project":
			project = aws.ToString(tag.Value)
		case "Environment":
			environment = aws.ToString(tag.Value)
		}
	}
	return name, project, environment
}
