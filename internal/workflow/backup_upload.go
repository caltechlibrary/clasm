package workflow

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// UploadResult is one file's outcome from the remote upload script --
// OK reflects only the instance's own report of `aws s3 cp`'s exit
// status. It is not itself authorization to delete anything; that comes
// only from VerifyUploads' independent s3:HeadObject check (see
// DESIGN.md, Feature 11, and Security Considerations).
type UploadResult struct {
	Key       string
	SizeBytes int64
	OK        bool
}

// uploadKey builds a bucket-relative S3 key that namespaces a file by
// its source instance, so backups from different systems sharing one
// bucket don't collide on identically- or similarly-named files (see
// DECISIONS.md, "Namespace backup uploads by instance"). An empty
// prefix falls back to the bare basename -- path.Join drops empty
// segments, so this needs no special case.
func uploadKey(prefix, filePath string) string {
	return path.Join(prefix, path.Base(filePath))
}

// buildUploadCommand builds a shell script that uploads each file to
// s3://bucket/<prefix>/<basename> via the instance's own `aws`
// CLI/credentials, reporting one OK/FAIL line per file with the
// already-known size from the dry-run listing (no need for the remote
// script to re-stat). `aws s3 cp` runs with --only-show-errors: without
// it, its own \r-updated progress meter can fill
// ssm:GetCommandInvocation's 24,000-character stdout cap on a large
// file, pushing this script's own OK/FAIL line off the end before it's
// ever captured -- silently misreporting a real, successful upload as
// failed (see DECISIONS.md, "Suppress aws s3 cp's progress output to
// avoid truncating the OK/FAIL signal"). Every dynamic value (source
// path, destination URI, echoed key) is shell-quoted and passed as a
// separate `printf` argument rather than interpolated into a
// double-quoted string, so a key or path containing a quote, backtick,
// or `$` can't break the generated script.
func buildUploadCommand(files []BackupFile, bucket, prefix string) string {
	var sb strings.Builder
	for _, f := range files {
		key := uploadKey(prefix, f.Path)
		dest := fmt.Sprintf("s3://%s/%s", bucket, key)
		fmt.Fprintf(&sb, "if aws s3 cp --only-show-errors %s %s; then printf 'OK\\t%%s\\t%%d\\n' %s %d; else printf 'FAIL\\t%%s\\t0\\n' %s; fi\n",
			shellQuote(f.Path), shellQuote(dest), shellQuote(key), f.SizeBytes, shellQuote(key))
	}
	return sb.String()
}

// parseUploadResults parses buildUploadCommand's <OK|FAIL>\t<key>\t<size>
// per-line output. Malformed lines are skipped.
func parseUploadResults(output string) []UploadResult {
	var results []UploadResult
	for line := range strings.SplitSeq(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		results = append(results, UploadResult{Key: parts[1], SizeBytes: size, OK: parts[0] == "OK"})
	}
	return results
}

// UploadProgress reports one file's outcome as UploadBackupFiles works
// through its list sequentially -- drives a live per-file progress
// display instead of a generic "still working" heartbeat (see
// DECISIONS.md, "Per-file upload progress for Backup Archive & Trim").
type UploadProgress struct {
	Done       int
	Total      int
	BytesDone  int64
	BytesTotal int64
	Result     UploadResult
}

// UploadBackupFiles runs one SSM command per file -- rather than a
// single script covering the whole batch -- so onProgress (may be nil)
// can report real per-file progress as each upload completes; a backup
// set can run into the tens of gigabytes and take long enough that a
// generic heartbeat isn't enough to tell it's actually making progress.
// prefix namespaces every destination key (see uploadKey) so backups
// from different instances sharing one bucket don't collide.
// A non-Success command status for any one file is a hard error, same
// as before this per-file split -- distinct from an individual file
// reporting FAIL inside its own command, which is a normal, expected
// outcome still returned to the caller to report.
func UploadBackupFiles(ctx context.Context, client awsclient.SSMAPI, instanceID string, files []BackupFile, bucket, prefix string, timeout, pollInterval time.Duration, onProgress func(UploadProgress)) ([]UploadResult, error) {
	var bytesTotal int64
	for _, f := range files {
		bytesTotal += f.SizeBytes
	}

	results := make([]UploadResult, 0, len(files))
	var bytesDone int64
	for i, f := range files {
		stdout, status, err := RunShellCommand(ctx, client, instanceID, buildUploadCommand([]BackupFile{f}, bucket, prefix), timeout, pollInterval)
		if err != nil {
			return nil, err
		}
		if status != ssmtypes.CommandInvocationStatusSuccess {
			return nil, fmt.Errorf("upload command on %s failed (status: %s)", instanceID, status)
		}

		result := UploadResult{Key: uploadKey(prefix, f.Path)}
		if parsed := parseUploadResults(stdout); len(parsed) == 1 {
			result = parsed[0]
		}
		results = append(results, result)

		bytesDone += f.SizeBytes
		if onProgress != nil {
			onProgress(UploadProgress{Done: i + 1, Total: len(files), BytesDone: bytesDone, BytesTotal: bytesTotal, Result: result})
		}
	}
	return results, nil
}
