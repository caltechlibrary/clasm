package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// openSearchSnapshotsPrefix returns the S3 key prefix (no trailing
// object name) every archived OpenSearch snapshot for instancePrefix
// lives under -- shared by SyncOpenSearchBackupsToS3, VerifySyncedSnapshot,
// and opensearch_cleanup.go's ListArchivedSnapshotPrefixes, so the layout
// is defined in exactly one place.
func openSearchSnapshotsPrefix(instancePrefix string) string {
	return fmt.Sprintf("%s/opensearch-snapshots", instancePrefix)
}

// buildSyncCommand builds the `aws s3 sync` command that uploads
// localDir's current contents to a new, snapshot-named destination
// prefix -- deliberately no `--delete` and a fresh prefix every run
// (DESIGN.md, "Archive OpenSearch Snapshot to S3", step 7): a shared,
// `--delete`-synced prefix would make S3 mirror EBS's single-snapshot
// state, defeating Restore's "pick a specific dated backup".
func buildSyncCommand(localDir, bucket, prefix, snapshotName string) string {
	dest := fmt.Sprintf("s3://%s/%s/%s/", bucket, openSearchSnapshotsPrefix(prefix), snapshotName)
	return fmt.Sprintf("aws s3 sync --only-show-errors %s %s", shellQuote(localDir), shellQuote(dest))
}

// SyncOpenSearchBackupsToS3 runs buildSyncCommand via SSM, one call for
// the whole directory -- unlike Feature 11's per-file upload loop, `aws
// s3 sync` handles the entire local snapshot repo as a single CLI
// invocation.
func SyncOpenSearchBackupsToS3(ctx context.Context, client awsclient.SSMAPI, instanceID, localDir, bucket, prefix, snapshotName string, timeout, pollInterval time.Duration) error {
	_, status, err := RunShellCommand(ctx, client, instanceID, buildSyncCommand(localDir, bucket, prefix, snapshotName), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("syncing OpenSearch snapshot %q to s3://%s/%s on %s failed (status: %s)", snapshotName, bucket, openSearchSnapshotsPrefix(prefix), instanceID, status)
	}
	return nil
}

// VerifySyncedSnapshot lists the just-synced snapshot's own destination
// prefix via the tool's own credentials (not the instance's self-report)
// and confirms it's non-empty. Deliberately lighter than Feature 11's
// per-file HeadObject reverification -- `aws s3 sync`'s own per-object
// PUT already carries S3's normal strong-consistency guarantee, and
// re-verifying every individual segment file here (there could be many)
// would be prohibitively slow.
func VerifySyncedSnapshot(ctx context.Context, client awsclient.S3API, bucket, prefix, snapshotName string) (objectCount int, totalBytes int64, err error) {
	keyPrefix := fmt.Sprintf("%s/%s/", openSearchSnapshotsPrefix(prefix), snapshotName)

	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(keyPrefix),
			ContinuationToken: token,
		})
		if err != nil {
			return 0, 0, err
		}
		for _, obj := range out.Contents {
			objectCount++
			totalBytes += aws.ToInt64(obj.Size)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}

	if objectCount == 0 {
		return 0, 0, fmt.Errorf("no objects found under s3://%s/%s after sync -- snapshot %q may not have synced correctly", bucket, keyPrefix, snapshotName)
	}
	return objectCount, totalBytes, nil
}
