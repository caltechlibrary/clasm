package workflow

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// sameS3Client returns a newS3Client factory that ignores the requested
// region and always returns client -- fakes don't actually filter by
// region, so every existing test can keep using one fakeS3Client
// regardless of BucketRegion's discovered region.
func sameS3Client(client awsclient.S3API) func(context.Context, string) (awsclient.S3API, error) {
	return func(context.Context, string) (awsclient.S3API, error) { return client, nil }
}

func nowUnix() int64 { return time.Now().Unix() }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func recentFindOutput(now int64) string {
	// two files, both younger than any reasonable threshold
	return "1024\t" + itoa(now-3600) + "\t/opt/rdm_sql_backups/recent-1.sql.gz\n" +
		"2048\t" + itoa(now-7200) + "\t/opt/rdm_sql_backups/recent-2.sql.gz\n"
}

var errUnavailable = errors.New("SSM unavailable")

// Instance selection (DESIGN.md's full conversion punch list, Picker
// tier) now runs a real bubbletea Program (tui.RunPicker), which can't
// be driven by a test's pipe input -- see internal/tui/picker_test.go
// for that component's own thorough test suite. Tests below exercise
// everything once an instance is already resolved via the unexported
// backupArchiveAndTrim; BackupArchiveAndTrim's own picker-selection step
// is covered only by manual/interactive verification, the same accepted
// limitation power_state.go's/terminate_instance.go's own conversions
// already have.

func TestBackupArchiveAndTrim_DryRunEmptyResult(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + // directory
		"my-backup-bucket\n" + // bucket
		"90\n" // age threshold (nothing is 90 days old in the fixture)

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No files match") {
		t.Errorf("expected a no-matches message, got:\n%s", buf.String())
	}
	if ssmClient.sendCommandCalls() != 2 {
		t.Errorf("sendCommandCalls = %d, want 2 (CLI check, list command)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_PreFillsDirectoryFromMatchingRule(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "rdm-prod-01", Region: "us-east-1"}
	rules := []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	input := "\n" + // accept the pre-filled default directory
		"my-backup-bucket\n" + // bucket
		"90\n" // age threshold (nothing is 90 days old in the fixture)

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, rules, BackupHistory{}, le, buf)
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

func TestBackupArchiveAndTrim_HistoryDirectoryTakesPriorityOverRule(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "rdm-prod-01", Region: "us-east-1"}
	rules := []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	hist := BackupHistory{LastDirectoryByInstance: map[string]string{"i-1": "/opt/actual-last-used"}}
	input := "\n" + // accept the pre-filled default directory
		"my-backup-bucket\n" +
		"90\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, rules, hist, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Accessible-mode prompts don't echo the default value's text, only
	// the static label -- the real signal that the recalled directory
	// (not the Name-pattern rule's) actually got used as the default is
	// which path the "find" command sent to SSM references, matching
	// TestBackupArchiveAndTrim_PreFillsDirectoryFromMatchingRule's own
	// verification shape.
	var findCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "find ") {
			findCmd = c
		}
	}
	if !strings.Contains(findCmd, "/opt/actual-last-used") {
		t.Errorf("find command = %q, want it to reference the recalled directory, not the rule's", findCmd)
	}
	if strings.Contains(findCmd, "/opt/rdm_sql_backups") {
		t.Errorf("find command = %q, want the rule's directory NOT used since history takes priority", findCmd)
	}
}

func TestBackupArchiveAndTrim_SavesInstanceAndDirectoryAfterDirectoryPrompt(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	var savedInstanceID, savedDirectory string
	saveCalls := 0
	hist := BackupHistory{Save: func(instanceID, directory string) error {
		saveCalls++
		savedInstanceID, savedDirectory = instanceID, directory
		return nil
	}}
	input := "/opt/rdm_sql_backups\n" +
		"my-backup-bucket\n" +
		"90\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, hist, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saveCalls != 1 {
		t.Fatalf("Save called %d times, want 1", saveCalls)
	}
	if savedInstanceID != "i-1" || savedDirectory != "/opt/rdm_sql_backups" {
		t.Errorf("Save(%q, %q), want Save(%q, %q)", savedInstanceID, savedDirectory, "i-1", "/opt/rdm_sql_backups")
	}
}

func TestBackupArchiveAndTrim_SaveErrorIsAWarningNotFatal(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	hist := BackupHistory{Save: func(instanceID, directory string) error {
		return errors.New("disk full")
	}}
	input := "/opt/rdm_sql_backups\n" +
		"my-backup-bucket\n" +
		"90\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, hist, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v (a failed history save must not abort the workflow)", err)
	}
	if !strings.Contains(buf.String(), "disk full") {
		t.Errorf("expected the save error to be reported as a warning, got:\n%s", buf.String())
	}
}

