package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestBuildUploadCommand_QuotesPathsAndIncludesBucket(t *testing.T) {
	files := []BackupFile{
		{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024},
		{Path: "/opt/rdm_sql_backups/it's a trap.sql.gz", SizeBytes: 2048},
	}
	cmd := buildUploadCommand(files, "my-backup-bucket")

	if !strings.Contains(cmd, "'/opt/rdm_sql_backups/foo.sql.gz'") {
		t.Errorf("expected a quoted first path, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, `'it'\''s a trap.sql.gz'`) {
		t.Errorf("expected the second path's embedded quote to be escaped, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "my-backup-bucket") {
		t.Errorf("expected the bucket name in the command, got:\n%s", cmd)
	}
}

func TestParseUploadResults(t *testing.T) {
	output := "OK\tfoo.sql.gz\t1024\n" +
		"FAIL\tbar.sql.gz\t0\n"

	got := parseUploadResults(output)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if !got[0].OK || got[0].Key != "foo.sql.gz" || got[0].SizeBytes != 1024 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].OK || got[1].Key != "bar.sql.gz" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestParseUploadResults_EmptyOutput(t *testing.T) {
	got := parseUploadResults("")
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

func TestUploadBackupFiles_Success(t *testing.T) {
	files := []BackupFile{{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024}}
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "OK\tfoo.sql.gz\t1024\n"}

	got, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[0].OK {
		t.Errorf("got %+v", got)
	}
}

func TestUploadBackupFiles_CommandFailure(t *testing.T) {
	files := []BackupFile{{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024}}
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}

	_, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the upload command itself fails to run")
	}
}
