package workflow

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// DeleteObjectsByPrefix runs the S3 domain's bulk object-delete workflow,
// for removing many objects without needing a local directory to diff
// against the way Sync Local Directory to Bucket requires. Pick a bucket,
// prompt a key prefix (blank means every object in the bucket), list the
// matches and show the count, then gate the irreversible deletion with
// ConfirmDestructive -- the operator types the prefix back (or the
// bucket name, for a blank/whole-bucket prefix) -- before deleting every
// match via one s3:DeleteObject call per key, the same per-key approach
// Sync's own bulk delete already uses.
func DeleteObjectsByPrefix(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
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

	prefix, err := ui.Prompt(t, le, "Delete objects under key prefix (blank for the whole bucket)")
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

	confirmTarget := prefix
	if confirmTarget == "" {
		confirmTarget = bucket.Name
	}
	t.Printf("This permanently deletes %d object(s) from %s. This cannot be undone.\n", len(objects), bucket.Name)
	t.Refresh()
	ok, err := ConfirmDestructive(t, le, confirmTarget)
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	var deleted, failed int
	for _, obj := range objects {
		key := aws.ToString(obj.Key)
		if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket.Name), Key: aws.String(key)}); err != nil {
			t.Printf("  FAIL deleting %s: %v\n", key, err)
			t.Refresh()
			failed++
			continue
		}
		deleted++
	}

	t.Printf("Deleted %d object(s)", deleted)
	if failed > 0 {
		t.Printf(", %d failed", failed)
	}
	t.Println(".")
	t.Refresh()
	return nil
}
