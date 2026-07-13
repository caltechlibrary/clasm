package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Bucket selection (PLAN.md Phase 20.4) now runs a real bubbletea
// Program (tui.RunPicker), which can't be driven by a test's pipe
// input -- see internal/tui/picker_test.go for that component's own
// thorough test suite. Tests below exercise everything once a bucket
// is already resolved via the unexported deleteBucket;
// DeleteBucket's own picker-selection step is covered only by
// manual/interactive verification, the same accepted limitation
// object_browser.go's huh-based bucket pre-flight already has.

func TestDeleteBucket_NoBucketsFound(t *testing.T) {
	term, _, buf := newPipeEditor("")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := DeleteBucket(context.Background(), term, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestDeleteBucket_RefusesNonEmptyBucket(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("a")}, {Key: aws.String("b")}}}
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2"}
	term, le, buf := newPipeEditor("")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := deleteBucket(context.Background(), term, newClient, bucket, le, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2"}
	term, le, buf := newPipeEditor("my-bucket\n") // type its name to confirm
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := deleteBucket(context.Background(), term, newClient, bucket, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteBucketCalls) != 1 || aws.ToString(fake.deleteBucketCalls[0].Bucket) != "my-bucket" {
		t.Fatalf("deleteBucketCalls = %+v, want one call for my-bucket", fake.deleteBucketCalls)
	}
}

func TestDeleteBucket_WrongConfirmationCancels(t *testing.T) {
	fake := &fakeS3Client{}
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2"}
	term, le, buf := newPipeEditor("not-the-bucket-name\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := deleteBucket(context.Background(), term, newClient, bucket, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancellation message, got:\n%s", buf.String())
	}
	if len(fake.deleteBucketCalls) != 0 {
		t.Errorf("deleteBucketCalls = %d, want 0", len(fake.deleteBucketCalls))
	}
}
