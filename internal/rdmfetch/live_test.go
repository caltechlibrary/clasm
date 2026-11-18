package rdmfetch

import (
	"context"
	"os"
	"testing"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// TestLive_ListsRealBackups lists, and only lists, a real bucket with the
// default AWS profile. It downloads nothing: a dump and a snapshot hold user
// data. Set CLASM_LIVE_S3_SQL_BUCKET, CLASM_LIVE_S3_OS_BUCKET and
// CLASM_LIVE_S3_SOURCE (and optionally CLASM_LIVE_S3_REGION) to run it.
func TestLive_ListsRealBackups(t *testing.T) {
	sqlBucket, osBucket, source := os.Getenv("CLASM_LIVE_S3_SQL_BUCKET"), os.Getenv("CLASM_LIVE_S3_OS_BUCKET"), os.Getenv("CLASM_LIVE_S3_SOURCE")
	if sqlBucket == "" || osBucket == "" || source == "" {
		t.Skip("set CLASM_LIVE_S3_SQL_BUCKET, CLASM_LIVE_S3_OS_BUCKET and CLASM_LIVE_S3_SOURCE to run this")
	}
	region := os.Getenv("CLASM_LIVE_S3_REGION")
	if region == "" {
		region = "us-west-2"
	}
	client, err := awsclient.NewS3Client(context.Background(), region)
	if err != nil {
		t.Fatal(err)
	}
	fe := &Fetcher{S3: client, CacheDir: t.TempDir()}
	dumps, err := fe.ListDumps(context.Background(), sqlBucket, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d dumps; newest %v", len(dumps), func() string {
		if len(dumps) == 0 {
			return "none"
		}
		return dumps[0].Key
	}())
	snaps, err := fe.ListSnapshots(context.Background(), osBucket, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d snapshots; newest %v", len(snaps), snaps)
}
