package rdmfetch

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const snapshotsDir = "opensearch-snapshots"

// isNotFound reports whether err is S3's "no such object" in any of the forms
// GetObject and HeadObject return it.
func isNotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	var nf *s3types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound")
}

// singlePartMD5 returns the MD5 an object's ETag carries, and false when the
// ETag is a multipart one (it contains a dash) and is not an MD5 of the content.
func singlePartMD5(etag string) (string, bool) {
	e := strings.Trim(etag, `"`)
	if e == "" || strings.Contains(e, "-") || len(e) != 32 {
		return "", false
	}
	return strings.ToLower(e), true
}

// listAll lists every object under prefix, following pagination.
func (f *Fetcher) listAll(ctx context.Context, bucket, prefix string) ([]s3types.Object, error) {
	var out []s3types.Object
	var token *string
	for {
		page, err := f.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, fmt.Errorf("listing s3://%s/%s: %w", bucket, prefix, err)
		}
		out = append(out, page.Contents...)
		if !aws.ToBool(page.IsTruncated) {
			return out, nil
		}
		token = page.NextContinuationToken
	}
}

// ListDumps lists the SQL backups (.sql and .sql.gz) directly under source/ in
// bucket, newest first.
//
// @param ctx {context.Context} cancels the listing
// @param bucket {string} the backup bucket
// @param source {string} the instance prefix, such as caltechauthors-v13
// @returns {[]Dump} the dumps, newest first
// @returns {error} why the bucket could not be listed
// @example
//
//	dumps, err := fe.ListDumps(ctx, "sql-backups.library.caltech.edu", "caltechauthors-v13")
func (f *Fetcher) ListDumps(ctx context.Context, bucket, source string) ([]Dump, error) {
	prefix := source + "/"
	objs, err := f.listAll(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	var dumps []Dump
	for _, o := range objs {
		key := aws.ToString(o.Key)
		rest := strings.TrimPrefix(key, prefix)
		if strings.Contains(rest, "/") || !(strings.HasSuffix(rest, ".sql") || strings.HasSuffix(rest, ".sql.gz")) {
			continue
		}
		dumps = append(dumps, Dump{Key: key, Size: aws.ToInt64(o.Size), LastModified: aws.ToTime(o.LastModified)})
	}
	sort.Slice(dumps, func(i, j int) bool {
		if !dumps[i].LastModified.Equal(dumps[j].LastModified) {
			return dumps[i].LastModified.After(dumps[j].LastModified)
		}
		return dumps[i].Key > dumps[j].Key
	})
	return dumps, nil
}

// ListSnapshots lists the archived OpenSearch snapshot names under
// source/opensearch-snapshots/, newest first (their names carry a timestamp).
//
// @param ctx {context.Context} cancels the listing
// @param bucket {string} the OpenSearch backup bucket
// @param source {string} the instance prefix
// @returns {[]string} the snapshot names, newest first
// @returns {error} why the bucket could not be listed
// @example
//
//	names, err := fe.ListSnapshots(ctx, "opensearch-backups.library.caltech.edu", "caltechauthors-v13")
func (f *Fetcher) ListSnapshots(ctx context.Context, bucket, source string) ([]string, error) {
	base := source + "/" + snapshotsDir + "/"
	var names []string
	var token *string
	for {
		page, err := f.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(base), Delimiter: aws.String("/"), ContinuationToken: token})
		if err != nil {
			return nil, fmt.Errorf("listing s3://%s/%s: %w", bucket, base, err)
		}
		for _, cp := range page.CommonPrefixes {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(aws.ToString(cp.Prefix), base), "/"))
		}
		if !aws.ToBool(page.IsTruncated) {
			break
		}
		token = page.NextContinuationToken
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// hashFile returns the MD5 of a local file.
func hashFile(p string) (string, error) {
	fh, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := md5.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// gzipOK reads a gzip file end to end, the same check as `gunzip -t`.
func gzipOK(p string) error {
	fh, err := os.Open(p)
	if err != nil {
		return err
	}
	defer fh.Close()
	zr, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, zr)
	return err
}

