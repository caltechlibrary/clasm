package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// The restore's file-location guard (DR-0187) starts by reading the file location
// rows a backup would restore. A backup is `pg_dump --column-inserts`, gzipped
// (or, for a legacy archive, plain), so each row of files_location is one INSERT
// line: a line-based reader is enough, and the uri value names the bucket.

const realShapedLocationInsert = `INSERT INTO public.files_location (created, updated, id, name, uri, "default", size) VALUES ('2024-03-07 18:02:11.123456', '2024-03-07 18:02:11.123456', '0b6c6e2a-6a4a-4a52-9b4e-2f0b7b1a1111', 'default-location', 's3://caltechauthors', true, 0);`

func TestParseFileLocationInsert_ReadsNameAndURI(t *testing.T) {
	got, ok, err := parseFileLocationInsert(realShapedLocationInsert)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Name != "default-location" || got.URI != "s3://caltechauthors" {
		t.Errorf("got %+v", got)
	}
}

func TestParseFileLocationInsert_ColumnOrderAndQuotingDoNotMatter(t *testing.T) {
	line := `INSERT INTO public.files_location (uri, "default", name, id) VALUES ('s3://it''s-odd, bucket', false, 'a, b', 'x');`
	got, ok, err := parseFileLocationInsert(line)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.URI != "s3://it's-odd, bucket" || got.Name != "a, b" {
		t.Errorf("commas and doubled quotes inside a value must survive: %+v", got)
	}
}

func TestParseFileLocationInsert_NullURIIsEmpty(t *testing.T) {
	got, ok, err := parseFileLocationInsert(`INSERT INTO public.files_location (name, uri) VALUES ('n', NULL);`)
	if err != nil || !ok || got.URI != "" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}

func TestParseFileLocationInsert_OtherLinesAreNotLocations(t *testing.T) {
	for _, line := range []string{
		"", "-- a comment", "CREATE TABLE public.files_location (",
		"INSERT INTO public.files_bucket (id) VALUES ('x');",
		"INSERT INTO public.files_location_other (uri) VALUES ('s3://b');",
	} {
		if _, ok, err := parseFileLocationInsert(line); ok || err != nil {
			t.Errorf("%q: ok=%v err=%v, want neither", line, ok, err)
		}
	}
}

// A files_location row the reader cannot understand is an error, never a quiet
// "no locations": the guard must not pass on what it could not read.
func TestParseFileLocationInsert_UnreadableRowIsAnError(t *testing.T) {
	for name, line := range map[string]string{
		"no uri column":        `INSERT INTO public.files_location (name, id) VALUES ('n', 'x');`,
		"count mismatch":       `INSERT INTO public.files_location (name, uri) VALUES ('n');`,
		"unterminated string":  `INSERT INTO public.files_location (name, uri) VALUES ('n', 's3://b);`,
		"not column-inserts":   `INSERT INTO public.files_location VALUES ('a', 'b');`,
		"no values":            `INSERT INTO public.files_location (name, uri);`,
		"missing closing semi": `INSERT INTO public.files_location (name, uri) VALUES ('n', 's3://b')`,
	} {
		if _, ok, err := parseFileLocationInsert(line); err == nil || ok {
			t.Errorf("%s: want an error, got ok=%v err=%v", name, ok, err)
		}
	}
}

func TestParseFileLocationLines_AllRowsAndOnlyRows(t *testing.T) {
	out := "noise from a shell\n" + realShapedLocationInsert + "\n" +
		`INSERT INTO public.files_location (name, uri) VALUES ('scratch', '/var/lib/files');` + "\n" +
		"clasm-scan-rc=0\n"
	got, err := parseFileLocationLines(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].URI != "s3://caltechauthors" || got[1].URI != "/var/lib/files" {
		t.Errorf("got %+v", got)
	}
	if _, err := parseFileLocationLines("INSERT INTO public.files_location (name) VALUES ('n');\n"); err == nil {
		t.Errorf("an unreadable row among the lines must fail the whole read")
	}
}

func TestS3BucketOfURI(t *testing.T) {
	for uri, want := range map[string]string{
		"s3://caltechauthors":  "caltechauthors",
		"s3://caltechauthors/": "caltechauthors",
		"s3://sql-backups.library.caltech.edu/caltechauthors-v13/x": "sql-backups.library.caltech.edu",
		"  s3://padded  ": "padded",
	} {
		if got, ok := s3BucketOfURI(uri); !ok || got != want {
			t.Errorf("%q: got %q ok=%v, want %q", uri, got, ok, want)
		}
	}
	for _, uri := range []string{"", "/var/lib/invenio/files", "file:///x", "https://example.org/b", "s3://", "s3:///x", "S3://upper"} {
		if got, ok := s3BucketOfURI(uri); ok {
			t.Errorf("%q: want not an S3 bucket, got %q", uri, got)
		}
	}
}

