package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestParseSnapshotTimestamp(t *testing.T) {
	got, err := parseSnapshotTimestamp("rdm-20260728-153000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 7, 28, 15, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSnapshotTimestamp_MalformedNameErrors(t *testing.T) {
	if _, err := parseSnapshotTimestamp("not-a-snapshot-name"); err == nil {
		t.Fatal("expected an error for a malformed name")
	}
}

func TestFilterOlderThan(t *testing.T) {
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	prefixes := []SnapshotPrefixInfo{
		{Name: "rdm-20260728-000000", CreatedAt: now.AddDate(0, 0, -1)},  // 1 day old
		{Name: "rdm-20260620-000000", CreatedAt: now.AddDate(0, 0, -39)}, // 39 days old
		{Name: "rdm-20260629-000000", CreatedAt: now.AddDate(0, 0, -30)}, // exactly 30 days old (boundary)
	}

	if got := FilterOlderThan(prefixes, 90, now); len(got) != 0 {
		t.Errorf("threshold 90: got %d matches, want 0 (nothing matches)", len(got))
	}
	if got := FilterOlderThan(prefixes, 0, now); len(got) != 3 {
		t.Errorf("threshold 0: got %d matches, want 3 (everything matches)", len(got))
	}
	got := FilterOlderThan(prefixes, 30, now)
	if len(got) != 1 || got[0].Name != "rdm-20260620-000000" {
		t.Errorf("threshold 30 (boundary-exact-N-days): got %v, want only the 39-day-old entry (exactly-30 is not strictly older)", got)
	}
}

func TestListArchivedSnapshotPrefixes_SkipsUnparseableNameNotFatal(t *testing.T) {
	s3Client := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-20260728-153000/index-0")},
		{Key: aws.String("caltechauthors/opensearch-snapshots/not-a-valid-name/index-0")},
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-20260729-000000/index-0")},
	}}

	got, err := ListArchivedSnapshotPrefixes(context.Background(), s3Client, "my-bucket", "caltechauthors")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d prefixes, want 2 (malformed name skipped, not fatal): %v", len(got), got)
	}
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["rdm-20260728-153000"] || !names["rdm-20260729-000000"] {
		t.Errorf("got names %v, want rdm-20260728-153000 and rdm-20260729-000000", names)
	}
}

func TestListArchivedSnapshotPrefixes_PropagatesListError(t *testing.T) {
	s3Client := &fakeS3Client{listObjectsV2Err: errors.New("boom")}
	_, err := ListArchivedSnapshotPrefixes(context.Background(), s3Client, "my-bucket", "caltechauthors")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDeleteSnapshotPrefixes_BatchesOver1000Keys(t *testing.T) {
	var objects []s3types.Object
	for i := range 1500 {
		objects = append(objects, s3types.Object{Key: aws.String(fmt.Sprintf("caltechauthors/opensearch-snapshots/rdm-20260728-153000/seg-%d", i))})
	}
	s3Client := &fakeS3Client{allObjects: objects}

	err := DeleteSnapshotPrefixes(context.Background(), s3Client, "my-bucket", "caltechauthors", []SnapshotPrefixInfo{{Name: "rdm-20260728-153000"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s3Client.deleteObjectsCalls) != 2 {
		t.Fatalf("deleteObjectsCalls = %d, want 2 (1500 keys batched at 1000 max per call)", len(s3Client.deleteObjectsCalls))
	}
	if len(s3Client.deleteObjectsCalls[0].Delete.Objects) != 1000 {
		t.Errorf("first batch = %d keys, want 1000", len(s3Client.deleteObjectsCalls[0].Delete.Objects))
	}
	if len(s3Client.deleteObjectsCalls[1].Delete.Objects) != 500 {
		t.Errorf("second batch = %d keys, want 500", len(s3Client.deleteObjectsCalls[1].Delete.Objects))
	}
}

func TestDeleteSnapshotPrefixes_MultiplePrefixesAllRemoved(t *testing.T) {
	s3Client := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-1/a")},
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-2/a")},
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-2/b")},
	}}

	err := DeleteSnapshotPrefixes(context.Background(), s3Client, "my-bucket", "caltechauthors", []SnapshotPrefixInfo{{Name: "rdm-1"}, {Name: "rdm-2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var deletedKeys []string
	for _, call := range s3Client.deleteObjectsCalls {
		for _, o := range call.Delete.Objects {
			deletedKeys = append(deletedKeys, aws.ToString(o.Key))
		}
	}
	if len(deletedKeys) != 3 {
		t.Fatalf("deleted %d keys, want 3 (all objects across both prefixes): %v", len(deletedKeys), deletedKeys)
	}
}
