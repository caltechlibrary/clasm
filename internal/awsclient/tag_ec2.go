package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// taggingEC2Client adds the Origin tag to every EC2 resource clasm creates
// (DR-0190). Every other call is the embedded client's own.
type taggingEC2Client struct {
	EC2API
	origin func() (key, value string)
}

// TagEC2Creations returns an EC2API that adds the Origin tag, the same tag and the
// same way as TagIAMCreations, to what each create call can tag: RunInstances (the
// instance and its volumes and network interfaces), CreateLaunchTemplate (the
// template, and the instance, volume and network interface specifications in its data,
// so whatever launches from it is tagged), CreateImage (the image and its snapshots),
// CreateKeyPair and ImportKeyPair.
//
// A RunInstances that names a launch template is tagged like any other: AWS merges a
// tag specification sent with it into the template's, dropping nothing (checked on a
// real launch, DR-0191), so this also tags launches from templates made before the tag
// existed. CreateLaunchTemplateVersion is deliberately left alone: a version inherits
// its source version's specification, and sending one could replace it.
//
// A specification for the same resource type is merged into, its own tags kept; a tag
// the request already names keeps its own value and is not added twice; nothing is
// added when the key or the value is empty. The caller's request is never modified.
//
// @param client {EC2API} the client to wrap
// @param origin {func() (key, value string)} the configured Origin tag key and its DLD-owned value, read at each create because Configure can change them while clasm runs
// @returns {EC2API} the wrapping client
// @example
//
//	ec2Client = TagEC2Creations(ec2Client, func() (string, string) { return "origin", "dld" })
func TagEC2Creations(client EC2API, origin func() (key, value string)) EC2API {
	return &taggingEC2Client{EC2API: client, origin: origin}
}

// hasKey reports whether tags already names key.
func hasKey(tags []types.Tag, key string) bool {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return true
		}
	}
	return false
}

// plusOrigin is tags, copied, with the Origin tag added unless its key is already
// there.
func plusOrigin(tags []types.Tag, key, value string) []types.Tag {
	out := make([]types.Tag, len(tags), len(tags)+1)
	copy(out, tags)
	if !hasKey(out, key) {
		out = append(out, types.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	return out
}

// withOrigin is specs, copied, with the Origin tag in the specification of each of
// resourceTypes: merged into the one that exists, or added as a new one.
func (c *taggingEC2Client) withOrigin(specs []types.TagSpecification, resourceTypes ...types.ResourceType) []types.TagSpecification {
	key, value := c.origin()
	if key == "" || value == "" {
		return specs
	}
	out := make([]types.TagSpecification, len(specs), len(specs)+len(resourceTypes))
	copy(out, specs)
	for _, rt := range resourceTypes {
		merged := false
		for i := range out {
			if out[i].ResourceType == rt {
				out[i].Tags = plusOrigin(out[i].Tags, key, value)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, types.TagSpecification{ResourceType: rt, Tags: plusOrigin(nil, key, value)})
		}
	}
	return out
}

// withOriginInTemplate is withOrigin for a launch template's own data, which has its
// own specification type.
func (c *taggingEC2Client) withOriginInTemplate(specs []types.LaunchTemplateTagSpecificationRequest, resourceTypes ...types.ResourceType) []types.LaunchTemplateTagSpecificationRequest {
	key, value := c.origin()
	if key == "" || value == "" {
		return specs
	}
	out := make([]types.LaunchTemplateTagSpecificationRequest, len(specs), len(specs)+len(resourceTypes))
	copy(out, specs)
	for _, rt := range resourceTypes {
		merged := false
		for i := range out {
			if out[i].ResourceType == rt {
				out[i].Tags = plusOrigin(out[i].Tags, key, value)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, types.LaunchTemplateTagSpecificationRequest{ResourceType: rt, Tags: plusOrigin(nil, key, value)})
		}
	}
	return out
}

func (c *taggingEC2Client) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	p := *params
	p.TagSpecifications = c.withOrigin(params.TagSpecifications, types.ResourceTypeInstance, types.ResourceTypeVolume, types.ResourceTypeNetworkInterface)
	return c.EC2API.RunInstances(ctx, &p, optFns...)
}

func (c *taggingEC2Client) CreateLaunchTemplate(ctx context.Context, params *ec2.CreateLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	p := *params
	p.TagSpecifications = c.withOrigin(params.TagSpecifications, types.ResourceTypeLaunchTemplate)
	if params.LaunchTemplateData != nil {
		data := *params.LaunchTemplateData
		data.TagSpecifications = c.withOriginInTemplate(data.TagSpecifications, types.ResourceTypeInstance, types.ResourceTypeVolume, types.ResourceTypeNetworkInterface)
		p.LaunchTemplateData = &data
	}
	return c.EC2API.CreateLaunchTemplate(ctx, &p, optFns...)
}

func (c *taggingEC2Client) CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	p := *params
	p.TagSpecifications = c.withOrigin(params.TagSpecifications, types.ResourceTypeImage, types.ResourceTypeSnapshot)
	return c.EC2API.CreateImage(ctx, &p, optFns...)
}

func (c *taggingEC2Client) CreateKeyPair(ctx context.Context, params *ec2.CreateKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	p := *params
	p.TagSpecifications = c.withOrigin(params.TagSpecifications, types.ResourceTypeKeyPair)
	return c.EC2API.CreateKeyPair(ctx, &p, optFns...)
}

func (c *taggingEC2Client) ImportKeyPair(ctx context.Context, params *ec2.ImportKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error) {
	p := *params
	p.TagSpecifications = c.withOrigin(params.TagSpecifications, types.ResourceTypeKeyPair)
	return c.EC2API.ImportKeyPair(ctx, &p, optFns...)
}
