package workflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// S3Object is one object ListObjectsByPrefix found -- shared by Restore
// SQL Backup (PLAN.md Phase 20.50) and Restore OpenSearch Snapshot
// (Phase 20.51)'s own source-object pickers, not duplicated.
type S3Object struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
}

// ListObjectsByPrefix lists every object under prefix in bucket, sorted
// by LastModified descending (most recent first) -- the same
// ListObjectsV2/ContinuationToken pagination loop as
// ListArchivedSnapshotPrefixes (opensearch_cleanup.go), just without a
// Delimiter, since a restore picks an individual object, not a
// sub-prefix.
func ListObjectsByPrefix(ctx context.Context, client awsclient.S3API, bucket, prefix string) ([]S3Object, error) {
	var objects []S3Object
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			objects = append(objects, S3Object{
				Key:          aws.ToString(obj.Key),
				SizeBytes:    aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].LastModified.After(objects[j].LastModified) })
	return objects, nil
}

// s3ObjectLabel formats one S3Object for pickS3Object's list.
func s3ObjectLabel(o S3Object) string {
	return fmt.Sprintf("%s (%d bytes, %s)", o.Key, o.SizeBytes, o.LastModified.Format("2006-01-02 15:04:05"))
}

// pickS3Object lets the operator pick one of objects (already sorted
// most-recent-first by ListObjectsByPrefix) via a filterable Menu-tier
// huh.Select -- accessible-mode pipe-testable, matching
// promptBackupBucket's own reasoning for staying off the Picker tier
// (this needs to stay embedded inside a larger pipe-testable prompt
// sequence, not a standalone tui.RunPicker call). The most-recent entry
// is naturally first/default-selected, mirroring Phase 20.21's own
// "sorted-to-front is itself the default" precedent -- no separate "use
// latest?" confirm step, while still fully browsable/pickable for any
// other entry.
func pickS3Object(w io.Writer, title, description string, objects []S3Object, input io.Reader, output io.Writer) (S3Object, error) {
	return pickComparable(w, title, description, hintCancel, objects, s3ObjectLabel, input, output)
}
