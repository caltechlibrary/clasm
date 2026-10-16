package inventory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// EBS volumes, EBS snapshots and network interfaces (DR-0192): the secondary
// resources of an instance or an AMI. clasm tags them when it creates them (DR-0190);
// every one that existed before is untagged, so they are listed here to be found and
// tagged. Each listing is aggregated across regions and follows NextToken.

// Volume is an EBS volume.
type Volume struct {
	VolumeID   string
	Region     string
	SizeGiB    int32
	State      string
	VolumeType string
	// AttachedTo is the instance the volume is attached to, "" if none.
	AttachedTo string
	// Created is the creation date, YYYY-MM-DD.
	Created string
	// Tags is the volume's full tag set.
	Tags map[string]string
}

// Snapshot is an EBS snapshot owned by the account.
type Snapshot struct {
	SnapshotID    string
	Region        string
	VolumeSizeGiB int32
	State         string
	Description   string
	// Started is the date the snapshot was started, YYYY-MM-DD.
	Started string
	Tags    map[string]string
}

// NetworkInterface is an elastic network interface the account manages. Interfaces an
// AWS service manages for it (a load balancer's, a NAT gateway's) are left out.
type NetworkInterface struct {
	ID          string
	Region      string
	Status      string
	Description string
	PrivateIP   string
	// AttachedTo is the instance the interface is attached to, "" if none.
	AttachedTo string
	Tags       map[string]string
}

// listAcrossRegions runs list in every region concurrently and concatenates the results,
// naming the region of the first one that fails.
func listAcrossRegions[T any](ctx context.Context, clients map[string]awsclient.EC2API, list func(ctx context.Context, client awsclient.EC2API, region string) ([]T, error)) ([]T, error) {
	type result struct {
		region string
		items  []T
		err    error
	}
	results := make(chan result, len(clients))
	var wg sync.WaitGroup
	for region, client := range clients {
		wg.Add(1)
		go func(region string, client awsclient.EC2API) {
			defer wg.Done()
			items, err := list(ctx, client, region)
			results <- result{region, items, err}
		}(region, client)
	}
	wg.Wait()
	close(results)
	var all []T
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", r.region, r.err)
		}
		all = append(all, r.items...)
	}
	return all, nil
}

func dateOf(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// ListVolumes lists the account's EBS volumes in every region.
//
// @param ctx {context.Context} the call's context
// @param clients {map[string]awsclient.EC2API} one EC2 client per region
// @returns {[]Volume, error} the volumes, or the first region's error, named
func ListVolumes(ctx context.Context, clients map[string]awsclient.EC2API) ([]Volume, error) {
	return listAcrossRegions(ctx, clients, func(ctx context.Context, client awsclient.EC2API, region string) ([]Volume, error) {
		var vols []Volume
		input := &ec2.DescribeVolumesInput{}
		for {
			out, err := client.DescribeVolumes(ctx, input)
			if err != nil {
				return nil, err
			}
			for _, v := range out.Volumes {
				vol := Volume{
					VolumeID: aws.ToString(v.VolumeId), Region: region, SizeGiB: aws.ToInt32(v.Size), State: string(v.State),
					VolumeType: string(v.VolumeType), Created: dateOf(v.CreateTime), Tags: tagsToMap(v.Tags),
				}
				if len(v.Attachments) > 0 {
					vol.AttachedTo = aws.ToString(v.Attachments[0].InstanceId)
				}
				vols = append(vols, vol)
			}
			if out.NextToken == nil {
				return vols, nil
			}
			input.NextToken = out.NextToken
		}
	})
}

// ListSnapshots lists the EBS snapshots the account owns in every region; snapshots
// other accounts shared with it are left out.
//
// @param ctx {context.Context} the call's context
// @param clients {map[string]awsclient.EC2API} one EC2 client per region
// @returns {[]Snapshot, error} the snapshots, or the first region's error, named
func ListSnapshots(ctx context.Context, clients map[string]awsclient.EC2API) ([]Snapshot, error) {
	return listAcrossRegions(ctx, clients, func(ctx context.Context, client awsclient.EC2API, region string) ([]Snapshot, error) {
		var snaps []Snapshot
		input := &ec2.DescribeSnapshotsInput{OwnerIds: []string{"self"}}
		for {
			out, err := client.DescribeSnapshots(ctx, input)
			if err != nil {
				return nil, err
			}
			for _, s := range out.Snapshots {
				snaps = append(snaps, Snapshot{
					SnapshotID: aws.ToString(s.SnapshotId), Region: region, VolumeSizeGiB: aws.ToInt32(s.VolumeSize), State: string(s.State),
					Description: aws.ToString(s.Description), Started: dateOf(s.StartTime), Tags: tagsToMap(s.Tags),
				})
			}
			if out.NextToken == nil {
				return snaps, nil
			}
			input.NextToken = out.NextToken
		}
	})
}

// ListNetworkInterfaces lists the network interfaces the account manages in every
// region, leaving out those an AWS service manages on its behalf.
//
// @param ctx {context.Context} the call's context
// @param clients {map[string]awsclient.EC2API} one EC2 client per region
// @returns {[]NetworkInterface, error} the interfaces, or the first region's error, named
func ListNetworkInterfaces(ctx context.Context, clients map[string]awsclient.EC2API) ([]NetworkInterface, error) {
	return listAcrossRegions(ctx, clients, func(ctx context.Context, client awsclient.EC2API, region string) ([]NetworkInterface, error) {
		var enis []NetworkInterface
		input := &ec2.DescribeNetworkInterfacesInput{}
		for {
			out, err := client.DescribeNetworkInterfaces(ctx, input)
			if err != nil {
				return nil, err
			}
			for _, n := range out.NetworkInterfaces {
				if aws.ToBool(n.RequesterManaged) {
					continue
				}
				eni := NetworkInterface{
					ID: aws.ToString(n.NetworkInterfaceId), Region: region, Status: string(n.Status),
					Description: aws.ToString(n.Description), PrivateIP: aws.ToString(n.PrivateIpAddress), Tags: tagsToMap(n.TagSet),
				}
				if n.Attachment != nil {
					eni.AttachedTo = aws.ToString(n.Attachment.InstanceId)
				}
				enis = append(enis, eni)
			}
			if out.NextToken == nil {
				return enis, nil
			}
			input.NextToken = out.NextToken
		}
	})
}
