package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// listBucketObjectsWithPrefix lists every object under prefix in bucket
// via s3:ListObjectsV2, following ContinuationToken to page through the
// full result (same pagination-cost caveat as Sync's listAllBucketObjects).
func listBucketObjectsWithPrefix(ctx context.Context, client awsclient.S3API, bucket, prefix string) ([]types.Object, error) {
	var all []types.Object
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, out.Contents...)
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return all, nil
}

func objectLabel(o types.Object) string {
	lastModified := "unknown"
	if o.LastModified != nil {
		lastModified = o.LastModified.Format(time.RFC3339)
	}
	return fmt.Sprintf("%s (%s, modified %s)", aws.ToString(o.Key), formatBytes(aws.ToInt64(o.Size)), lastModified)
}

var objectActions = []string{"Show metadata", "Delete", "Back"}

func objectActionLabel(s string) string { return s }

// BrowseBucketObjects runs the S3 domain's "Browse/Manage Objects"
// workflow (DESIGN.md, Feature 21, plus the approved optional key-prefix
// filter -- this team's real buckets like sql-backups.library.caltech.edu
// hold many objects across many per-instance prefixes): pick a bucket,
// prompt an optional key prefix, list matching objects, pick one, then a
// small sub-menu -- metadata via s3:HeadObject, or delete via a plain
// Confirm (lower blast-radius than Sync's bulk delete) then
// s3:DeleteObject.
func BrowseBucketObjects(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return nil
	}

	bucket, err := ui.PickList(t, le, buckets, bucketLabel, "Select a bucket")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	prefix, err := ui.Prompt(t, le, "Filter by key prefix (blank for all)")
	if err != nil {
		return err
	}

	objects, err := listBucketObjectsWithPrefix(ctx, client, bucket.Name, prefix)
	if err != nil {
		return fmt.Errorf("listing objects in bucket %s: %w", bucket.Name, err)
	}
	if len(objects) == 0 {
		t.Println("No objects found.")
		t.Refresh()
		return nil
	}

	obj, err := ui.PickList(t, le, objects, objectLabel, "Select an object")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	action, err := ui.PickList(t, le, objectActions, objectActionLabel, "Choose an action")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	switch action {
	case "Show metadata":
		return showObjectMetadata(ctx, t, client, bucket.Name, aws.ToString(obj.Key))
	case "Delete":
		return deleteBucketObject(ctx, t, le, client, bucket.Name, aws.ToString(obj.Key))
	default: // "Back"
		return nil
	}
}

func showObjectMetadata(ctx context.Context, t *termlib.Terminal, client awsclient.S3API, bucket, key string) error {
	out, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("getting metadata for %s: %w", key, err)
	}
	lastModified := "unknown"
	if out.LastModified != nil {
		lastModified = out.LastModified.Format(time.RFC3339)
	}
	t.Printf("%s: %s, modified %s, content-type %s\n", key, formatBytes(aws.ToInt64(out.ContentLength)), lastModified, aws.ToString(out.ContentType))
	t.Refresh()
	return nil
}

func deleteBucketObject(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.S3API, bucket, key string) error {
	ok, err := Confirm(t, le, fmt.Sprintf("Delete %s from %s?", key, bucket))
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}
	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	t.Printf("Deleted %s.\n", key)
	t.Refresh()
	return nil
}
