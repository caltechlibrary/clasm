package workflow

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

func TestCreateBucket_InvalidNameNeverCallsAWS(t *testing.T) {
	fake := &fakeS3Client{}
	input := "Bad_Name\n" + // uppercase/underscore -- rejected locally, re-prompt
		"valid-bucket-name\n" +
		"1\n" + // region
		"1\n" // purpose

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := CreateBucket(context.Background(), term, le, newClient, []string{"us-west-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.createBucketCalls) != 1 {
		t.Fatalf("createBucketCalls = %d, want 1 (only after a valid name)", len(fake.createBucketCalls))
	}
	if got := aws.ToString(fake.createBucketCalls[0].Bucket); got != "valid-bucket-name" {
		t.Errorf("created bucket name = %q, want %q", got, "valid-bucket-name")
	}
	if buf.String() == "" {
		t.Errorf("expected the invalid-name rejection to be shown")
	}
}

func TestCreateBucket_SuccessPath(t *testing.T) {
	fake := &fakeS3Client{}
	input := "my-website-bucket\n" +
		"2\n" + // us-west-2 (second of two regions)
		"1\n" // "website" (first of three purposes)

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := CreateBucket(context.Background(), term, le, newClient, []string{"us-east-1", "us-west-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.createBucketCalls) != 1 {
		t.Fatalf("createBucketCalls = %d, want 1", len(fake.createBucketCalls))
	}
	if got := aws.ToString(fake.createBucketCalls[0].Bucket); got != "my-website-bucket" {
		t.Errorf("bucket name = %q, want %q", got, "my-website-bucket")
	}

	if len(fake.putPublicAccessBlockCalls) != 1 {
		t.Fatalf("putPublicAccessBlockCalls = %d, want 1", len(fake.putPublicAccessBlockCalls))
	}
	block := fake.putPublicAccessBlockCalls[0].PublicAccessBlockConfiguration
	if block == nil || !aws.ToBool(block.BlockPublicAcls) || !aws.ToBool(block.BlockPublicPolicy) || !aws.ToBool(block.IgnorePublicAcls) || !aws.ToBool(block.RestrictPublicBuckets) {
		t.Errorf("PutPublicAccessBlock config = %+v, want all four settings true", block)
	}

	if len(fake.putBucketTaggingCalls) != 1 {
		t.Fatalf("putBucketTaggingCalls = %d, want 1", len(fake.putBucketTaggingCalls))
	}
	tagSet := fake.putBucketTaggingCalls[0].Tagging.TagSet
	if len(tagSet) != 1 || aws.ToString(tagSet[0].Key) != "Purpose" || aws.ToString(tagSet[0].Value) != "website" {
		t.Errorf("tag set = %+v, want one Purpose=website tag", tagSet)
	}
}

func TestCreateBucket_UsEast1OmitsLocationConstraint(t *testing.T) {
	fake := &fakeS3Client{}
	input := "my-bucket\n" +
		"1\n" + // us-east-1
		"1\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := CreateBucket(context.Background(), term, le, newClient, []string{"us-east-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg := fake.createBucketCalls[0].CreateBucketConfiguration; cfg != nil {
		t.Errorf("CreateBucketConfiguration = %+v, want nil for us-east-1", cfg)
	}
}

func TestCreateBucket_NonDefaultRegionSetsLocationConstraint(t *testing.T) {
	fake := &fakeS3Client{}
	input := "my-bucket\n" +
		"1\n" + // us-west-2
		"1\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := CreateBucket(context.Background(), term, le, newClient, []string{"us-west-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := fake.createBucketCalls[0].CreateBucketConfiguration
	if cfg == nil || string(cfg.LocationConstraint) != "us-west-2" {
		t.Errorf("CreateBucketConfiguration = %+v, want LocationConstraint us-west-2", cfg)
	}
}

func TestCreateBucket_RegionCancellationAbortsCleanly(t *testing.T) {
	fake := &fakeS3Client{}
	input := "my-bucket\n" +
		"0\n" // cancel the region pick

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := CreateBucket(context.Background(), term, le, newClient, []string{"us-west-2"})
	if err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
	if len(fake.createBucketCalls) != 0 {
		t.Errorf("createBucketCalls = %d, want 0 after cancelling", len(fake.createBucketCalls))
	}
}
