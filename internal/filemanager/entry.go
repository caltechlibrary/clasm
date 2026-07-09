// Package filemanager implements the S3 domain's interactive "Browse &
// Manage Objects" screen (DESIGN.md, Features 21.2-21.8; PLAN.md, Phase
// 20.1): a bubbletea Model, scoped to this one screen -- every other
// workflow in internal/workflow stays on huh/termlib's blocking prompts
// (DECISIONS.md, "Use a scoped bubbletea screen for the file manager's
// double-pane mode").
package filemanager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// entryKind distinguishes a directory-like row (an S3 CommonPrefix or a
// local subdirectory) from a leaf row (an S3 object or a local file).
type entryKind int

const (
	kindDir entryKind = iota
	kindFile
)

// entry is one row in a pane's listing. Both the local and S3 panes share
// this one shape so navigation, tagging, and rendering code isn't
// duplicated per side (DESIGN.md 21.5).
type entry struct {
	name     string // basename shown in the listing
	kind     entryKind
	size     int64
	modified time.Time
	// key is the full S3 key (kindFile) or prefix (kindDir) for the
	// remote pane, or the full local filesystem path for the local pane
	// -- always enough on its own to act on the row (download/delete/
	// upload/navigate) without recombining it with the pane's current
	// position.
	key string
}

// sortEntries orders a listing folders-then-files, alphabetical within
// each group (DESIGN.md 21.5).
func sortEntries(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind == kindDir
		}
		return entries[i].name < entries[j].name
	})
}

// formatBytes renders n as a human-readable size (KB/MB/GB), matching
// internal/workflow's own formatBytes -- kept as a separate copy rather
// than an import to avoid this package depending on internal/workflow
// (which depends on this package the other way, via object_browser.go).
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// displayName returns name for a plain listing row, or name+"/" for a
// directory-like row.
func displayName(e entry) string {
	if e.kind == kindDir {
		return e.name + "/"
	}
	return e.name
}

// joinKey builds a slash-separated child key/path from a parent
// prefix/dir and a basename, matching S3 key and Unix path conventions
// alike. An empty name (root-level prefix/dir) returns parent
// unchanged, not parent+"/" -- important for pane.label(), which
// otherwise showed a spurious trailing slash at a linked directory's
// root ("LOCAL: /path/on/disk/" instead of "LOCAL: /path/on/disk").
func joinKey(parent, name string) string {
	if name == "" {
		return parent
	}
	if parent == "" {
		return name
	}
	return strings.TrimSuffix(parent, "/") + "/" + name
}

// parentOfLocal returns the parent of a root-relative local path ("" if
// path is already top-level) -- no trailing slash, matching the local
// pane's prefix convention (pane.prefix for the local side is always a
// bare relative path like "sub/nested", never "sub/nested/").
func parentOfLocal(path string) string {
	path = strings.TrimSuffix(path, "/")
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}

// parentOfS3Prefix returns the parent prefix of an S3 key or prefix,
// preserving the trailing slash S3 prefixes always carry ("" if key is
// already at the bucket root). Getting this wrong (returning "logs"
// instead of "logs/" for a key under "logs/sub/") breaks "go up a
// level": the next s3:ListObjectsV2 call would use a bare string-prefix
// match ("logs") instead of a directory-boundary match ("logs/"),
// silently hiding every bucket-root object that doesn't happen to start
// with the literal string "logs".
func parentOfS3Prefix(key string) string {
	key = strings.TrimSuffix(key, "/")
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return ""
	}
	return key[:i+1]
}

// baseOf returns the final path component of a slash-separated key/path.
func baseOf(key string) string {
	key = strings.TrimSuffix(key, "/")
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return key
	}
	return key[i+1:]
}
