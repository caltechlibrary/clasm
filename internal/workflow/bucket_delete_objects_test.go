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

func TestDeleteObjectsByPrefix_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestDeleteObjectsByPrefix_NoObjectsFound(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "logs/\n" // pick bucket, key prefix
	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No objects found") {
		t.Errorf("expected a no-objects message, got:\n%s", buf.String())
	}
}

func TestDeleteObjectsByPrefix_ConfirmedDeletesMatchingObjectsUnderPrefix(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("logs/a")},
		{Key: aws.String("logs/b")},
		{Key: aws.String("data/c")},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "logs/\n" + "logs/\n" // pick bucket, key prefix, type the prefix to confirm
	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Deleted 2 object(s)") {
		t.Errorf("expected a deleted-count message, got:\n%s", buf.String())
	}
	if len(fake.deleteObjectCalls) != 2 {
		t.Fatalf("deleteObjectCalls = %+v, want 2", fake.deleteObjectCalls)
	}
	deletedKeys := map[string]bool{}
	for _, c := range fake.deleteObjectCalls {
		deletedKeys[aws.ToString(c.Key)] = true
	}
	if !deletedKeys["logs/a"] || !deletedKeys["logs/b"] || deletedKeys["data/c"] {
		t.Errorf("deleted keys = %v, want exactly logs/a and logs/b", deletedKeys)
	}
}

func TestDeleteObjectsByPrefix_BlankPrefixRequiresBucketNameConfirmation(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("a")}, {Key: aws.String("b")}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "\n" + "my-bucket\n" // pick bucket, blank prefix (whole bucket), confirm with bucket name
	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteObjectCalls) != 2 {
		t.Fatalf("deleteObjectCalls = %+v, want 2", fake.deleteObjectCalls)
	}
}

func TestDeleteObjectsByPrefix_WrongConfirmationCancels(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{{Key: aws.String("logs/a")}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + "logs/\n" + "not-the-prefix\n"
	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancellation message, got:\n%s", buf.String())
	}
	if len(fake.deleteObjectCalls) != 0 {
		t.Errorf("deleteObjectCalls = %d, want 0", len(fake.deleteObjectCalls))
	}
}

func TestDeleteObjectsByPrefix_CancellationAtBucketPick(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := DeleteObjectsByPrefix(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
}