func TestBackupArchiveAndTrim_NoMatchingRuleLeavesPromptRequired(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newt-machine-test", Region: "us-east-1"}
	rules := []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	input := "\n" + // blank -- no default configured, rejected
		"/opt/newt/backups\n" + // retry, accepted
		"my-backup-bucket\n" +
		"90\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, rules, BackupHistory{}, le, buf)
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

func TestBackupArchiveAndTrim_AbortsWhenBucketInaccessible(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" + // directory
		"my-backup-bucket\n" + // bucket
		"90\n" // age threshold

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{headBucketErr: errors.New("Forbidden")}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when the S3 bucket is inaccessible")
	}
	if !strings.Contains(err.Error(), "my-backup-bucket") {
		t.Errorf("expected the bucket name in the error, got: %v", err)
	}
	if ssmClient.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the CLI check; the dry-run list must not run before the bucket check)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_HappyPath(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\n" +
		"my-backup-bucket\n" +
		"7\n" +
		"i-1\n" // type-to-confirm with the instance ID

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: "/opt/rdm_sql_backups: 1 GiB trimmed\n"},
		},
	}
	s3Client := &fakeS3Client{objects: map[string]int64{"newauthors/old-1.sql.gz": 1048576}}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
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
	if ssmClient.sendCommandCalls() != 5 {
		t.Errorf("sendCommandCalls = %d, want 5 (CLI check, list, upload, delete, fstrim)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_UsesBucketRegionScopedS3Client(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	// The bucket lives in us-west-2 -- a different region than the
	// instance's (us-east-1) and different from whatever region the
	// probe client happens to be scoped to, exactly the mismatch that
	// caused MovedPermanently in real testing.
	probeClient := &fakeS3Client{bucketLocation: "us-west-2"}
	realClient := &fakeS3Client{objects: map[string]int64{"newauthors/old-1.sql.gz": 1048576}}
	var factoryRegion string
	newS3Client := func(ctx context.Context, region string) (awsclient.S3API, error) {
		factoryRegion = region
		return realClient, nil
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, probeClient, newS3Client, inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if factoryRegion != "us-west-2" {
		t.Errorf("newS3Client was called with region %q, want %q (the bucket's actual region)", factoryRegion, "us-west-2")
	}
	if realClient.headBucketCalls == 0 {
		t.Error("expected the region-scoped client to be used for the HeadBucket access check")
	}
	if realClient.headObjectCalls == 0 {
		t.Error("expected the region-scoped client to be used for HeadObject verification")
	}
	if probeClient.headBucketCalls != 0 {
		t.Error("expected the probe client (used only for BucketRegion) not to be used for HeadBucket")
	}
}

func TestBackupArchiveAndTrim_TypeToConfirmMismatchCancels(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\nwrong\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1024\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
		},
	}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ssmClient.sendCommandCalls() != 2 {
		t.Errorf("sendCommandCalls = %d, want 2 (CLI check, list command; upload must not run)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_PartialVerificationFailure(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1000\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/good.sql.gz\n" +
					"2000\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/bad.sql.gz\n"},
			{substring: "aws s3 cp --only-show-errors '/opt/rdm_sql_backups/good.sql.gz'", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/good.sql.gz\t1000\n"},
			{substring: "aws s3 cp --only-show-errors '/opt/rdm_sql_backups/bad.sql.gz'", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/bad.sql.gz\t2000\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	// bad.sql.gz is missing from the bucket -- verification fails for it
	s3Client := &fakeS3Client{objects: map[string]int64{"newauthors/good.sql.gz": 1000}}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
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

func TestBackupArchiveAndTrim_UntaggedInstanceUsesIDAsKeyPrefix(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-untagged", Name: "", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-untagged\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\ti-untagged/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	s3Client := &fakeS3Client{objects: map[string]int64{"i-untagged/old-1.sql.gz": 1048576}}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uploadCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "aws s3 cp") {
			uploadCmd = c
		}
	}
	if !strings.Contains(uploadCmd, "s3://my-backup-bucket/i-untagged/old-1.sql.gz") {
		t.Errorf("upload command = %q, want the instance ID used as the key prefix for an untagged instance", uploadCmd)
	}
}

func TestBackupArchiveAndTrim_BucketPickerOffersKnownBuckets(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	// directory, bucket-picker choice "2" (zeta-bucket, second in the
	// alphabetically-sorted list: 1=alpha-bucket, 2=zeta-bucket, 3=Other),
	// age threshold, type-to-confirm.
	input := "/opt/rdm_sql_backups\n2\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	s3Client := &fakeS3Client{
		buckets: []s3types.Bucket{{Name: aws.String("alpha-bucket")}, {Name: aws.String("zeta-bucket")}},
		objects: map[string]int64{"newauthors/old-1.sql.gz": 1048576},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uploadCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "aws s3 cp") {
			uploadCmd = c
		}
	}
	if !strings.Contains(uploadCmd, "s3://zeta-bucket/") {
		t.Errorf("upload command = %q, want it to reference zeta-bucket (option 2 in the sorted bucket list)", uploadCmd)
	}
}

