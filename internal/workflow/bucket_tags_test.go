package workflow

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// statefulTagsFakeS3Client is a minimal stateful fake, distinct from
// the shared fakeS3Client (which just echoes back whatever f.tagSet
// was configured with once, regardless of any PutBucketTagging/
// DeleteBucketTagging call) -- applyBucketTagChange's read-modify-
// write needs a *second* GetBucketTagging (triggered by its own
// fetch-then-write sequence) to actually reflect a prior
// PutBucketTagging/DeleteBucketTagging call, which the shared fake
// was never built to simulate. Mirrors statefulTagsFakeEC2Client
// (manage_tags_test.go).
type statefulTagsFakeS3Client struct {
	awsclient.S3API
	tags                     map[string]string
	deleteBucketTaggingCalls int
	putBucketTaggingCalls    int
}

func (f *statefulTagsFakeS3Client) GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	if f.tags == nil {
		return nil, awsAPIError("NoSuchTagSet")
	}
	tagSet := make([]types.Tag, 0, len(f.tags))
	for k, v := range f.tags {
		tagSet = append(tagSet, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return &s3.GetBucketTaggingOutput{TagSet: tagSet}, nil
}

func (f *statefulTagsFakeS3Client) PutBucketTagging(ctx context.Context, params *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error) {
	f.putBucketTaggingCalls++
	f.tags = s3TagsToMap(params.Tagging.TagSet)
	return &s3.PutBucketTaggingOutput{}, nil
}

func (f *statefulTagsFakeS3Client) DeleteBucketTagging(ctx context.Context, params *s3.DeleteBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketTaggingOutput, error) {
	f.deleteBucketTaggingCalls++
	f.tags = nil
	return &s3.DeleteBucketTaggingOutput{}, nil
}

func TestFetchBucketTags(t *testing.T) {
	fake := &fakeS3Client{tagSet: []types.Tag{
		{Key: aws.String("Purpose"), Value: aws.String("backup")},
		{Key: aws.String("Project"), Value: aws.String("caltechauthors")},
	}}
	got, err := fetchBucketTags(context.Background(), fake, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"Purpose": "backup", "Project": "caltechauthors"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFetchBucketTags_NoSuchTagSetIsEmptyNotError(t *testing.T) {
	fake := &fakeS3Client{getBucketTaggingErr: awsAPIError("NoSuchTagSet")}
	got, err := fetchBucketTags(context.Background(), fake, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}

func TestFetchBucketTags_PropagatesOtherErrors(t *testing.T) {
	fake := &fakeS3Client{getBucketTaggingErr: awsAPIError("AccessDenied")}
	_, err := fetchBucketTags(context.Background(), fake, "my-bucket")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestApplyBucketTagChange_Add(t *testing.T) {
	fake := &statefulTagsFakeS3Client{tags: map[string]string{"Purpose": "backup"}}
	err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "add", Key: "Owner", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"Purpose": "backup", "Owner": "dld"}
	if !reflect.DeepEqual(fake.tags, want) {
		t.Errorf("tags = %+v, want %+v", fake.tags, want)
	}
	if fake.putBucketTaggingCalls != 1 {
		t.Errorf("putBucketTaggingCalls = %d, want 1", fake.putBucketTaggingCalls)
	}
}

func TestApplyBucketTagChange_Update(t *testing.T) {
	fake := &statefulTagsFakeS3Client{tags: map[string]string{"Environment": "production"}}
	err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "update", Key: "Environment", Value: "development"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.tags["Environment"] != "development" {
		t.Errorf("tags = %+v, want Environment=development", fake.tags)
	}
}

// TestApplyBucketTagChange_RemoveLastTagUsesDeleteBucketTagging is the
// direct proof for the DeleteBucketTagging precedent (bucket_tags.go's
// own doc comment): removing a bucket's only remaining tag must not
// call PutBucketTagging with an empty TagSet.
func TestApplyBucketTagChange_RemoveLastTagUsesDeleteBucketTagging(t *testing.T) {
	fake := &statefulTagsFakeS3Client{tags: map[string]string{"Owner": "dld"}}
	err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "remove", Key: "Owner"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.deleteBucketTaggingCalls != 1 {
		t.Errorf("deleteBucketTaggingCalls = %d, want 1", fake.deleteBucketTaggingCalls)
	}
	if fake.putBucketTaggingCalls != 0 {
		t.Errorf("putBucketTaggingCalls = %d, want 0 (must not PutBucketTagging an empty TagSet)", fake.putBucketTaggingCalls)
	}
	if len(fake.tags) != 0 {
		t.Errorf("tags = %+v, want empty", fake.tags)
	}
}

func TestApplyBucketTagChange_RemoveOneOfSeveralTagsUsesPutBucketTagging(t *testing.T) {
	fake := &statefulTagsFakeS3Client{tags: map[string]string{"Owner": "dld", "Project": "caltechauthors"}}
	err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "remove", Key: "Owner"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"Project": "caltechauthors"}
	if !reflect.DeepEqual(fake.tags, want) {
		t.Errorf("tags = %+v, want %+v", fake.tags, want)
	}
	if fake.putBucketTaggingCalls != 1 || fake.deleteBucketTaggingCalls != 0 {
		t.Errorf("putBucketTaggingCalls=%d deleteBucketTaggingCalls=%d, want 1/0", fake.putBucketTaggingCalls, fake.deleteBucketTaggingCalls)
	}
}

// TestApplyBucketTagChange_AddThenUpdateRoundTrips is the actual
// read-modify-write proof: Update only succeeds in finding the key
// Add just wrote if applyBucketTagChange's own fetch-modify-write
// sequence round-trips through the fake's state correctly, the same
// role TestManageTags_LoopRefreshesTagsAfterChange plays for EC2.
func TestApplyBucketTagChange_AddThenUpdateRoundTrips(t *testing.T) {
	fake := &statefulTagsFakeS3Client{}
	if err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "add", Key: "Project", Value: "caltechauthors"}); err != nil {
		t.Fatalf("unexpected error on add: %v", err)
	}
	if err := applyBucketTagChange(context.Background(), fake, TagChangeParams{ResourceID: "my-bucket", Action: "update", Key: "Project", Value: "caltechdata"}); err != nil {
		t.Fatalf("unexpected error on update: %v", err)
	}
	if fake.tags["Project"] != "caltechdata" {
		t.Errorf("tags = %+v, want Project=caltechdata", fake.tags)
	}
}
