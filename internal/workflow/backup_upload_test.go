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
	cmd := buildUploadCommand(files, "my-backup-bucket", "newauthors")

	if !strings.Contains(cmd, "'/opt/rdm_sql_backups/foo.sql.gz'") {
		t.Errorf("expected a quoted first path, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, `'newauthors/it'\''s a trap.sql.gz'`) {
		t.Errorf("expected the second path's embedded quote to be escaped, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "my-backup-bucket") {
		t.Errorf("expected the bucket name in the command, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "s3://my-backup-bucket/newauthors/foo.sql.gz") {
		t.Errorf("expected the destination key to be namespaced by the prefix, got:\n%s", cmd)
	}
}

func TestBuildUploadCommand_SuppressesAWSCLIProgressOutput(t *testing.T) {
	files := []BackupFile{{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024}}
	cmd := buildUploadCommand(files, "my-backup-bucket", "newauthors")

	// Without --only-show-errors, `aws s3 cp`'s own \r-updated progress
	// meter can fill ssm:GetCommandInvocation's 24,000-character stdout
	// cap on a large file, pushing this script's own OK/FAIL line off
	// the end before it's ever captured -- silently misreporting a
	// successful upload as failed (see DECISIONS.md, "Suppress aws s3
	// cp's progress output to avoid truncating the OK/FAIL signal").
	if !strings.Contains(cmd, "aws s3 cp --only-show-errors ") {
		t.Errorf("expected aws s3 cp to run with --only-show-errors, got:\n%s", cmd)
	}
}

func TestUploadKey_JoinsPrefixAndBasename(t *testing.T) {
	got := uploadKey("newauthors", "/opt/rdm_sql_backups/foo.sql.gz")
	if got != "newauthors/foo.sql.gz" {
		t.Errorf("got %q, want %q", got, "newauthors/foo.sql.gz")
	}
}

func TestUploadKey_EmptyPrefixUsesBasenameOnly(t *testing.T) {
	got := uploadKey("", "/opt/rdm_sql_backups/foo.sql.gz")
	if got != "foo.sql.gz" {
		t.Errorf("got %q, want %q", got, "foo.sql.gz")
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
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/foo.sql.gz\t1024\n"}

	got, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", "newauthors", testPollInterval, testPollInterval, nil)
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

	_, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", "newauthors", testPollInterval, testPollInterval, nil)
	if err == nil {
		t.Fatal("expected an error when the upload command itself fails to run")
	}
}

func TestUploadBackupFiles_SendsOneCommandPerFile(t *testing.T) {
	files := []BackupFile{
		{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024},
		{Path: "/opt/rdm_sql_backups/bar.sql.gz", SizeBytes: 2048},
	}
	fake := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "foo.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/foo.sql.gz\t1024\n"},
			{substring: "bar.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/bar.sql.gz\t2048\n"},
		},
	}

	got, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", "newauthors", testPollInterval, testPollInterval, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 2 {
		t.Errorf("sendCommandCalls = %d, want 2 (one per file, not one batched command)", fake.sendCommandCalls())
	}
	if len(got) != 2 || !got[0].OK || !got[1].OK || got[0].Key != "newauthors/foo.sql.gz" || got[1].Key != "newauthors/bar.sql.gz" {
		t.Errorf("got %+v", got)
	}
}

func TestUploadBackupFiles_ReportsProgressPerFile(t *testing.T) {
	files := []BackupFile{
		{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1000},
		{Path: "/opt/rdm_sql_backups/bar.sql.gz", SizeBytes: 3000},
	}
	fake := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "foo.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/foo.sql.gz\t1000\n"},
			{substring: "bar.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "FAIL\tnewauthors/bar.sql.gz\t0\n"},
		},
	}

	var progress []UploadProgress
	_, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", "newauthors", testPollInterval, testPollInterval, func(p UploadProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("got %d progress reports, want 2", len(progress))
	}
	if progress[0].Done != 1 || progress[0].Total != 2 || progress[0].BytesDone != 1000 || progress[0].BytesTotal != 4000 || !progress[0].Result.OK {
		t.Errorf("progress[0] = %+v", progress[0])
	}
	if progress[1].Done != 2 || progress[1].Total != 2 || progress[1].BytesDone != 4000 || progress[1].Result.OK {
		t.Errorf("progress[1] = %+v", progress[1])
	}
}

func TestUploadBackupFiles_PrefixesKeyWithInstanceName(t *testing.T) {
	files := []BackupFile{{Path: "/opt/rdm_sql_backups/foo.sql.gz", SizeBytes: 1024}}
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/foo.sql.gz\t1024\n"}

	got, err := UploadBackupFiles(context.Background(), fake, "i-1", files, "my-bucket", "newauthors", testPollInterval, testPollInterval, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Key != "newauthors/foo.sql.gz" {
		t.Errorf("got %+v, want Key %q", got, "newauthors/foo.sql.gz")
	}
	if !strings.Contains(fake.lastCommandText, "s3://my-bucket/newauthors/foo.sql.gz") {
		t.Errorf("command text = %q, want the destination to include the instance prefix", fake.lastCommandText)
	}
}
