package workflow

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// DeleteBucket runs the S3 domain's "Delete Bucket" workflow: pick a
// bucket, refuse if it isn't empty (deleting a bucket's objects is a
// separate, even more destructive action -- see DeleteObjectsByPrefix --
// that shouldn't be bundled silently into a bucket-delete confirmation),
// then gate the irreversible s3:DeleteBucket call with ConfirmDestructive
// (type the bucket name back), the same heavier confirmation tier used
// for Terminate/Remove AMI/Backup Delete.
func DeleteBucket(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return nil
	}

	bucket, err := ui.PickList(t, le, buckets, bucketLabel, "Select a bucket to delete")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	objects, err := listBucketObjectsWithPrefix(ctx, client, bucket.Name, "")
	if err != nil {
		return fmt.Errorf("checking whether bucket %s is empty: %w", bucket.Name, err)
	}
	if len(objects) > 0 {
		t.Printf("Bucket %s is not empty (%d object(s)) -- empty it first via Delete Objects by Prefix.\n", bucket.Name, len(objects))
		t.Refresh()
		return nil
	}

	t.Printf("This permanently deletes bucket %s. This cannot be undone.\n", bucket.Name)
	t.Refresh()
	ok, err := ConfirmDestructive(t, le, bucket.Name)
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket.Name)}); err != nil {
		return fmt.Errorf("deleting bucket %s: %w", bucket.Name, err)
	}
	t.Printf("Deleted bucket %s.\n", bucket.Name)
	t.Refresh()
	return nil
}
