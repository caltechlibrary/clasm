package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// The restore's file-location guard (DR-0187) reads the file location rows a
// backup would restore. A backup is `pg_dump --column-inserts`, gzipped (a legacy
// archive may be plain), so every row of files_location is one INSERT line and a
// line-based reader is enough; the uri value names the bucket. The rows are found
// with grep on the instance -- a dump is gigabytes, and the rows are a handful.

// FileLocation is one row of RDM's files_location table: where uploaded files are
// stored.
type FileLocation struct {
	Name string
	URI  string
}

const fileLocationInsertPrefix = "INSERT INTO public.files_location "

// locationScanSentinel ends a scan's output with the status of the whole
// pipeline, because grep finding nothing (exit 1) must not look like a download
// that failed.
const locationScanSentinel = "clasm-scan-rc="

// splitSQLValues splits the inside of a VALUES (...) list into its values at the
// commas outside quotes. A single quote inside a string is doubled.
func splitSQLValues(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				cur.WriteString("''")
				i++
				continue
			}
			inQuote = false
			cur.WriteByte(c)
		case !inQuote && c == '\'':
			inQuote = true
			cur.WriteByte(c)
		case !inQuote && c == ',':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, errors.New("unterminated string")
	}
	return append(out, strings.TrimSpace(cur.String())), nil
}

// sqlValueText is a SQL literal as plain text: a quoted string without its quotes
// and with doubled quotes undoubled, NULL as "", anything else (a number, true) as
// written.
func sqlValueText(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	if strings.EqualFold(v, "NULL") {
		return ""
	}
	return v
}

// parseFileLocationInsert reads one line of a dump. ok is false, with no error,
// for any line that is not an INSERT into files_location. A files_location INSERT
// it cannot read is an error: the guard must not pass on what it could not read.
func parseFileLocationInsert(line string) (loc FileLocation, ok bool, err error) {
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, fileLocationInsertPrefix) {
		return FileLocation{}, false, nil
	}
	fail := func(format string, a ...any) (FileLocation, bool, error) {
		return FileLocation{}, false, fmt.Errorf("unreadable files_location row (%s): %.200s", fmt.Sprintf(format, a...), line)
	}
	rest := line[len(fileLocationInsertPrefix):]
	if !strings.HasPrefix(rest, "(") {
		return fail("not a column-listing INSERT")
	}
	i := strings.Index(rest, ") VALUES (")
	if i < 0 {
		return fail("no VALUES list")
	}
	columns := rest[1:i]
	tail := strings.TrimSpace(rest[i+len(") VALUES ("):])
	if !strings.HasSuffix(tail, ");") {
		return fail("the statement does not end in );")
	}
	values, err := splitSQLValues(strings.TrimSuffix(tail, ");"))
	if err != nil {
		return fail("%v", err)
	}
	names := strings.Split(columns, ",")
	if len(names) != len(values) {
		return fail("%d columns but %d values", len(names), len(values))
	}
	uriFound := false
	for n, name := range names {
		switch strings.Trim(strings.TrimSpace(name), `"`) {
		case "uri":
			loc.URI, uriFound = sqlValueText(values[n]), true
		case "name":
			loc.Name = sqlValueText(values[n])
		}
	}
	if !uriFound {
		return fail("no uri column")
	}
	return loc, true, nil
}

// parseFileLocationLines reads every files_location row out of a scan's output,
// ignoring any other line.
func parseFileLocationLines(output string) ([]FileLocation, error) {
	var locs []FileLocation
	for _, line := range strings.Split(output, "\n") {
		loc, ok, err := parseFileLocationInsert(line)
		if err != nil {
			return nil, err
		}
		if ok {
			locs = append(locs, loc)
		}
	}
	return locs, nil
}

// s3BucketOfURI is the bucket of an s3://bucket[/prefix] URI. ok is false for
// anything else: a local path, another scheme, or no bucket.
func s3BucketOfURI(uri string) (bucket string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(uri), "s3://")
	if !found {
		return "", false
	}
	bucket, _, _ = strings.Cut(rest, "/")
	return bucket, bucket != ""
}

