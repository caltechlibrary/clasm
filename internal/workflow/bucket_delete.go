package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// DeleteBucket runs the S3 domain's "Delete Bucket" workflow: pick a
// bucket, refuse if it isn't empty (deleting a bucket's objects is a
// separate, even more destructive action -- see BrowseAndManageObjects's
// Delete action -- that shouldn't be bundled silently into a
// bucket-delete confirmation), then gate the irreversible
// s3:DeleteBucket call with ConfirmDestructive
// (type the bucket name back), the same heavier confirmation tier used
// for Terminate/Remove AMI/Backup Delete.
//
// Bucket selection runs a real bubbletea Program (tui.RunPicker, PLAN.md
// Phase 20.4) that can't be driven by a test's pipe input, so the rest
// of the workflow lives in the unexported, directly-testable
// deleteBucket.
func DeleteBucket(ctx context.Context, w io.Writer, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		fmt.Fprintln(w, "No buckets found.")
		return nil
	}

	bucket, err := pickBucket(ctx, "Select a bucket to delete", buckets)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return deleteBucket(ctx, w, newS3Client, bucket, nil, nil)
}

// deleteBucket is DeleteBucket's testable core, once a bucket is
// resolved. input/output are nil in production and supplied by tests to
// drive the ConfirmDestructive gate through its accessible-mode pipe
// path instead.
func deleteBucket(ctx context.Context, w io.Writer, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), bucket inventory.Bucket, input io.Reader, output io.Writer) error {
	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	objects, err := listBucketObjectsWithPrefix(ctx, client, bucket.Name, "")
	if err != nil {
		return fmt.Errorf("checking whether bucket %s is empty: %w", bucket.Name, err)
	}
	if len(objects) > 0 {
		fmt.Fprintf(w, "Bucket %s is not empty (%d object(s)) -- empty it first via Browse & Manage Objects.\n", bucket.Name, len(objects))
		return nil
	}

	fmt.Fprintf(w, "This permanently deletes bucket %s. This cannot be undone.\n", bucket.Name)
	ok, err := ConfirmDestructive([]string{bucket.Name}, WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket.Name)}); err != nil {
		return fmt.Errorf("deleting bucket %s: %w", bucket.Name, err)
	}
	fmt.Fprintf(w, "Deleted bucket %s.\n", bucket.Name)
	return nil
}
