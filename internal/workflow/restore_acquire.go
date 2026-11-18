package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// SQLAcquirer puts a SQL backup on the host and returns the path of the plain
// SQL file there (DR-0207). It runs before anything is dropped, so a failure
// leaves the database untouched.
type SQLAcquirer func(ctx context.Context, ex Executor) (sqlPath string, err error)

// SnapshotAcquirer makes an OpenSearch snapshot's files present in directory on
// the host (DR-0207). The restore hands them to the service user afterwards.
type SnapshotAcquirer func(ctx context.Context, ex Executor, directory string) error

// SQLRestoreParams is what RestoreSQLOnHost needs beyond the host and the
// acquirer.
type SQLRestoreParams struct {
	ContainerName string
	DBName        string
	DBUser        string
	Bucket        string // the backup's S3 bucket, for messages
	Key           string // the backup's S3 key, for messages
}

// OpenSearchRestoreParams is what RestoreOpenSearchOnHost needs beyond the host
// and the acquirer.
type OpenSearchRestoreParams struct {
	HostName    string // the instance or VM name, for the index checks and messages; required
	Owner       ServiceOwner
	Directory   string
	Bucket      string
	SourceName  string
	IndexPrefix string
	Snapshot    SnapshotPrefixInfo
	Indices     []string
	Existing    []string
}

// awsSQLAcquirer is the AWS acquirer: the backup is downloaded, and
// decompressed if need be, on the instance, under its own IAM role.
func awsSQLAcquirer(bucket, key string) SQLAcquirer {
	return func(ctx context.Context, ex Executor) (string, error) {
		return downloadAndDecompressSQLBackup(ctx, ex, bucket, key, DefaultSQLRestoreTimeout, DefaultSSMPollInterval)
	}
}

// awsSnapshotAcquirer is the AWS acquirer: the snapshot is synced down from S3
// on the instance, under its own IAM role.
func awsSnapshotAcquirer(bucket, sourceName, snapshotName string) SnapshotAcquirer {
	return func(ctx context.Context, ex Executor, directory string) error {
		return SyncOpenSearchBackupsFromS3(ctx, ex, bucket, sourceName, snapshotName, directory, DefaultOpenSearchSyncTimeout, DefaultSSMPollInterval)
	}
}

// RestoreSQLOnHost runs a confirmed SQL restore on any host: acquire puts the
// backup there, then the database is dropped, created, loaded and counted, and
// the scratch files removed (DR-0207). The DR-0187 file-location guard is not
// run: it judges an instance's IAM role, which a Multipass VM does not have.
//
// @param ctx {context.Context} cancels the restore
// @param w {io.Writer} receives progress and the result
// @param ex {Executor} reaches the host
// @param p {SQLRestoreParams} the database and the backup's S3 name, for messages
// @param acquire {SQLAcquirer} puts the backup on the host
// @returns {error} why the restore failed; nothing is dropped before the backup is on the host
// @example
//
//	err := RestoreSQLOnHost(ctx, os.Stdout, ex, SQLRestoreParams{ContainerName: "db-1", DBName: "caltechauthors", DBUser: "postgres"}, acquire)
func RestoreSQLOnHost(ctx context.Context, w io.Writer, ex Executor, p SQLRestoreParams, acquire SQLAcquirer) error {
	return executeSQLRestoreOn(ctx, w, ex, sqlRestoreRun{
		inst:          inventory.Instance{InstanceID: ex.Name(), Name: ex.Name()},
		containerName: p.ContainerName, dbName: p.DBName, dbUser: p.DBUser, bucket: p.Bucket, object: S3Object{Key: p.Key},
	}, acquire)
}

// RestoreOpenSearchOnHost runs a confirmed OpenSearch restore on any host:
// acquire makes the snapshot's files present, then they are handed to the
// service user, registered, checked, restored, verified and cleaned up, and the
// post-restore repair runs (DR-0207).
//
// @param ctx {context.Context} cancels the restore
// @param w {io.Writer} receives progress and the result
// @param ex {Executor} reaches the host
// @param p {OpenSearchRestoreParams} the repository directory, the snapshot and the indices
// @param acquire {SnapshotAcquirer} puts the snapshot's files in the directory
// @returns {error} why the restore failed; no index is deleted before the snapshot is registered and checked
// @example
//
//	err := RestoreOpenSearchOnHost(ctx, os.Stdout, ex, params, acquire)
func RestoreOpenSearchOnHost(ctx context.Context, w io.Writer, ex Executor, p OpenSearchRestoreParams, acquire SnapshotAcquirer) error {
	name := p.HostName
	return executeOpenSearchRestoreOn(ctx, w, ex, openSearchRestoreRun{
		inst:  inventory.Instance{InstanceID: ex.Name(), Name: name},
		owner: p.Owner, directory: p.Directory, bucket: p.Bucket, sourceName: p.SourceName,
		indexPrefix: p.IndexPrefix, snap: p.Snapshot, indices: p.Indices, existing: p.Existing,
	}, acquire)
}

// The fixed scratch files a restore uses on the host. The cleanup step removes
// exactly these two, so an acquirer for another host must use them too.
const (
	RestoreDownloadPath = remoteRestoreDownloadPath
	RestoreSQLPath      = remoteRestoreSQLPath
)

// NeedsDecompression reports whether a backup key names a gzip file, by suffix.
//
// @param key {string} the backup's S3 key or file name
// @returns {bool} true for a .gz name
// @example
//
//	NeedsDecompression("db-2026-11-17.sql.gz") // true
func NeedsDecompression(key string) bool { return needsDecompression(key) }

// DecompressRestoreDownloadCommand is the step of the on-host download that
// turns RestoreDownloadPath into plain SQL at RestoreSQLPath, for an acquirer
// that put the compressed file there itself.
//
// @returns {string} the shell command
// @example
//
//	out, status, err := ex.Run(ctx, DecompressRestoreDownloadCommand(), timeout, poll)
func DecompressRestoreDownloadCommand() string {
	return fmt.Sprintf("{ set -e; gunzip -c %s > %s; } 2>&1", shellQuote(RestoreDownloadPath), shellQuote(RestoreSQLPath))
}
