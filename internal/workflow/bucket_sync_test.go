package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing test fixture %s: %v", name, err)
	}
}

func TestSyncDirectoryToBucket_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestSyncDirectoryToBucket_DryRunDiffCorrectness(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "hello")      // 5 bytes, matches remote -- unchanged
	writeTestFile(t, dir, "b.txt", "0123456789") // 10 bytes, new -- upload candidate

	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("a.txt"), Size: aws.Int64(5)},
		{Key: aws.String("c.txt"), Size: aws.Int64(3)}, // bucket-only -- delete candidate
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" + "n\n" // pick bucket, directory, decline upload

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "b.txt") {
		t.Errorf("expected b.txt listed as an upload candidate, got:\n%s", out)
	}
	if !strings.Contains(out, "c.txt") {
		t.Errorf("expected c.txt listed as a delete candidate, got:\n%s", out)
	}
	if strings.Count(out, "a.txt") != 0 {
		t.Errorf("expected a.txt (unchanged) to not appear in the dry run, got:\n%s", out)
	}
	if len(fake.putObjectCalls) != 0 || len(fake.deleteObjectCalls) != 0 {
		t.Errorf("declining upload must not touch AWS: putObjectCalls=%d deleteObjectCalls=%d", len(fake.putObjectCalls), len(fake.deleteObjectCalls))
	}
}

func TestSyncDirectoryToBucket_DeclineUploadAbortsBeforeDeletePrompt(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "new.txt", "new content")

	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("gone.txt"), Size: aws.Int64(4)},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" + "n\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "type the exact identifier") {
		t.Errorf("expected the delete confirmation to never be reached, got:\n%s", buf.String())
	}
	if len(fake.deleteObjectCalls) != 0 {
		t.Errorf("deleteObjectCalls = %d, want 0", len(fake.deleteObjectCalls))
	}
}

func TestSyncDirectoryToBucket_UploadThenSeparateDeleteConfirm(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "new.txt", "0123456789") // 10 bytes, new

	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("gone.txt"), Size: aws.Int64(4)},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" +
		"y\n" + // confirm upload
		"my-bucket\n" // type-to-confirm the delete

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.putObjectCalls) != 1 || aws.ToString(fake.putObjectCalls[0].Key) != "new.txt" {
		t.Fatalf("putObjectCalls = %+v, want one call for new.txt", fake.putObjectCalls)
	}
	if len(fake.deleteObjectCalls) != 1 || aws.ToString(fake.deleteObjectCalls[0].Key) != "gone.txt" {
		t.Fatalf("deleteObjectCalls = %+v, want one call for gone.txt", fake.deleteObjectCalls)
	}
	if !strings.Contains(buf.String(), "OK new.txt") {
		t.Errorf("expected a per-file OK progress line for new.txt, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "type the exact identifier") {
		t.Errorf("expected the type-to-confirm delete gate to be shown, got:\n%s", buf.String())
	}
}

func TestSyncDirectoryToBucket_NoDeleteCandidatesSkipsDeleteConfirm(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "new.txt", "hi")

	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" + "y\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "type the exact identifier") {
		t.Errorf("expected no delete confirmation when there are no delete candidates, got:\n%s", buf.String())
	}
}

func TestSyncDirectoryToBucket_DeleteOnlyCandidatesStillPromptsWithoutUploadStep(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "kept.txt", "same")

	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("kept.txt"), Size: aws.Int64(4)},
		{Key: aws.String("locally-deleted.txt"), Size: aws.Int64(9)},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" + "my-bucket\n" // no upload prompt expected -- straight to delete confirm

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.putObjectCalls) != 0 {
		t.Errorf("putObjectCalls = %d, want 0 (nothing changed locally)", len(fake.putObjectCalls))
	}
	if len(fake.deleteObjectCalls) != 1 || aws.ToString(fake.deleteObjectCalls[0].Key) != "locally-deleted.txt" {
		t.Fatalf("deleteObjectCalls = %+v, want one call for locally-deleted.txt", fake.deleteObjectCalls)
	}
}

func TestSyncDirectoryToBucket_FollowsListObjectsV2Pagination(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeS3Client{
		allObjects: []types.Object{
			{Key: aws.String("a"), Size: aws.Int64(1)},
			{Key: aws.String("b"), Size: aws.Int64(1)},
			{Key: aws.String("c"), Size: aws.Int64(1)},
		},
		listObjectsPageSize: 1,
	}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	input := "1\n" + dir + "\n" + "my-bucket\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.listObjectsV2Calls) != 3 {
		t.Errorf("listObjectsV2Calls = %d, want 3 (one per page)", len(fake.listObjectsV2Calls))
	}
	if len(fake.deleteObjectCalls) != 3 {
		t.Errorf("deleteObjectCalls = %d, want 3 (all bucket-only across every page)", len(fake.deleteObjectCalls))
	}
}

func TestSyncDirectoryToBucket_InvalidDirectoryRejectedLocally(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	realDir := t.TempDir()
	input := "1\n" + "/no/such/directory\n" + realDir + "\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected the invalid directory to be rejected locally, got:\n%s", buf.String())
	}
}

func TestSyncDirectoryToBucket_CancellationAbortsCleanly(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := SyncDirectoryToBucket(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
}
