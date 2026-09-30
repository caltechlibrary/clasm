package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Found live 2026-09-30: the Source instance name prompt defaults to the
// *target's* Name (caltechauthors-test-v13), the snapshots were archived under
// another instance's (caltechauthors-v13), and Restore said only "No
// OpenSearch snapshots found under s3://.../opensearch-snapshots/." and
// returned success. It must fail, and say which sources in the bucket do hold
// snapshots, since a wrong source is by far the likeliest reason for an empty
// one.

func s3Objects(keys ...string) []s3types.Object {
	var out []s3types.Object
	for _, k := range keys {
		out = append(out, s3types.Object{Key: aws.String(k), Size: aws.Int64(1)})
	}
	return out
}

func TestListSnapshotSources(t *testing.T) {
	s3Client := &fakeS3Client{allObjects: s3Objects(
		"prod-box/opensearch-snapshots/rdm-20260930-123545/index-0",
		"prod-box/opensearch-snapshots/rdm-20260930-123545/indices/a",
		"prod-box/opensearch-snapshots/rdm-20260929-123545/index-0",
		"prod-box/sql-backups/dump.sql.gz",                         // not a snapshot
		"sql-only-box/sql-backups/dump.sql.gz",                     // no snapshots at all
		"odd-box/opensearch-snapshots/not-a-snapshot-name/index-0", // unparseable name, skipped
		"other-box/opensearch-snapshots/rdm-20260101-000000/index-0",
	)}
	got, err := ListSnapshotSources(context.Background(), s3Client, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int{"prod-box": 2, "other-box": 1}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want sources %v", got, want)
	}
	for _, g := range got {
		if want[g.Name] != g.Snapshots {
			t.Errorf("source %q: got %d snapshots, want %d (all: %+v)", g.Name, g.Snapshots, want[g.Name], got)
		}
	}
	if got[0].Name != "other-box" && got[0].Name != "prod-box" {
		t.Errorf("unexpected first source %+v", got)
	}
}

func TestListSnapshotSources_SortedByName(t *testing.T) {
	s3Client := &fakeS3Client{allObjects: s3Objects(
		"b-box/opensearch-snapshots/rdm-20260101-000000/x",
		"a-box/opensearch-snapshots/rdm-20260101-000000/x",
	)}
	got, _ := ListSnapshotSources(context.Background(), s3Client, "my-bucket")
	if len(got) != 2 || got[0].Name != "a-box" || got[1].Name != "b-box" {
		t.Errorf("want sources sorted by name, got %+v", got)
	}
}

func runEmptySourceRestore(t *testing.T, objects []s3types.Object) error {
	t.Helper()
	inst := inventory.Instance{InstanceID: "i-1", Name: "target-box", Region: "us-east-1"}
	// index prefix (default), directory, bucket, source name (the default)
	term, le, buf := newPipeEditor("\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "target-box\n")
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "target-box-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: objects}
	return restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
}

func TestRestoreOpenSearchSnapshot_EmptySourceNamesTheSourcesThatHoldSnapshots(t *testing.T) {
	err := runEmptySourceRestore(t, s3Objects(
		"prod-box/opensearch-snapshots/rdm-20260930-123545/index-0",
		"prod-box/opensearch-snapshots/rdm-20260929-123545/index-0",
	))
	if err == nil {
		t.Fatal("an empty source must be an error, not a silent success")
	}
	for _, want := range []string{"s3://my-bucket/target-box/opensearch-snapshots/", "prod-box", "2 snapshots", "target"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestRestoreOpenSearchSnapshot_EmptyBucketSaysNoSourceHoldsSnapshots(t *testing.T) {
	err := runEmptySourceRestore(t, s3Objects("target-box/sql-backups/dump.sql.gz"))
	if err == nil {
		t.Fatal("an empty source must be an error, not a silent success")
	}
	if !strings.Contains(err.Error(), "no source") || !strings.Contains(err.Error(), "my-bucket") {
		t.Errorf("error should say no source in the bucket holds snapshots, got: %v", err)
	}
}
