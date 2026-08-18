package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestListObjectsByPrefix_Pagination(t *testing.T) {
	fake := &fakeS3Client{
		listObjectsPageSize: 1,
		allObjects: []types.Object{
			{Key: aws.String("new-data/a.sql.gz"), Size: aws.Int64(10), LastModified: aws.Time(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))},
			{Key: aws.String("new-data/b.sql.gz"), Size: aws.Int64(20), LastModified: aws.Time(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))},
			{Key: aws.String("new-data/c.sql.gz"), Size: aws.Int64(30), LastModified: aws.Time(time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))},
		},
	}

	got, err := ListObjectsByPrefix(context.Background(), fake, "my-bucket", "new-data/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.listObjectsV2Calls) != 3 {
		t.Errorf("listObjectsV2Calls = %d, want 3 (one per page)", len(fake.listObjectsV2Calls))
	}
	if len(got) != 3 {
		t.Fatalf("got %d objects, want 3", len(got))
	}
}

func TestListObjectsByPrefix_SortsByLastModifiedDescending(t *testing.T) {
	fake := &fakeS3Client{
		allObjects: []types.Object{
			{Key: aws.String("new-data/a.sql.gz"), Size: aws.Int64(10), LastModified: aws.Time(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))},
			{Key: aws.String("new-data/b.sql.gz"), Size: aws.Int64(20), LastModified: aws.Time(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))},
			{Key: aws.String("new-data/c.sql.gz"), Size: aws.Int64(30), LastModified: aws.Time(time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))},
		},
	}

	got, err := ListObjectsByPrefix(context.Background(), fake, "my-bucket", "new-data/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"new-data/b.sql.gz", "new-data/a.sql.gz", "new-data/c.sql.gz"}
	if len(got) != len(want) {
		t.Fatalf("got %d objects, want %d", len(got), len(want))
	}
	for i, k := range want {
		if got[i].Key != k {
			t.Errorf("got[%d].Key = %q, want %q (order: %v)", i, got[i].Key, k, got)
		}
	}
}

func TestListObjectsByPrefix_EmptyPrefixReturnsEmptyNotError(t *testing.T) {
	fake := &fakeS3Client{}

	got, err := ListObjectsByPrefix(context.Background(), fake, "my-bucket", "no-such-prefix/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d objects, want 0", len(got))
	}
}

func TestListObjectsByPrefix_PropagatesError(t *testing.T) {
	fake := &fakeS3Client{listObjectsV2Err: errors.New("boom")}

	_, err := ListObjectsByPrefix(context.Background(), fake, "my-bucket", "new-data/")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestPickS3Object_MostRecentIsFirstDefault(t *testing.T) {
	objects := []S3Object{
		{Key: "new-data/b.sql.gz", SizeBytes: 20, LastModified: time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)},
		{Key: "new-data/a.sql.gz", SizeBytes: 10, LastModified: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)},
	}
	var buf strings.Builder
	got, err := pickS3Object(&buf, "Select a backup", "", objects, newHuhAccessibleInput("\n"), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Key != "new-data/b.sql.gz" {
		t.Errorf("got %q, want the first (most recent) entry %q", got.Key, "new-data/b.sql.gz")
	}
}

func TestPickS3Object_CanPickAnyEntry(t *testing.T) {
	objects := []S3Object{
		{Key: "new-data/b.sql.gz", SizeBytes: 20, LastModified: time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)},
		{Key: "new-data/a.sql.gz", SizeBytes: 10, LastModified: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)},
	}
	var buf strings.Builder
	got, err := pickS3Object(&buf, "Select a backup", "", objects, newHuhAccessibleInput("2\n"), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Key != "new-data/a.sql.gz" {
		t.Errorf("got %q, want the second entry %q", got.Key, "new-data/a.sql.gz")
	}
}