// FetchDump downloads one SQL backup into the cache, mode 0600 because it holds
// user data, and verifies it before it is kept: size against S3, MD5 against the
// ETag when that is comparable, and a full gzip read for a .gz. A file already in
// the cache that passes the same checks is reused and nothing is downloaded. A
// failed fetch leaves nothing behind.
//
// @param ctx {context.Context} cancels the download
// @param bucket {string} the backup bucket
// @param key {string} the object key
// @returns {DumpReport} the verified local copy
// @returns {error} ErrNotFound, ErrCorrupt, or why it could not be read or written
// @example
//
//	rep, err := fe.FetchDump(ctx, "sql-backups.library.caltech.edu", "caltechauthors-v13/db-2026-11-17.sql.gz")
func (f *Fetcher) FetchDump(ctx context.Context, bucket, key string) (DumpReport, error) {
	head, err := f.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return DumpReport{}, fmt.Errorf("s3://%s/%s: %w", bucket, key, ErrNotFound)
		}
		return DumpReport{}, fmt.Errorf("s3://%s/%s: %w", bucket, key, err)
	}
	want := aws.ToInt64(head.ContentLength)
	wantMD5, comparable := singlePartMD5(aws.ToString(head.ETag))
	isGz := strings.HasSuffix(key, ".gz")
	final := filepath.Join(f.CacheDir, path.Base(key))

	// A copy already in the cache is reused when it passes every check that can be
	// made without downloading: the same size as S3; its MD5 against the ETag when
	// that is comparable; for a gzip file, a full read. A dump uploaded in parts has
	// no comparable MD5 (every large dump is one), so there the copy must also be
	// no older than the object, which is what stops a changed object of the same
	// size being taken for the cached one.
	if info, err := os.Stat(final); err == nil && info.Size() == want && (comparable || !info.ModTime().Before(aws.ToTime(head.LastModified))) {
		if got, err := hashFile(final); err == nil && (!comparable || got == wantMD5) && (!isGz || gzipOK(final) == nil) {
			return DumpReport{Path: final, Key: key, Size: want, MD5: got, MD5Verified: comparable, GzipOK: isGz, Reused: true}, nil
		}
	}

	if err := os.MkdirAll(f.CacheDir, 0o700); err != nil {
		return DumpReport{}, err
	}
	part := final + ".part"
	defer os.Remove(part)
	got, err := f.download(ctx, bucket, key, part, 0o600, want, wantMD5, comparable)
	if err != nil {
		return DumpReport{}, err
	}
	if isGz {
		if err := gzipOK(part); err != nil {
			return DumpReport{}, fmt.Errorf("s3://%s/%s: %w: gzip test failed: %v", bucket, key, ErrCorrupt, err)
		}
	}
	if err := os.Rename(part, final); err != nil {
		return DumpReport{}, err
	}
	return DumpReport{Path: final, Key: key, Size: want, MD5: got, MD5Verified: comparable, GzipOK: isGz}, nil
}

