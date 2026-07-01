package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/opt/rdm_sql_backups/foo.sql.gz", want: "'/opt/rdm_sql_backups/foo.sql.gz'"},
		{in: "it's a trap.sql", want: `'it'\''s a trap.sql'`},
		{in: "", want: "''"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseBackupFileList(t *testing.T) {
	output := "1048576\t1751328000\t/opt/rdm_sql_backups/caltechauthors-db-1-2026-06-01.sql.gz\n" +
		"2097152\t1751414400\t/opt/rdm_sql_backups/caltechauthors-db-1-2026-06-02.sql.gz\n"

	got, err := parseBackupFileList(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}
	if got[0].Path != "/opt/rdm_sql_backups/caltechauthors-db-1-2026-06-01.sql.gz" || got[0].SizeBytes != 1048576 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if !got[0].ModTime.Equal(time.Unix(1751328000, 0)) {
		t.Errorf("got[0].ModTime = %v, want %v", got[0].ModTime, time.Unix(1751328000, 0))
	}
}

func TestParseBackupFileList_EmptyOutput(t *testing.T) {
	got, err := parseBackupFileList("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d files, want 0", len(got))
	}
}

func TestParseBackupFileList_SkipsMalformedLines(t *testing.T) {
	got, err := parseBackupFileList("not-a-valid-line\n1024\t1751328000\t/ok/file.gz\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/ok/file.gz" {
		t.Errorf("got %+v, want only /ok/file.gz", got)
	}
}

func TestFilterByAge(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	files := []BackupFile{
		{Path: "recent.gz", ModTime: now.AddDate(0, 0, -1)},   // 1 day old
		{Path: "old.gz", ModTime: now.AddDate(0, 0, -10)},     // 10 days old
		{Path: "boundary.gz", ModTime: now.AddDate(0, 0, -7)}, // exactly 7 days old
	}

	got := FilterByAge(files, 7, now)

	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	if len(got) != 1 || paths[0] != "old.gz" {
		t.Errorf("got %v, want only old.gz (strictly older than the 7-day cutoff)", paths)
	}
}

func TestListBackupFiles_Success(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess,
		stdout: "1024\t1751328000\t/opt/rdm_sql_backups/foo.sql.gz\n"}

	got, err := ListBackupFiles(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/opt/rdm_sql_backups/foo.sql.gz" {
		t.Errorf("got %+v", got)
	}
}

func TestListBackupFiles_CommandFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	_, err := ListBackupFiles(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the list command fails")
	}
}