func TestBuildLocationScanCommands(t *testing.T) {
	local := buildLocalLocationScanCommand("/var/tmp/clasm-sql-restore.sql")
	for _, want := range []string{"files_location", "grep -a", "'/var/tmp/clasm-sql-restore.sql'", "clasm-scan-rc="} {
		if !strings.Contains(local, want) {
			t.Errorf("local scan missing %q: %s", want, local)
		}
	}
	// grep exits 1 when nothing matches, which is a clean empty scan, not a failure.
	if !strings.Contains(local, `if [ "$rc" -eq 1 ]; then rc=0; fi`) {
		t.Errorf("the local scan must treat grep's exit 1 as no rows: %s", local)
	}
	if strings.Contains(local, "aws s3") {
		t.Errorf("the local scan must not touch S3: %s", local)
	}

	gz := buildStreamLocationScanCommand("my-bucket", "src/b-2026-09-01.sql.gz")
	for _, want := range []string{"files_location", "pipefail", "aws s3 cp --only-show-errors 's3://my-bucket/src/b-2026-09-01.sql.gz' -", "gunzip -c", "grep -a", "clasm-scan-rc="} {
		if !strings.Contains(gz, want) {
			t.Errorf("stream scan missing %q: %s", want, gz)
		}
	}
	if !strings.Contains(gz, "|| [ $? -eq 1 ]") {
		t.Errorf("the stream scan must treat grep's exit 1 as no rows: %s", gz)
	}
	plain := buildStreamLocationScanCommand("my-bucket", "src/legacy.sql")
	if strings.Contains(plain, "gunzip") {
		t.Errorf("a bare .sql key is not gzip: %s", plain)
	}
	// Nothing may be written to the instance's disk by a stream scan.
	for _, c := range []string{gz, plain} {
		if strings.Contains(c, remoteRestoreDownloadPath) || strings.Contains(c, remoteRestoreSQLPath) || strings.Contains(c, "> /") {
			t.Errorf("a stream scan must write nothing: %s", c)
		}
	}
}

func scanFake(stdout string, status types.CommandInvocationStatus) *fakeSSMClient {
	return &fakeSSMClient{
		commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{{substring: "files_location", stdout: stdout, status: status}},
	}
}

func TestScanFileLocations_ReturnsTheRowsOfTheLocalFileAndTheStream(t *testing.T) {
	out := realShapedLocationInsert + "\nclasm-scan-rc=0\n"
	for name, run := range map[string]func(*fakeSSMClient) ([]FileLocation, error){
		"local": func(f *fakeSSMClient) ([]FileLocation, error) {
			return ScanLocalFileLocations(context.Background(), f, "i-1", remoteRestoreSQLPath)
		},
		"stream": func(f *fakeSSMClient) ([]FileLocation, error) {
			return ScanBackupFileLocations(context.Background(), f, "i-1", "b", "src/x.sql.gz")
		},
	} {
		got, err := run(scanFake(out, types.CommandInvocationStatusSuccess))
		if err != nil || len(got) != 1 || got[0].URI != "s3://caltechauthors" {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
}

func TestScanFileLocations_NoRowsIsAnEmptyResult(t *testing.T) {
	got, err := ScanLocalFileLocations(context.Background(), scanFake("clasm-scan-rc=0\n", types.CommandInvocationStatusSuccess), "i-1", remoteRestoreSQLPath)
	if err != nil || len(got) != 0 {
		t.Errorf("a clean scan with no rows is empty, not an error: %+v, %v", got, err)
	}
}

// A scan that did not finish cleanly says nothing about the dump, so it is an
// error: a missing or non-zero sentinel (the download or gunzip failed, with the
// remote output as the reason) and a failed command both.
func TestScanFileLocations_AScanThatDidNotFinishIsAnError(t *testing.T) {
	for name, tc := range map[string]struct {
		stdout string
		status types.CommandInvocationStatus
	}{
		"no sentinel":        {realShapedLocationInsert + "\n", types.CommandInvocationStatusSuccess},
		"non-zero sentinel":  {"fatal error: Unable to locate credentials\nclasm-scan-rc=1\n", types.CommandInvocationStatusSuccess},
		"command failed":     {"boom", types.CommandInvocationStatusFailed},
		"truncated sentinel": {"clasm-scan-rc=", types.CommandInvocationStatusSuccess},
	} {
		_, err := ScanBackupFileLocations(context.Background(), scanFake(tc.stdout, tc.status), "i-1", "b", "src/x.sql.gz")
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	_, err := ScanBackupFileLocations(context.Background(), scanFake("fatal error: Unable to locate credentials\nclasm-scan-rc=1\n", types.CommandInvocationStatusSuccess), "i-1", "b", "src/x.sql.gz")
	if err == nil || !strings.Contains(err.Error(), "Unable to locate credentials") {
		t.Errorf("the remote message is the reason, got %v", err)
	}
}

// A command that failed is a failed scan even if its output looks complete.
func TestScanFileLocations_AFailedCommandIsAnErrorEvenWithACompleteLookingOutput(t *testing.T) {
	_, err := ScanBackupFileLocations(context.Background(), scanFake(realShapedLocationInsert+"\nclasm-scan-rc=0\n", types.CommandInvocationStatusFailed), "i-1", "b", "src/x.sql.gz")
	if err == nil {
		t.Fatal("a failed command must be an error")
	}
}

// Output with no sentinel at all is an error however it ends: one whose tail would
// read as "0" if the missing sentinel were not checked first.
func TestScanFileLocations_NoSentinelIsAnErrorEvenWhenTheTailLooksLikeZero(t *testing.T) {
	_, err := ScanLocalFileLocations(context.Background(), scanFake("xxxxxxxxxxxxx0", types.CommandInvocationStatusSuccess), "i-1", remoteRestoreSQLPath)
	if err == nil {
		t.Fatal("want an error")
	}
}
