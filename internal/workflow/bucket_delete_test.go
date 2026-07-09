package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestDeleteBucket_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := DeleteBucket(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestDeleteBucket_RefusesNonEmptyBucket(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("a")}, {Key: aws.String("b")}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	term, le, buf := newPipeEditor(t, "1\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not empty (2 object(s))") {
		t.Errorf("expected a not-empty message with the object count, got:\n%s", buf.String())
	}
	if len(fake.deleteBucketCalls) != 0 {
		t.Errorf("deleteBucketCalls = %d, want 0", len(fake.deleteBucketCalls))
	}
}

func TestDeleteBucket_ConfirmedDeletesEmptyBucket(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "my-bucket\n" // pick bucket, type its name to confirm
	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteBucketCalls) != 1 || aws.ToString(fake.deleteBucketCalls[0].Bucket) != "my-bucket" {
		t.Fatalf("deleteBucketCalls = %+v, want one call for my-bucket", fake.deleteBucketCalls)
	}
}

func TestDeleteBucket_WrongConfirmationCancels(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "not-the-bucket-name\n"
	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancellation message, got:\n%s", buf.String())
	}
	if len(fake.deleteBucketCalls) != 0 {
		t.Errorf("deleteBucketCalls = %d, want 0", len(fake.deleteBucketCalls))
	}
}

func TestDeleteBucket_CancellationAtBucketPick(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
}
