package workflow

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// syncDiff is the result of comparing a local directory tree against a
// bucket's current contents by key and size (not checksum, per
// DESIGN.md): Upload holds keys that are new locally or whose size
// differs from the bucket's copy; Delete holds keys that exist in the
// bucket but have no local counterpart. Both are sorted for a stable,
// readable dry-run listing.
type syncDiff struct {
	Upload []string
	Delete []string
}

func diffSync(local, remote map[string]int64) syncDiff {
	var d syncDiff
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

func validateLocalDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

// walkLocalTree builds a map of slash-separated relative path -> size in
// bytes for every regular file under root, via filepath.WalkDir.
func walkLocalTree(root string) (map[string]int64, error) {
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

// contentTypeFor derives a PutObject Content-Type from a file's
// extension, falling back to application/octet-stream when the stdlib
// mime package doesn't recognize it.
func contentTypeFor(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// listAllBucketObjects lists every object in bucket via s3:ListObjectsV2,
// following ContinuationToken to page through the full result (same
// pagination-cost caveat as Key Management's listing -- acceptable at
// this team's actual bucket scale, not claiming unbounded scalability).
func listAllBucketObjects(ctx context.Context, client awsclient.S3API, bucket string) (map[string]int64, error) {
	objects := make(map[string]int64)
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), ContinuationToken: token})
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

func displaySyncDryRun(t *termlib.Terminal, diff syncDiff) {
	t.Println("\n=== DRY RUN: sync plan ===")
	if len(diff.Upload) == 0 {
		t.Println("Upload: none")
	} else {
		t.Printf("Upload (%d):\n", len(diff.Upload))
		for _, key := range diff.Upload {
			t.Printf("  %s\n", key)
		}
	}
	if len(diff.Delete) == 0 {
		t.Println("Delete (bucket-only): none")
	} else {
		t.Printf("Delete (bucket-only) (%d):\n", len(diff.Delete))
		for _, key := range diff.Delete {
			t.Printf("  %s\n", key)
		}
	}
	t.Refresh()
}

func uploadFile(ctx context.Context, client awsclient.S3API, bucket, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(info.Size()),
		ContentType:   aws.String(contentTypeFor(path)),
	})
	return err
}

// SyncDirectoryToBucket runs the S3 domain's "Sync Local Directory to
// Bucket" workflow (DESIGN.md, Feature 20): pick a bucket, prompt a local
// directory, diff its contents against the bucket's by key+size, show
// both the upload and delete candidate lists as a dry run before
// touching anything, gate the upload step with a plain Confirm (declining
// aborts the whole run -- no delete prompt follows), upload via
// s3:PutObject with per-file OK/FAIL progress (Backup Archive & Trim's
// established convention, Phase 15.20), then -- only if there are
// bucket-only objects -- a separate, stronger ConfirmDestructive gate
// (Security Consideration #11: upload and delete confirmations must
// never be bundled) before s3:DeleteObject.
func SyncDirectoryToBucket(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
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

	dir, err := ui.Prompt(t, le, "Local directory to sync", ui.WithValidator(validateLocalDirectory))
	if err != nil {
		return err
	}

	local, err := walkLocalTree(dir)
	if err != nil {
		return fmt.Errorf("reading local directory %s: %w", dir, err)
	}

	remote, err := listAllBucketObjects(ctx, client, bucket.Name)
	if err != nil {
		return fmt.Errorf("listing objects in bucket %s: %w", bucket.Name, err)
	}

	diff := diffSync(local, remote)
	displaySyncDryRun(t, diff)

	if len(diff.Upload) == 0 && len(diff.Delete) == 0 {
		t.Println("Nothing to do -- local directory and bucket already match.")
		t.Refresh()
		return nil
	}

	var uploaded, failed int
	var bytesUploaded int64
	if len(diff.Upload) > 0 {
		ok, err := Confirm(t, le, fmt.Sprintf("Upload %d file(s) to %s?", len(diff.Upload), bucket.Name))
		if err != nil {
			return err
		}
		if !ok {
			t.Println("Cancelled.")
			t.Refresh()
			return nil
		}

		for i, key := range diff.Upload {
			path := filepath.Join(dir, filepath.FromSlash(key))
			size := local[key]
			status := "OK"
			if err := uploadFile(ctx, client, bucket.Name, key, path); err != nil {
				status = "FAIL"
				failed++
			} else {
				uploaded++
				bytesUploaded += size
			}
			t.Printf("  ... %d/%d (%s) - %s %s\n", i+1, len(diff.Upload), formatBytes(size), status, key)
			t.Refresh()
		}
	}

	var deleted int
	if len(diff.Delete) > 0 {
		t.Printf("\n%d object(s) exist in the bucket but not locally: %s\n", len(diff.Delete), strings.Join(diff.Delete, ", "))
		t.Refresh()

		ok, err := ConfirmDestructive(t, le, bucket.Name)
		if err != nil {
			return err
		}
		if !ok {
			t.Println("Deletion cancelled -- bucket-only objects left untouched.")
			t.Refresh()
		} else {
			for _, key := range diff.Delete {
				if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket.Name), Key: aws.String(key)}); err != nil {
					t.Printf("  FAIL deleting %s: %v\n", key, err)
					t.Refresh()
					continue
				}
				deleted++
			}
		}
	}

	t.Printf("\nUploaded %d file(s) (%s), deleted %d object(s).\n", uploaded, formatBytes(bytesUploaded), deleted)
	if failed > 0 {
		t.Printf("%d file(s) failed to upload.\n", failed)
	}
	t.Refresh()
	return nil
}
