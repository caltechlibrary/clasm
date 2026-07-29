package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// maxDeleteObjectsBatch is S3's own DeleteObjects limit -- at most 1,000
// keys per call.
const maxDeleteObjectsBatch = 1000

// SnapshotPrefixInfo is one already-archived OpenSearch snapshot's S3
// sub-prefix, as discovered by ListArchivedSnapshotPrefixes.
type SnapshotPrefixInfo struct {
	// Name is the snapshot's own name (e.g. "rdm-20260728-153000"), not
	// the full S3 key prefix.
	Name string
	// CreatedAt is parsed from Name via parseSnapshotTimestamp.
	CreatedAt time.Time
}

// parseSnapshotTimestamp parses a snapshot name in the
// "rdm-20060102-150405" format CreateSnapshot's caller generates
// (opensearch_archive.go) into the time it embeds.
func parseSnapshotTimestamp(name string) (time.Time, error) {
	const prefix = "rdm-"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, fmt.Errorf("snapshot name %q does not start with %q", name, prefix)
	}
	t, err := time.Parse("20060102-150405", strings.TrimPrefix(name, prefix))
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing snapshot name %q: %w", name, err)
	}
	return t, nil
}

// ListArchivedSnapshotPrefixes lists every OpenSearch snapshot
// sub-prefix already archived for instancePrefix under
// "<instancePrefix>/opensearch-snapshots/" in bucket, one entry per
// CommonPrefixes result (Delimiter: "/"). A sub-prefix whose name
// doesn't parse via parseSnapshotTimestamp is skipped, not fatal --
// matches ListBackupFiles' own malformed-line tolerance -- so one
// unexpected object under this prefix doesn't abort the rest of the
// listing.
func ListArchivedSnapshotPrefixes(ctx context.Context, client awsclient.S3API, bucket, instancePrefix string) ([]SnapshotPrefixInfo, error) {
	basePrefix := openSearchSnapshotsPrefix(instancePrefix) + "/"

	var results []SnapshotPrefixInfo
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(basePrefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, cp := range out.CommonPrefixes {
			name := strings.TrimSuffix(strings.TrimPrefix(aws.ToString(cp.Prefix), basePrefix), "/")
			createdAt, err := parseSnapshotTimestamp(name)
			if err != nil {
				continue
			}
			results = append(results, SnapshotPrefixInfo{Name: name, CreatedAt: createdAt})
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	return results, nil
}

// FilterOlderThan returns the subset of prefixes whose CreatedAt is more
// than days old as of now -- pure function, directly analogous to
// FilterByAge.
func FilterOlderThan(prefixes []SnapshotPrefixInfo, days int, now time.Time) []SnapshotPrefixInfo {
	var out []SnapshotPrefixInfo
	cutoff := now.AddDate(0, 0, -days)
	for _, p := range prefixes {
		if p.CreatedAt.Before(cutoff) {
			out = append(out, p)
		}
	}
	return out
}

// DeleteSnapshotPrefixes deletes every object under each of prefixes'
// own S3 sub-prefix (instancePrefix's opensearch-snapshots directory) --
// S3 has no atomic "delete a whole prefix" call, so each prefix is fully
// listed, then its objects are deleted via DeleteObjects in batches of
// up to maxDeleteObjectsBatch keys.
func DeleteSnapshotPrefixes(ctx context.Context, client awsclient.S3API, bucket, instancePrefix string, prefixes []SnapshotPrefixInfo) error {
	for _, p := range prefixes {
		keyPrefix := fmt.Sprintf("%s/%s/", openSearchSnapshotsPrefix(instancePrefix), p.Name)

		var keys []string
		var token *string
		for {
			out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				Prefix:            aws.String(keyPrefix),
				ContinuationToken: token,
			})
			if err != nil {
				return err
			}
			for _, obj := range out.Contents {
				keys = append(keys, aws.ToString(obj.Key))
			}
			if !aws.ToBool(out.IsTruncated) {
				break
			}
			token = out.NextContinuationToken
		}

		for start := 0; start < len(keys); start += maxDeleteObjectsBatch {
			end := min(start+maxDeleteObjectsBatch, len(keys))
			objects := make([]s3types.ObjectIdentifier, len(keys[start:end]))
			for i, k := range keys[start:end] {
				objects[i] = s3types.ObjectIdentifier{Key: aws.String(k)}
			}
			if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &s3types.Delete{Objects: objects},
			}); err != nil {
				return fmt.Errorf("deleting snapshot %q's objects: %w", p.Name, err)
			}
		}
	}
	return nil
}

// displayCleanupDryRun prints the candidate snapshot prefixes about to be
// removed -- mirrors displayBackupDryRun's shape.
func displayCleanupDryRun(w io.Writer, prefixes []SnapshotPrefixInfo) {
	fmt.Fprintln(w, "\n=== DRY RUN: OpenSearch snapshots to remove ===")
	for _, p := range prefixes {
		fmt.Fprintf(w, "  %s  (created %s)\n", p.Name, p.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(w, "Total: %d snapshot(s)\n", len(prefixes))
}
