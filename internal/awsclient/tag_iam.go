package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// taggingIAMClient adds the Origin tag to every IAM resource clasm creates
// (DR-0189). Every other call is the embedded client's own.
type taggingIAMClient struct {
	IAMAPI
	origin func() (key, value string)
}

// TagIAMCreations returns an IAMAPI that adds the Origin tag to every CreateRole,
// CreatePolicy and CreateInstanceProfile before delegating to client. The tag is how
// clasm decides which roles, profiles and policies to show and manage, and how a
// report finds what it made, so it is added here, once, rather than by each workflow:
// a creator added later is covered without the configuration being passed to it.
// A request that already names the tag keeps its own value and is not tagged twice;
// nothing is added when the key or the value is empty.
//
// @param client {IAMAPI} the client to wrap
// @param origin {func() (key, value string)} the configured Origin tag key and its DLD-owned value, read at each create because Configure can change them while clasm runs
// @returns {IAMAPI} the wrapping client
// @example
//
//	iamClient = TagIAMCreations(iamClient, func() (string, string) { return "origin", "dld" })
func TagIAMCreations(client IAMAPI, origin func() (key, value string)) IAMAPI {
	return &taggingIAMClient{IAMAPI: client, origin: origin}
}

// withOrigin is tags plus the Origin tag, unless the key is already there.
func (c *taggingIAMClient) withOrigin(tags []iamtypes.Tag) []iamtypes.Tag {
	key, value := c.origin()
	if key == "" || value == "" {
		return tags
	}
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return tags
		}
	}
	out := make([]iamtypes.Tag, 0, len(tags)+1)
	out = append(out, tags...)
	return append(out, iamtypes.Tag{Key: aws.String(key), Value: aws.String(value)})
}

func (c *taggingIAMClient) CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	p := *params
	p.Tags = c.withOrigin(params.Tags)
	return c.IAMAPI.CreateRole(ctx, &p, optFns...)
}

func (c *taggingIAMClient) CreatePolicy(ctx context.Context, params *iam.CreatePolicyInput, optFns ...func(*iam.Options)) (*iam.CreatePolicyOutput, error) {
	p := *params
	p.Tags = c.withOrigin(params.Tags)
	return c.IAMAPI.CreatePolicy(ctx, &p, optFns...)
}

func (c *taggingIAMClient) CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	p := *params
	p.Tags = c.withOrigin(params.Tags)
	return c.IAMAPI.CreateInstanceProfile(ctx, &p, optFns...)
}
