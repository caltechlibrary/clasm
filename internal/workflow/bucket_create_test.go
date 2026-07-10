package workflow

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// The region and bucket-purpose pickers converted to huh.Select
// (DESIGN.md's full conversion punch list): their selections are fed via
// a separate newHuhAccessibleInput reader (menuInput), not le, which
// still feeds the bucket-name prompt. Cancelling either picker is only
// reachable via 'q'/ctrl+c, which accessible mode has no keyboard to
// simulate (mapMenuPickerErr's doc comment covers the same limitation),
// so the old "0=Cancel" region-cancellation test is retired rather than
// kept.

func TestCreateBucket_InvalidNameNeverCallsAWS(t *testing.T) {
	fake := &fakeS3Client{}
	input := "Bad_Name\n" + // uppercase/underscore -- rejected locally, re-prompt
		"valid-bucket-name\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := createBucket(context.Background(), term, le, newClient, []string{"us-west-2"}, newHuhAccessibleInput("1\n1\n"), buf) // region, purpose
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
	term, le, buf := newPipeEditor(t, "my-website-bucket\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := createBucket(context.Background(), term, le, newClient, []string{"us-east-1", "us-west-2"}, newHuhAccessibleInput("2\n1\n"), buf) // us-west-2, website
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
	term, le, buf := newPipeEditor(t, "my-bucket\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := createBucket(context.Background(), term, le, newClient, []string{"us-east-1"}, newHuhAccessibleInput("1\n1\n"), buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg := fake.createBucketCalls[0].CreateBucketConfiguration; cfg != nil {
		t.Errorf("CreateBucketConfiguration = %+v, want nil for us-east-1", cfg)
	}
}

func TestCreateBucket_NonDefaultRegionSetsLocationConstraint(t *testing.T) {
	fake := &fakeS3Client{}
	term, le, buf := newPipeEditor(t, "my-bucket\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := createBucket(context.Background(), term, le, newClient, []string{"us-west-2"}, newHuhAccessibleInput("1\n1\n"), buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := fake.createBucketCalls[0].CreateBucketConfiguration
	if cfg == nil || string(cfg.LocationConstraint) != "us-west-2" {
		t.Errorf("CreateBucketConfiguration = %+v, want LocationConstraint us-west-2", cfg)
	}
}
