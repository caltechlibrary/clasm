package workflow

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/config"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func nowUnix() int64 { return time.Now().Unix() }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func recentFindOutput(now int64) string {
	// two files, both younger than any reasonable threshold
	return "1024\t" + itoa(now-3600) + "\t/opt/rdm_sql_backups/recent-1.sql.gz\n" +
		"2048\t" + itoa(now-7200) + "\t/opt/rdm_sql_backups/recent-2.sql.gz\n"
}

var errUnavailable = errors.New("SSM unavailable")

func TestBackupArchiveAndTrim_DryRunEmptyResult(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}}
	input := "1\n" + // pick instance
		"/opt/rdm_sql_backups\n" + // directory
		"90\n" + // age threshold (nothing is 90 days old in the fixture)
		"my-backup-bucket\n" // bucket

	term, le, buf := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No files match") {
		t.Errorf("expected a no-matches message, got:\n%s", buf.String())
	}
	if ssmClient.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the list command)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_PreFillsDirectoryFromMatchingRule(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "rdm-prod-01", Region: "us-east-1"}}
	rules := []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	input := "1\n" + // pick instance
		"\n" + // accept the pre-filled default directory
		"90\n" + // age threshold (nothing is 90 days old in the fixture)
		"my-backup-bucket\n" // bucket

	term, le, buf := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "/opt/rdm_sql_backups") {
		t.Errorf("expected the pre-filled default directory to appear in the prompt, got:\n%s", buf.String())
	}
	var findCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "find ") {
			findCmd = c
		}
	}
	if !strings.Contains(findCmd, "/opt/rdm_sql_backups") {
		t.Errorf("find command = %q, want it to reference the pre-filled default directory", findCmd)
	}
}

func TestBackupArchiveAndTrim_NoMatchingRuleLeavesPromptRequired(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newt-machine-test", Region: "us-east-1"}}
	rules := []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	input := "1\n" + // pick instance
		"\n" + // blank -- no default configured, rejected
		"/opt/newt/backups\n" + // retry, accepted
		"90\n" +
		"my-backup-bucket\n"

	term, le, _ := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var findCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "find ") {
			findCmd = c
		}
	}
	if !strings.Contains(findCmd, "/opt/newt/backups") {
		t.Errorf("find command = %q, want it to reference the manually-entered directory", findCmd)
	}
}

func TestBackupArchiveAndTrim_HappyPath(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "1\n" +
		"/opt/rdm_sql_backups\n" +
		"7\n" +
		"my-backup-bucket\n" +
		"i-1\n" // type-to-confirm with the instance ID

	term, le, buf := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\told-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: "/opt/rdm_sql_backups: 1 GiB trimmed\n"},
		},
	}
	s3Client := &fakeS3Client{objects: map[string]int64{"old-1.sql.gz": 1048576}}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1048576") {
		t.Errorf("expected bytes-freed total in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 file") {
		t.Errorf("expected a file count in output, got:\n%s", out)
	}
	// list, upload, delete, fstrim = 4 SendCommand calls
	if ssmClient.sendCommandCalls() != 4 {
		t.Errorf("sendCommandCalls = %d, want 4 (list, upload, delete, fstrim)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_TypeToConfirmMismatchCancels(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "1\n/opt/rdm_sql_backups\n7\nmy-backup-bucket\nwrong\n"

	term, le, _ := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1024\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
		},
	}
	s3Client := &fakeS3Client{}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ssmClient.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the list command; upload must not run)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_PartialVerificationFailure(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "1\n/opt/rdm_sql_backups\n7\nmy-backup-bucket\ni-1\n"

	term, le, buf := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1000\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/good.sql.gz\n" +
					"2000\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/bad.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tgood.sql.gz\t1000\nOK\tbad.sql.gz\t2000\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	// bad.sql.gz is missing from the bucket -- verification fails for it
	s3Client := &fakeS3Client{objects: map[string]int64{"good.sql.gz": 1000}}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 file") {
		t.Errorf("expected 1 file freed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "bad.sql.gz") {
		t.Errorf("expected the failed file to be named in output, got:\n%s", out)
	}
	// the delete command must only reference good.sql.gz's full path, not bad.sql.gz's
	var deleteCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "rm -f") {
			deleteCmd = c
		}
	}
	if !strings.Contains(deleteCmd, "good.sql.gz") || strings.Contains(deleteCmd, "bad.sql.gz") {
		t.Errorf("delete command = %q, want only good.sql.gz's path", deleteCmd)
	}
	// fstrim must still run even though one file failed verification
	fstrimRan := false
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "fstrim") {
			fstrimRan = true
		}
	}
	if !fstrimRan {
		t.Error("fstrim did not run despite a partial verification failure")
	}
}

func TestBackupArchiveAndTrim_SSMUnavailablePropagatesError(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}}
	input := "1\n/opt/rdm_sql_backups\n7\nmy-backup-bucket\n"

	term, le, _ := newPipeEditor(t, input)
	ssmClient := &fakeSSMClient{sendCommandErr: errUnavailable}
	s3Client := &fakeS3Client{}

	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, instances, nil)
	if err == nil {
		t.Fatal("expected an error when SSM is unavailable for the initial listing")
	}
}

func TestBackupArchiveAndTrim_NoInstances(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}, &fakeS3Client{}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestBackupArchiveAndTrim_CancelledPickList(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Region: "us-east-1"}}
	term, le, _ := newPipeEditor(t, "0\n")
	ssmClient := &fakeSSMClient{}
	err := BackupArchiveAndTrim(context.Background(), term, le, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, &fakeS3Client{}, instances, nil)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if ssmClient.sendCommandCalls() != 0 {
		t.Error("an SSM command was sent despite cancelling the pick list")
	}
}