// download copies one object to dest, checking its size against both the
// reported length and wantSize, and its MD5 when comparable. It returns the MD5
// of what was written.
func (f *Fetcher) download(ctx context.Context, bucket, key, dest string, mode os.FileMode, wantSize int64, wantMD5 string, comparable bool) (string, error) {
	out, err := f.S3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return "", fmt.Errorf("s3://%s/%s: %w", bucket, key, ErrNotFound)
		}
		return "", fmt.Errorf("s3://%s/%s: %w", bucket, key, err)
	}
	defer out.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	fh, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	h := md5.New()
	n, copyErr := io.Copy(io.MultiWriter(fh, h), out.Body)
	closeErr := fh.Close()
	if copyErr != nil {
		return "", fmt.Errorf("s3://%s/%s: %w", bucket, key, copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != wantSize || n != aws.ToInt64(out.ContentLength) {
		return "", fmt.Errorf("s3://%s/%s: %w: size is %d bytes, S3 says %d", bucket, key, ErrCorrupt, n, wantSize)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if comparable && sum != wantMD5 {
		return "", fmt.Errorf("s3://%s/%s: %w: MD5 is %s, S3's ETag says %s", bucket, key, ErrCorrupt, sum, wantMD5)
	}
	return sum, nil
}

type snapFile struct {
	rel  string
	key  string
	size int64
	md5  string
	cmp  bool
}

// FetchSnapshot downloads one archived snapshot repository into
// CacheDir/<name>, so that directory is the repository root (the dated
// directory's contents). Every object's size, and its MD5 where the ETag
// allows, is checked; the repository must hold an indices/ directory. The copy
// is built beside the final directory and moved into place only when complete,
// so a failure leaves nothing. A complete copy from an earlier fetch whose files
// still match S3's listing is reused.
//
// @param ctx {context.Context} cancels the download
// @param bucket {string} the OpenSearch backup bucket
// @param source {string} the instance prefix
// @param name {string} the snapshot name
// @returns {SnapshotReport} the local copy's directory, object count and bytes
// @returns {error} ErrNotFound, ErrCorrupt, or why it could not be written
// @example
//
//	rep, err := fe.FetchSnapshot(ctx, "opensearch-backups.library.caltech.edu", "caltechauthors-v13", "rdm-20261117-030000")
func (f *Fetcher) FetchSnapshot(ctx context.Context, bucket, source, name string) (SnapshotReport, error) {
	prefix := source + "/" + snapshotsDir + "/" + name + "/"
	objs, err := f.listAll(ctx, bucket, prefix)
	if err != nil {
		return SnapshotReport{}, err
	}
	var files []snapFile
	var total int64
	for _, o := range objs {
		key := aws.ToString(o.Key)
		if strings.HasSuffix(key, "/") {
			continue
		}
		rel := strings.TrimPrefix(key, prefix)
		clean := path.Clean(rel)
		if rel == "" || path.IsAbs(rel) || clean != rel || clean == ".." || strings.HasPrefix(clean, "../") {
			return SnapshotReport{}, fmt.Errorf("s3://%s/%s: %w: the key would write outside the repository directory", bucket, key, ErrCorrupt)
		}
		md, cmp := singlePartMD5(aws.ToString(o.ETag))
		files = append(files, snapFile{rel: rel, key: key, size: aws.ToInt64(o.Size), md5: md, cmp: cmp})
		total += aws.ToInt64(o.Size)
	}
	if len(files) == 0 {
		return SnapshotReport{}, fmt.Errorf("no snapshot %q under s3://%s/%s: %w", name, bucket, source+"/"+snapshotsDir+"/", ErrNotFound)
	}
	hasIndices := false
	for _, sf := range files {
		if strings.HasPrefix(sf.rel, "indices/") {
			hasIndices = true
			break
		}
	}
	if !hasIndices {
		return SnapshotReport{}, fmt.Errorf("snapshot %q under s3://%s/%s: %w: the repository has no indices/ directory", name, bucket, prefix, ErrCorrupt)
	}

	final := filepath.Join(f.CacheDir, name)
	marker := final + ".fetched"
	if localMatches(final, marker, files) {
		return SnapshotReport{Dir: final, Name: name, Objects: len(files), Bytes: total, Reused: true}, nil
	}

	if err := os.MkdirAll(f.CacheDir, 0o700); err != nil {
		return SnapshotReport{}, err
	}
	part := final + ".part"
	os.RemoveAll(part)
	os.Remove(marker)
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(part)
		}
	}()

	workers := f.Workers
	if workers <= 0 {
		workers = 8
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, workers)
	)
	for _, sf := range files {
		sem <- struct{}{}
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func(sf snapFile) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := f.download(ctx, bucket, sf.key, filepath.Join(part, filepath.FromSlash(sf.rel)), 0o644, sf.size, sf.md5, sf.cmp); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", sf.rel, err)
					cancel()
				}
				mu.Unlock()
			}
		}(sf)
	}
	wg.Wait()
	if firstErr != nil {
		return SnapshotReport{}, firstErr
	}
	if err := os.RemoveAll(final); err != nil {
		return SnapshotReport{}, err
	}
	if err := os.Rename(part, final); err != nil {
		return SnapshotReport{}, err
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d objects, %d bytes\n", len(files), total)), 0o600); err != nil {
		return SnapshotReport{}, err
	}
	ok = true
	return SnapshotReport{Dir: final, Name: name, Objects: len(files), Bytes: total}, nil
}

// localMatches reports whether dir holds a complete earlier fetch: its marker
// exists and every file in the S3 listing is present locally at the same size.
func localMatches(dir, marker string, files []snapFile) bool {
	if _, err := os.Stat(marker); err != nil {
		return false
	}
	for _, sf := range files {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(sf.rel)))
		if err != nil || info.Size() != sf.size {
			return false
		}
	}
	return true
}

// PackSnapshot writes the repository directory's contents as one tar file, with
// paths relative to dir, and returns the file's SHA-256. The entries are in a
// fixed order with fixed times and owners, so the same tree always packs to the
// same bytes. The repository must hold an indices/ directory; nothing is left
// at tarPath on failure.
//
// @param dir {string} the repository root, as returned in SnapshotReport.Dir
// @param tarPath {string} where to write the tar file
// @returns {string} the SHA-256 of the tar file, in hex
// @returns {error} why the directory is not a repository or the tar could not be written
// @example
//
//	sum, err := PackSnapshot(rep.Dir, "/tmp/repo.tar")
func PackSnapshot(dir, tarPath string) (string, error) {
	if info, err := os.Stat(filepath.Join(dir, "indices")); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s has no indices/ directory, so it is not a snapshot repository", dir)
	}
	part := tarPath + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(part)
		}
	}()
	h := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(out, h))
	epoch := time.Unix(0, 0)
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: epoch})
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: 0o644, Size: info.Size(), ModTime: epoch}); err != nil {
				return err
			}
			fh, err := os.Open(p)
			if err != nil {
				return err
			}
			defer fh.Close()
			_, err = io.Copy(tw, fh)
			return err
		}
		return fmt.Errorf("%s is neither a file nor a directory", p)
	})
	if err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(part, tarPath); err != nil {
		return "", err
	}
	ok = true
	return hex.EncodeToString(h.Sum(nil)), nil
}
