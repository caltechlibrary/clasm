package workflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// BackupFile is one file found by ListBackupFiles.
type BackupFile struct {
	Path      string
	SizeBytes int64
	ModTime   time.Time
}

// shellQuote wraps s in single quotes, escaping any embedded single
// quote -- the standard POSIX-safe way to embed an arbitrary string
// (e.g. a filename with spaces) into a shell command line built for SSM
// Run Command. Building remote command strings reintroduces exactly the
// shell-quoting risk category this project was rewritten to Go to
// eliminate for *local* command construction (see DECISIONS.md,
// "Retarget implementation from Bash to Go"); AWS SSM's API only accepts
// a command string, so this is the one place that risk is unavoidable --
// handled carefully and tested directly, rather than hand-built ad hoc
// per call site.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ListBackupFiles runs a read-only `find` over directory on instanceID
// via SSM, listing every file's size and modification time. Age
// filtering is done locally by FilterByAge rather than in the remote
// shell command, keeping the remote script minimal and the filtering
// logic testable without a real instance.
func ListBackupFiles(ctx context.Context, client awsclient.SSMAPI, instanceID, directory string, timeout, pollInterval time.Duration) ([]BackupFile, error) {
	command := fmt.Sprintf(`find %s -maxdepth 1 -type f -printf '%%s\t%%T@\t%%p\n'`, shellQuote(directory))
	stdout, status, err := RunShellCommand(ctx, client, instanceID, command, timeout, pollInterval)
	if err != nil {
		return nil, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, fmt.Errorf("listing files in %s on %s failed (status: %s)", directory, instanceID, status)
	}
	return parseBackupFileList(stdout)
}

// parseBackupFileList parses ListBackupFiles' <size>\t<mtime-epoch>\t<path>
// per-line output. Malformed lines are skipped rather than treated as a
// fatal error -- a single corrupted line shouldn't abort the whole dry-run.
func parseBackupFileList(output string) ([]BackupFile, error) {
	var files []BackupFile
	for line := range strings.SplitSeq(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		epoch, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}
		files = append(files, BackupFile{
			Path:      parts[2],
			SizeBytes: size,
			ModTime:   time.Unix(int64(epoch), 0),
		})
	}
	return files, nil
}

// FilterByAge keeps only files strictly older than thresholdDays,
// measured from now.
func FilterByAge(files []BackupFile, thresholdDays int, now time.Time) []BackupFile {
	cutoff := now.AddDate(0, 0, -thresholdDays)
	var out []BackupFile
	for _, f := range files {
		if f.ModTime.Before(cutoff) {
			out = append(out, f)
		}
	}
	return out
}
