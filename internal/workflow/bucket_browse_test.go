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

func TestBrowseBucketObjects_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestBrowseBucketObjects_NoObjectsFound(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" // pick bucket, blank prefix

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No objects found") {
		t.Errorf("expected a no-objects message, got:\n%s", buf.String())
	}
}

func TestBrowseBucketObjects_PrefixFilterNarrowsTheCall(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("a/1.txt"), Size: aws.Int64(1)},
		{Key: aws.String("b/1.txt"), Size: aws.Int64(1)},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "a/\n" + "1\n" + "3\n" // pick bucket, prefix "a/", pick the one match, Back

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.listObjectsV2Calls) == 0 || aws.ToString(fake.listObjectsV2Calls[0].Prefix) != "a/" {
		t.Fatalf("expected ListObjectsV2 to be called with Prefix=a/, got calls: %+v", fake.listObjectsV2Calls)
	}
}

func TestBrowseBucketObjects_ShowMetadata(t *testing.T) {
	fake := &fakeS3Client{
		allObjects: []types.Object{{Key: aws.String("file.txt"), Size: aws.Int64(1024)}},
		objects:    map[string]int64{"file.txt": 1024},
	}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "1\n" + "1\n" // pick bucket, no prefix, pick object, Show metadata

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.headObjectCalls != 1 {
		t.Errorf("headObjectCalls = %d, want 1", fake.headObjectCalls)
	}
	if !strings.Contains(buf.String(), "file.txt") {
		t.Errorf("expected metadata output to mention the object key, got:\n%s", buf.String())
	}
}

func TestBrowseBucketObjects_DeleteConfirmed(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("file.txt"), Size: aws.Int64(1024)}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "1\n" + "2\n" + "y\n" // pick bucket, no prefix, pick object, Delete, confirm

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteObjectCalls) != 1 || aws.ToString(fake.deleteObjectCalls[0].Key) != "file.txt" {
		t.Fatalf("deleteObjectCalls = %+v, want one call for file.txt", fake.deleteObjectCalls)
	}
}

func TestBrowseBucketObjects_DeleteDeclined(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("file.txt"), Size: aws.Int64(1024)}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "1\n" + "2\n" + "n\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteObjectCalls) != 0 {
		t.Errorf("deleteObjectCalls = %d, want 0 after declining", len(fake.deleteObjectCalls))
	}
}

func TestBrowseBucketObjects_Back(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("file.txt"), Size: aws.Int64(1024)}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "1\n" + "3\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.headObjectCalls != 0 || len(fake.deleteObjectCalls) != 0 {
		t.Errorf("expected Back to take no action, got headObjectCalls=%d deleteObjectCalls=%d", fake.headObjectCalls, len(fake.deleteObjectCalls))
	}
}

func TestBrowseBucketObjects_FollowsListObjectsV2Pagination(t *testing.T) {
	fake := &fakeS3Client{
		allObjects: []types.Object{
			{Key: aws.String("a"), Size: aws.Int64(1)},
			{Key: aws.String("b"), Size: aws.Int64(1)},
			{Key: aws.String("c"), Size: aws.Int64(1)},
		},
		listObjectsPageSize: 1,
	}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "3\n" + "3\n" // pick bucket, no prefix, pick 3rd (last) object, Back

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.listObjectsV2Calls) != 3 {
		t.Errorf("listObjectsV2Calls = %d, want 3 (one per page)", len(fake.listObjectsV2Calls))
	}
}

func TestBrowseBucketObjects_CancellationAbortsCleanly(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := BrowseBucketObjects(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
}
