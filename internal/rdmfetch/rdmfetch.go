// Package rdmfetch fetches production RDM backups from S3 onto the local
// machine and verifies them before anything is sent to a VM (DR-0207). It only
// reads S3.
package rdmfetch

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	// ErrNotFound is returned when the object or snapshot does not exist.
	ErrNotFound = errors.New("not found in S3")
	// ErrCorrupt is returned when a download fails a size, checksum or format check.
	ErrCorrupt = errors.New("download failed verification")
)

// S3Reader is the part of the S3 API this package uses. It has no write method,
// so the package cannot write to S3.
type S3Reader interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// Fetcher downloads into CacheDir.
type Fetcher struct {
	S3       S3Reader
	CacheDir string
	Workers  int // concurrent downloads for a snapshot; 0 means 8
}

// Dump is one SQL backup in S3.
type Dump struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// DumpReport describes a verified local copy of a dump.
type DumpReport struct {
	Path        string
	Key         string
	Size        int64
	MD5         string
	MD5Verified bool // false when S3's ETag is a multipart one and cannot be compared
	GzipOK      bool // true when the file is gzip and decompressed cleanly end to end
	Reused      bool
}

// SnapshotReport describes a verified local copy of a snapshot repository.
type SnapshotReport struct {
	Dir     string
	Name    string
	Objects int
	Bytes   int64
	Reused  bool
}
