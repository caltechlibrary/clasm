package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

// ubuntuAMIOwnerID is Canonical's well-known AWS account ID that
// publishes official Ubuntu Server AMIs -- a long-standing, publicly
// documented value (used throughout AWS's own docs and infrastructure-
// as-code tooling), safe to hardcode.
const ubuntuAMIOwnerID = "099720109477"

// curatedUbuntuReleases is a short, hand-picked list of official Ubuntu
// LTS releases offered alongside the account's own AMIs when picking a
// base AMI to launch from (DESIGN.md, Feature 2/3: "Select an AMI") --
// not a general public-AMI browser. amd64/x86_64 only, matching the
// curated instance-type list's architecture (DECISIONS.md, "Instance
// type pick list: curated shortlist, not the full AWS catalog"). See
// DECISIONS.md, "Offer official Ubuntu LTS AMIs alongside owned AMIs
// when picking a base AMI" -- anything more exotic (arm64/Graviton, a
// different distribution, a specific non-LTS release) means copying the
// specific public AMI into the account first (outside awsops), same as
// before this addition.
//
// namePattern must match Canonical's full published AMI Name, which is
// prefixed with a path-like "ubuntu/images/hvm-ssd/" or newer
// "ubuntu/images/hvm-ssd-gp3/" (real AMIs are never named just
// "ubuntu-noble-24.04-amd64-server-..." on their own) -- confirmed via
// -debug's JSONL log after the first version of this pattern (missing
// the leading wildcard) matched zero real AMIs in every region, every
// time, silently, per DECISIONS.md, "Offer official Ubuntu LTS AMIs..."
// (fix logged in "Fix official Ubuntu AMI name filter pattern", below).
var curatedUbuntuReleases = []struct {
	label       string
	namePattern string
}{
	{"Ubuntu 24.04 LTS (Noble Numbat), amd64 -- official", "ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*"},
	{"Ubuntu 22.04 LTS (Jammy Jellyfish), amd64 -- official", "ubuntu/images/hvm-ssd*/ubuntu-jammy-22.04-amd64-server-*"},
}

// latestUbuntuAMI finds the most recently published AMI matching
// namePattern among Canonical's official Ubuntu images, or nil if none
// match in this region (not an error -- a given release may not be
// published in every region, or may eventually be retired from one).
func latestUbuntuAMI(ctx context.Context, client awsclient.EC2API, namePattern string) (*types.Image, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{ubuntuAMIOwnerID},
		Filters: []types.Filter{
			{Name: aws.String("name"), Values: []string{namePattern}},
			{Name: aws.String("state"), Values: []string{"available"}},
			{Name: aws.String("root-device-type"), Values: []string{"ebs"}},
			{Name: aws.String("virtualization-type"), Values: []string{"hvm"}},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(out.Images) == 0 {
		return nil, nil
	}
	latest := out.Images[0]
	for _, img := range out.Images[1:] {
		if aws.ToString(img.CreationDate) > aws.ToString(latest.CreationDate) {
			latest = img
		}
	}
	return &latest, nil
}

// listOfficialUbuntuAMIsInRegion resolves curatedUbuntuReleases to real
// AMIs in one region, skipping any release not published there. Carries
// EnaSupport through exactly like inventory.imageFromSDK does for owned
// AMIs -- without it, the instance-type-vs-AMI-ENA-support pre-flight
// check would wrongly flag a modern, actually-ENA-enabled official AMI
// as incompatible with every Nitro-based curated instance type.
func listOfficialUbuntuAMIsInRegion(ctx context.Context, client awsclient.EC2API, region string) ([]inventory.Image, error) {
	var out []inventory.Image
	for _, rel := range curatedUbuntuReleases {
		img, err := latestUbuntuAMI(ctx, client, rel.namePattern)
		if err != nil {
			return nil, err
		}
		if img == nil {
			continue
		}
		out = append(out, inventory.Image{
			ImageID:      aws.ToString(img.ImageId),
			Name:         rel.label,
			CreationDate: aws.ToString(img.CreationDate),
			Region:       region,
			EnaSupport:   aws.ToBool(img.EnaSupport),
		})
	}
	return out, nil
}

// listOfficialUbuntuAMIs aggregates listOfficialUbuntuAMIsInRegion across
// every configured region client, sequentially -- this only runs once,
// on demand, when a base-AMI pick list is being built (not on every
// resource-list refresh), so the extra latency of a handful of
// DescribeImages calls isn't a concern the way it would be in the main
// listing loop.
func listOfficialUbuntuAMIs(ctx context.Context, clients map[string]awsclient.EC2API) ([]inventory.Image, error) {
	var all []inventory.Image
	for region, client := range clients {
		imgs, err := listOfficialUbuntuAMIsInRegion(ctx, client, region)
		if err != nil {
			return nil, err
		}
		all = append(all, imgs...)
	}
	return all, nil
}

// imagesWithOfficialUbuntu appends the curated official Ubuntu AMIs to
// images for a base-AMI pick list, best-effort: if the lookup itself
// errors, the picker still works with just the account's own AMIs
// rather than failing the whole launch over an enhancement.
func imagesWithOfficialUbuntu(ctx context.Context, clients map[string]awsclient.EC2API, images []inventory.Image) []inventory.Image {
	ubuntuImages, err := listOfficialUbuntuAMIs(ctx, clients)
	if err != nil {
		return images
	}
	return append(append([]inventory.Image{}, images...), ubuntuImages...)
}