func TestBackupArchiveAndTrim_BucketPickerOtherFallsBackToFreeText(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	// directory, bucket-picker choice "2" (Other -- 1=alpha-bucket,
	// 2=Other, since only one real bucket is offered), the typed bucket
	// name, age threshold, type-to-confirm.
	input := "/opt/rdm_sql_backups\n2\ntyped-bucket-name\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	s3Client := &fakeS3Client{
		buckets: []s3types.Bucket{{Name: aws.String("alpha-bucket")}},
		objects: map[string]int64{"newauthors/old-1.sql.gz": 1048576},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uploadCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "aws s3 cp") {
			uploadCmd = c
		}
	}
	if !strings.Contains(uploadCmd, "s3://typed-bucket-name/") {
		t.Errorf("upload command = %q, want it to reference the typed bucket name", uploadCmd)
	}
}

func TestBackupArchiveAndTrim_BucketPickerFallsBackToFreeTextOnListError(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	// directory, then the bucket free-text prompt directly (no picker,
	// since ListBuckets fails), age threshold, type-to-confirm.
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}
	s3Client := &fakeS3Client{
		listBucketsErr: errors.New("access denied"),
		objects:        map[string]int64{"newauthors/old-1.sql.gz": 1048576},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uploadCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "aws s3 cp") {
			uploadCmd = c
		}
	}
	if !strings.Contains(uploadCmd, "s3://my-backup-bucket/") {
		t.Errorf("upload command = %q, want the free-text bucket name used when the bucket list can't be fetched", uploadCmd)
	}
}

// TestBackupArchiveAndTrim_CancellingBucketPickerReturnsToMenu is a
// regression test for a bug where hitting 'q' to cancel the bucket
// picker exited the whole program instead of returning to the previous
// menu, like cancelling the instance picker one step earlier already
// does. promptBackupBucket's own huh.Select Quit keybinding can't be
// driven through the accessible-mode pipe path (see
// promptBackupBucketFunc's doc comment), so this substitutes a fake
// through that seam to simulate the cancellation directly.
func TestBackupArchiveAndTrim_CancellingBucketPickerReturnsToMenu(t *testing.T) {
	orig := promptBackupBucketFunc
	defer func() { promptBackupBucketFunc = orig }()
	promptBackupBucketFunc = func(ctx context.Context, w io.Writer, s3Client awsclient.S3API, newS3Client func(context.Context, string) (awsclient.S3API, error), input io.Reader, output io.Writer) (string, error) {
		return "", huh.ErrUserAborted
	}

	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\n" // directory only -- cancelled at the bucket step

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(nowUnix())}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("expected cancelling the bucket picker to return nil (back to the previous menu), got: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled.") {
		t.Errorf("expected a Cancelled. message, got:\n%s", buf.String())
	}
}

func TestBackupArchiveAndTrim_AbortsWhenAWSCLIMissing(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, le, buf := newPipeEditor("") // nothing should be needed

	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when the AWS CLI is missing on the target instance")
	}
	if !strings.Contains(err.Error(), "AWS CLI") || !strings.Contains(err.Error(), "i-1") {
		t.Errorf("expected an actionable error naming the instance and the AWS CLI, got: %v", err)
	}
	if ssmClient.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the CLI check; no directory/age/bucket prompts should even matter)", ssmClient.sendCommandCalls())
	}
}

func TestBackupArchiveAndTrim_SSMUnavailablePropagatesError(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{sendCommandErr: errUnavailable}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when SSM is unavailable for the initial listing")
	}
}

func TestBackupArchiveAndTrim_NoInstances(t *testing.T) {
	term, _, buf := newPipeEditor("")
	err := BackupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": &fakeSSMClient{}}, &fakeS3Client{}, nil, nil, nil, BackupHistory{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}
