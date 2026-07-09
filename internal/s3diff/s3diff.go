// Package s3diff holds the local-directory-vs-bucket diff logic
// originally built for the S3 domain's "Sync Local Directory to Bucket"
// wizard (DESIGN.md, Feature 20) and now shared by the file manager's
// Sync action (DESIGN.md 21.6, PLAN.md Phase 20.1): both
// internal/workflow (that wizard's retirement notwithstanding -- its
// diff/walk/list helpers were explicitly kept, per PLAN.md's Phase 20.1
// work items) and internal/filemanager depend on this package, avoiding
// either a workflow<->filemanager import cycle or a second
// implementation of the same key+size diff.
package s3diff

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// DefaultCallTimeout bounds a single (non-polling) AWS API call, as an
// extra safety net beyond the SDK's own retry/timeout behavior --
// mirrors internal/workflow's own DefaultAWSCallTimeout/withCallTimeout
// (call_timeout.go), duplicated here rather than imported: workflow
// depends on internal/filemanager (via object_browser.go), so
// internal/filemanager (and this package, which it also depends on)
// importing back into workflow would cycle. Without this, a single
// stalled connection -- not just a slow-but-progressing one -- hangs
// the calling goroutine forever, with no way for the operator to
// recover short of killing the whole program (see DECISIONS.md,
// investigated after a report that the file manager "appeared hung"
// after an upload).
// var, not const, so tests can shrink it temporarily to exercise
// timeout-recovery behavior without an actual 30-second wait.
var DefaultCallTimeout = 30 * time.Second

// TransferCallTimeout bounds a single Upload/Download data-transfer call
// (PutObject/GetObject's body read) -- longer than DefaultCallTimeout
// since a large object's transfer time scales with its size and the
// connection's bandwidth, not just request/response latency, and 30s
// would be too tight for a legitimately large (if slow) upload. var,
// not const, for the same test-shrinking reason as DefaultCallTimeout.
var TransferCallTimeout = 5 * time.Minute

// WithCallTimeout returns a context bounded by DefaultCallTimeout,
// derived from ctx, for a single lightweight (metadata/listing/delete)
// AWS API call.
func WithCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultCallTimeout)
}

// WithTransferTimeout returns a context bounded by TransferCallTimeout,
// derived from ctx, for a single Upload/Download data-transfer call.
func WithTransferTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, TransferCallTimeout)
}

// Diff is the result of comparing a local directory tree against a
// bucket's current contents by key and size (not checksum, per
// DESIGN.md): Upload holds keys that are new locally or whose size
// differs from the bucket's copy; Delete holds keys that exist in the
// bucket but have no local counterpart. Both are sorted for a stable,
// readable dry-run listing.
type Diff struct {
	Upload []string
	Delete []string
}

// Compute builds a Diff from local and remote key -> size maps.
func Compute(local, remote map[string]int64) Diff {
	var d Diff
	for key, size := range local {
		if remoteSize, ok := remote[key]; !ok || remoteSize != size {
			d.Upload = append(d.Upload, key)
		}
	}
	for key := range remote {
		if _, ok := local[key]; !ok {
			d.Delete = append(d.Delete, key)
		}
	}
	sort.Strings(d.Upload)
	sort.Strings(d.Delete)
	return d
}

// ValidateLocalDirectory reports an error if path doesn't exist or isn't
// a directory.
func ValidateLocalDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

// WalkLocalTree builds a map of slash-separated relative path -> size in
// bytes for every regular file under root, via filepath.WalkDir.
func WalkLocalTree(root string) (map[string]int64, error) {
	files := make(map[string]int64)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// ContentTypeFor derives a PutObject Content-Type from a file's
// extension, falling back to application/octet-stream when the stdlib
// mime package doesn't recognize it.
func ContentTypeFor(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// ListAllBucketObjects lists every object in bucket via s3:ListObjectsV2,
// following ContinuationToken to page through the full result.
func ListAllBucketObjects(ctx context.Context, client awsclient.S3API, bucket string) (map[string]int64, error) {
	objects := make(map[string]int64)
	var token *string
	for {
		callCtx, cancel := WithCallTimeout(ctx)
		out, err := client.ListObjectsV2(callCtx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), ContinuationToken: token})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, o := range out.Contents {
			objects[aws.ToString(o.Key)] = aws.ToInt64(o.Size)
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return objects, nil
}

// UploadFile uploads the local file at path to bucket/key.
func UploadFile(ctx context.Context, client awsclient.S3API, bucket, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	callCtx, cancel := WithTransferTimeout(ctx)
	defer cancel()
	_, err = client.PutObject(callCtx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(info.Size()),
		ContentType:   aws.String(ContentTypeFor(path)),
	})
	return err
}