// locationScanPattern is the grep pattern for a files_location INSERT.
const locationScanPattern = `^INSERT INTO public\.files_location `

// buildLocalLocationScanCommand finds the files_location rows in a plain SQL file
// already on the instance. Read-only.
func buildLocalLocationScanCommand(sqlPath string) string {
	return fmt.Sprintf("exec 2>&1; grep -a %s %s; rc=$?; if [ \"$rc\" -eq 1 ]; then rc=0; fi; echo \"%s$rc\"",
		shellQuote(locationScanPattern), shellQuote(sqlPath), locationScanSentinel)
}

// buildStreamLocationScanCommand finds the rows by streaming the backup from S3
// through gunzip (when the key is gzip) and grep, writing nothing to the
// instance's disk: a dry run must change nothing. bash's pipefail makes a failed
// download count, and the sentinel line carries the whole pipeline's status.
func buildStreamLocationScanCommand(bucket, key string) string {
	decompress := ""
	if needsDecompression(key) {
		decompress = " | gunzip -c"
	}
	return fmt.Sprintf("bash -o pipefail <<'CLASM_LOCATION_SCAN'\nexec 2>&1\naws s3 cp --only-show-errors %s -%s | { grep -a %s || [ $? -eq 1 ]; }\necho \"%s$?\"\nCLASM_LOCATION_SCAN",
		shellQuote(fmt.Sprintf("s3://%s/%s", bucket, key)), decompress, shellQuote(locationScanPattern), locationScanSentinel)
}

// readLocationScan turns a scan's output into rows. The output must end with the
// sentinel reporting status 0; anything else (no sentinel, a non-zero status) means
// the scan did not finish, and the rest of the output is the reason.
func readLocationScan(what, stdout string, status ssmtypes.CommandInvocationStatus) ([]FileLocation, error) {
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, curlFailureError(fmt.Sprintf("scanning %s for file locations failed", what), status, stdout)
	}
	idx := strings.LastIndex(stdout, locationScanSentinel)
	if idx < 0 || strings.TrimSpace(stdout[idx+len(locationScanSentinel):]) != "0" {
		return nil, fmt.Errorf("scanning %s for file locations did not finish cleanly: %s", what, strings.TrimSpace(stdout))
	}
	return parseFileLocationLines(stdout[:idx])
}

// ScanLocalFileLocations reads the files_location rows from a plain SQL file
// already on the instance (the decompressed download of a confirmed restore).
//
// @param ctx {context.Context} the call's context
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param instanceID {string} the instance holding the file
// @param sqlPath {string} the plain SQL file on the instance
// @returns {[]FileLocation, error} the rows, or an error if the scan did not finish or a row was unreadable
func ScanLocalFileLocations(ctx context.Context, client awsclient.SSMAPI, instanceID, sqlPath string) ([]FileLocation, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildLocalLocationScanCommand(sqlPath), DefaultOwnershipTimeout, DefaultSSMPollInterval)
	if err != nil {
		return nil, err
	}
	return readLocationScan(sqlPath+" on "+instanceID, stdout, status)
}

// ScanBackupFileLocations reads the files_location rows by streaming a backup from
// S3 through the instance, writing nothing to its disk. It is what a dry run uses,
// since the backup has not been downloaded.
//
// @param ctx {context.Context} the call's context
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param instanceID {string} the instance that streams the backup
// @param bucket {string} the backup's bucket
// @param key {string} the backup's key; a .gz suffix means gzip
// @returns {[]FileLocation, error} the rows, or an error if the scan did not finish or a row was unreadable
func ScanBackupFileLocations(ctx context.Context, client awsclient.SSMAPI, instanceID, bucket, key string) ([]FileLocation, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildStreamLocationScanCommand(bucket, key), DefaultSQLRestoreTimeout, DefaultSSMPollInterval)
	if err != nil {
		return nil, err
	}
	return readLocationScan(fmt.Sprintf("s3://%s/%s", bucket, key), stdout, status)
}
