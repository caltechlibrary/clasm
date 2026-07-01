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

// buildUploadCommand builds a shell script that uploads each file to
// s3://bucket/<basename> via the instance's own `aws` CLI/credentials,
// reporting one OK/FAIL line per file with the already-known size from
// the dry-run listing (no need for the remote script to re-stat). Every
// dynamic value (source path, destination URI, echoed key) is
// shell-quoted and passed as a separate `printf` argument rather than
// interpolated into a double-quoted string, so a key or path containing
// a quote, backtick, or `$` can't break the generated script.
func buildUploadCommand(files []BackupFile, bucket string) string {
	var sb strings.Builder
	for _, f := range files {
		key := path.Base(f.Path)
		dest := fmt.Sprintf("s3://%s/%s", bucket, key)
		fmt.Fprintf(&sb, "if aws s3 cp %s %s; then printf 'OK\\t%%s\\t%%d\\n' %s %d; else printf 'FAIL\\t%%s\\t0\\n' %s; fi\n",
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

// UploadBackupFiles runs buildUploadCommand's script via SSM and parses
// the per-file results. A non-Success command status is a hard error --
// distinct from an individual file reporting FAIL, which is a normal,
// expected outcome this function still returns successfully so the
// caller can report it.
func UploadBackupFiles(ctx context.Context, client awsclient.SSMAPI, instanceID string, files []BackupFile, bucket string, timeout, pollInterval time.Duration) ([]UploadResult, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildUploadCommand(files, bucket), timeout, pollInterval)
	if err != nil {
		return nil, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, fmt.Errorf("upload command on %s failed (status: %s)", instanceID, status)
	}
	return parseUploadResults(stdout), nil
}
