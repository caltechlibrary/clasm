package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// s3TagsToMap flattens a bucket's full tag set into a map -- the S3
// analog of manage_tags.go's tagsToMap, kept separate since EC2 and S3
// each define their own distinct Tag type.
func s3TagsToMap(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// fetchBucketTags fetches a bucket's current full tag set, treating
// NoSuchTagSet as "no tags yet" rather than an error -- same
// convention as bucketPurpose (internal/inventory/buckets.go), which
// reads only the Purpose tag; this reads the whole set, for the Tag
// Management domain's Manage tags/Show all tags actions
// (DECISIONS.md, "Tag Management: a fourth domain...").
func fetchBucketTags(ctx context.Context, client awsclient.S3API, bucketName string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucketName)})
	if err != nil {
		if isS3ErrorCode(err, "NoSuchTagSet") {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return s3TagsToMap(out.TagSet), nil
}

// applyBucketTagChange is the S3 apply closure (tagApplyFunc,
// manage_tags.go) for the Tag Management domain's "Manage tags"
// action: unlike EC2's CreateTags/DeleteTags, s3:PutBucketTagging
// replaces a bucket's *entire* tag set and has no fine-grained add/
// remove-one-tag call of its own, so this implements the operator-
// facing "add/update/remove one tag" as a transparent read-modify-
// write -- fetch the bucket's current full tag set, change the one
// key params identifies, then write the whole set back (DECISIONS.md,
// "Tag Management: a fourth domain...", "S3 bucket Add/Update/Remove
// is a transparent read-modify-write"). If the change removes the
// bucket's last remaining tag, this calls s3:DeleteBucketTagging
// instead of PutBucketTagging with an empty TagSet -- proactively
// matching ManageBucketLifecyclePolicies' own DeleteBucketLifecycle
// precedent for the same "replace the whole set" operation shape (see
// awsclient.S3API's doc comment); not itself confirmed against real
// AWS yet.
func applyBucketTagChange(ctx context.Context, client awsclient.S3API, params TagChangeParams) error {
	tags, err := fetchBucketTags(ctx, client, params.ResourceID)
	if err != nil {
		return err
	}
	if params.Action == "remove" {
		delete(tags, params.Key)
	} else {
		tags[params.Key] = params.Value
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()

	if len(tags) == 0 {
		_, err := client.DeleteBucketTagging(ctx, &s3.DeleteBucketTaggingInput{Bucket: aws.String(params.ResourceID)})
		return err
	}

	tagSet := make([]types.Tag, 0, len(tags))
	for _, k := range sortedKeys(tags) {
		tagSet = append(tagSet, types.Tag{Key: aws.String(k), Value: aws.String(tags[k])})
	}
	_, err = client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket:  aws.String(params.ResourceID),
		Tagging: &types.Tagging{TagSet: tagSet},
	})
	return err
}
