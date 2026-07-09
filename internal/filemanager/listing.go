package filemanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/s3diff"
)

// validateLocalDir mirrors internal/workflow's validateLocalDirectory --
// duplicated rather than imported to keep this package free of a
// dependency on internal/workflow (which depends on this package the
// other way, via object_browser.go).
func validateLocalDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

// listS3Level lists one directory level of bucket under prefix via
// s3:ListObjectsV2 with Delimiter=/ (DESIGN.md 21.5): CommonPrefixes
// become kindDir rows, Contents become kindFile rows. Follows
// ContinuationToken to page through a level with more than one page of
// entries. The prefix's own placeholder object (some tools write a
// zero-byte object at the prefix itself) is skipped.
func listS3Level(ctx context.Context, client awsclient.S3API, bucket, prefix string) ([]entry, error) {
	var out []entry
	var token *string
	for {
		callCtx, cancel := s3diff.WithCallTimeout(ctx)
		resp, err := client.ListObjectsV2(callCtx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, cp := range resp.CommonPrefixes {
			p := aws.ToString(cp.Prefix)
			out = append(out, entry{name: baseOf(p), kind: kindDir, key: p})
		}
		for _, o := range resp.Contents {
			key := aws.ToString(o.Key)
			if key == prefix {
				continue
			}
			out = append(out, entry{
				name:     baseOf(key),
				kind:     kindFile,
				size:     aws.ToInt64(o.Size),
				modified: aws.ToTime(o.LastModified),
				key:      key,
			})
		}
		if !aws.ToBool(resp.IsTruncated) || resp.NextContinuationToken == nil {
			break
		}
		token = resp.NextContinuationToken
	}
	sortEntries(out)
	return out, nil
}

// listS3Recursive lists every object under prefix (no Delimiter), for
// Find (DESIGN.md 21.7) -- the same full-listing cost Sync and the old
// delete-by-prefix wizard already paid, just user-triggered from inside
// the browser now.
func listS3Recursive(ctx context.Context, client awsclient.S3API, bucket, prefix string) ([]entry, error) {
	var out []entry
	var token *string
	for {
		callCtx, cancel := s3diff.WithCallTimeout(ctx)
		resp, err := client.ListObjectsV2(callCtx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, o := range resp.Contents {
			key := aws.ToString(o.Key)
			if key == prefix {
				continue
			}
			out = append(out, entry{
				name:     key,
				kind:     kindFile,
				size:     aws.ToInt64(o.Size),
				modified: aws.ToTime(o.LastModified),
				key:      key,
			})
		}
		if !aws.ToBool(resp.IsTruncated) || resp.NextContinuationToken == nil {
			break
		}
		token = resp.NextContinuationToken
	}
	return out, nil
}

// listLocalLevel lists one directory level of dir via os.ReadDir
// (DESIGN.md 21.5) -- not a recursive walk; that traversal is reserved
// for Find (21.7) and reuses filepath.WalkDir separately.
func listLocalLevel(dir string) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]entry, 0, len(des))
	for _, d := range des {
		path := filepath.Join(dir, d.Name())
		if d.IsDir() {
			out = append(out, entry{name: d.Name(), kind: kindDir, key: path})
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		out = append(out, entry{
			name:     d.Name(),
			kind:     kindFile,
			size:     info.Size(),
			modified: info.ModTime(),
			key:      path,
		})
	}
	sortEntries(out)
	return out, nil
}

// listLocalRecursive lists every regular file under dir (Find, 21.7),
// reusing the same filepath.WalkDir traversal internal/workflow's
// walkLocalTree already uses for Sync's diff.
func listLocalRecursive(dir string) ([]entry, error) {
	var out []entry
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
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
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, entry{
			name:     filepath.ToSlash(rel),
			kind:     kindFile,
			size:     info.Size(),
			modified: info.ModTime(),
			key:      path,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// globMatch reports whether pattern (a shell glob, Go stdlib
// path/filepath.Match semantics) matches name -- name is always the
// full path relative to the search's starting point (DESIGN.md 21.7).
// By default Find/Filter match against just the basename, so a plain
// "index.html" finds it at any depth; a pattern starting with "^" or
// "/" is anchored to the search's root instead and matched against the
// whole path (the marker stripped first) -- e.g. "^index.html" (or
// "/index.html", accepted as an equivalent alias) matches only the
// root-level index.html, not sub/index.html, and "^sub/*.html" matches
// only directly under sub/ (filepath.Match's "*" doesn't cross "/", the
// same as a single shell glob level). "^" is the primary, documented
// form -- the regex convention for "anchor to the start" an operator is
// more likely to already reach for -- with "/" kept working since it
// was the original spelling; neither means an actual filesystem/S3-key
// path starting at the real root, just "the search's own root."
func globMatch(pattern, name string) bool {
	if anchored, ok := stripAnchor(pattern); ok {
		matched, err := filepath.Match(anchored, name)
		return err == nil && matched
	}
	base := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		base = name[i+1:]
	}
	ok, err := filepath.Match(pattern, base)
	return err == nil && ok
}

// stripAnchor reports whether pattern is anchored ("^" or "/" prefix)
// and returns the pattern with that marker removed.
func stripAnchor(pattern string) (rest string, anchored bool) {
	if rest, ok := strings.CutPrefix(pattern, "^"); ok {
		return rest, true
	}
	if rest, ok := strings.CutPrefix(pattern, "/"); ok {
		return rest, true
	}
	return pattern, false
}
