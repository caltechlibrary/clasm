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

// TestBackupArchiveAndTrim_NothingOldEnoughStillCopiesEverything is the
// regression test for the bug this phase exists to fix (DR-0170): with
// no file old enough to trim, the workflow used to print "No files match
// the age threshold. Nothing to do." and exit having copied nothing --
// silently leaving the newest, most valuable dump only on EBS. Every
// file must now be copied regardless of age; the threshold governs only
// what may be deleted afterwards.
func TestBackupArchiveAndTrim_NothingOldEnoughStillCopiesEverything(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	now := nowUnix()
	input := "/opt/rdm_sql_backups\n" + // directory
		"my-backup-bucket\n" + // bucket
		"90\n" + // trim threshold (nothing is 90 days old in the fixture)
		"y\n" // nothing will be deleted, so this is a plain confirm

	term, le, buf := newPipeEditor(input)
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(now)},
			{substring: "recent-1.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/recent-1.sql.gz\t1024\n"},
			{substring: "recent-2.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/recent-2.sql.gz\t2048\n"},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "No files match") {
		t.Errorf("the age threshold must not gate the copy at all, got:\n%s", out)
	}
	// CLI check, list, two uploads. No delete, no fstrim.
	if ssmClient.sendCommandCalls() != 4 {
		t.Errorf("sendCommandCalls = %d, want 4 (CLI check, list, one upload per file)", ssmClient.sendCommandCalls())
	}
	for _, key := range []string{"newauthors/recent-1.sql.gz", "newauthors/recent-2.sql.gz"} {
		if _, ok := s3Client.objects[key]; !ok {
			t.Errorf("%s never reached the bucket; objects = %v", key, s3Client.objects)
		}
	}
	if !strings.Contains(out, "Copied 2 file(s)") {
		t.Errorf("expected both files reported as copied, got:\n%s", out)
	}
	if !strings.Contains(out, "deleted 0 local file(s)") {
		t.Errorf("expected nothing deleted, got:\n%s", out)
	}
}

// TestBackupArchiveAndTrim_BlankThresholdCopiesAndDeletesNothing covers
// DR-0170 decision 4's blank answer, and decision 6's lighter
// confirmation: a copy-only run is not destructive, so it asks a plain
// yes/no rather than requiring the instance name typed out. The input
// below deliberately supplies "y", not "i-1".
func TestBackupArchiveAndTrim_BlankThresholdCopiesAndDeletesNothing(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\n" +
		"my-backup-bucket\n" +
		"\n" + // blank: keep every local copy
		"y\n" // plain confirm, not type-to-confirm

	term, le, buf := newPipeEditor(input)
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if ssmClient.sendCommandCalls() != 3 {
		t.Errorf("sendCommandCalls = %d, want 3 (CLI check, list, upload) -- no delete, no fstrim", ssmClient.sendCommandCalls())
	}
	if !strings.Contains(out, "deleted 0 local file(s)") {
		t.Errorf("expected nothing deleted, got:\n%s", out)
	}
	if _, ok := s3Client.objects["newauthors/old-1.sql.gz"]; !ok {
		t.Error("a 30-day-old file must still be copied when the trim threshold is blank")
	}
}

// TestBackupArchiveAndTrim_ZeroThresholdTrimsEverythingVerified covers
// the other end of DR-0170 decision 4: "0" is an answer, not a blank.
func TestBackupArchiveAndTrim_ZeroThresholdTrimsEverythingVerified(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	now := nowUnix()
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n0\ni-1\n"

	term, le, buf := newPipeEditor(input)
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess, stdout: recentFindOutput(now)},
			{substring: "recent-1.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/recent-1.sql.gz\t1024\n"},
			{substring: "recent-2.sql.gz", status: types.CommandInvocationStatusSuccess, stdout: "OK\tnewauthors/recent-2.sql.gz\t2048\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "deleted 2 local file(s)") {
		t.Errorf("expected both hours-old files deleted at threshold 0, got:\n%s", out)
	}
	// CLI check, list, two uploads, delete, fstrim.
	if ssmClient.sendCommandCalls() != 6 {
		t.Errorf("sendCommandCalls = %d, want 6", ssmClient.sendCommandCalls())
	}
}

// TestBackupArchiveAndTrim_AlreadyArchivedFileIsNotReUploadedButStillTrimmed
// covers DR-0170 decisions 2 and 3 together, including the consequence
// worth stating plainly: a file can be deleted on the strength of an
// object an earlier run uploaded, without this run uploading anything.
// DR-0172 requires it to be visible as such in the dry run.
func TestBackupArchiveAndTrim_AlreadyArchivedFileIsNotReUploadedButStillTrimmed(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	// Seeded up front: an earlier run already archived this file.
	s3Client := &fakeS3Client{objects: map[string]int64{"newauthors/old-1.sql.gz": 1048576}}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, cmd := range ssmClient.sentCommands {
		if strings.Contains(cmd, "aws s3 cp") {
			t.Errorf("an already-archived file must not be re-uploaded, got command:\n%s", cmd)
		}
	}
	if !strings.Contains(out, "already in S3") {
		t.Errorf("expected the already-archived file to be marked in the dry run, got:\n%s", out)
	}
	if !strings.Contains(out, "deleted 1 local file(s)") {
		t.Errorf("an already-archived file that is old enough must still be trimmed, got:\n%s", out)
	}
}

// TestBackupArchiveAndTrim_SizeMismatchIsReUploaded covers DR-0170
// decision 2's overwrite case: same key, different size means the local
// file is the newer truth.
func TestBackupArchiveAndTrim_SizeMismatchIsReUploaded(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	// Present, but a different size than the local file: stale.
	s3Client := &fakeS3Client{objects: map[string]int64{"newauthors/old-1.sql.gz": 999}}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
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

	if err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var uploaded bool
	for _, cmd := range ssmClient.sentCommands {
		if strings.Contains(cmd, "aws s3 cp") {
			uploaded = true
		}
	}
	if !uploaded {
		t.Error("a same-key object of a different size must be overwritten, not skipped")
	}
	if s3Client.objects["newauthors/old-1.sql.gz"] != 1048576 {
		t.Errorf("object size = %d, want the local file's 1048576 after the overwrite", s3Client.objects["newauthors/old-1.sql.gz"])
	}
}

// TestBackupArchiveAndTrim_RecentCopyFailureIsAWarningNotFatal covers
// DR-0171 at the workflow level: a file too new to trim failing to copy
// must not stop the aged files from being archived and trimmed.
func TestBackupArchiveAndTrim_RecentCopyFailureIsAWarningNotFatal(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	now := nowUnix()
	oldEpoch := now - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n" +
					"2048\t" + itoa(now-3600) + "\t/opt/rdm_sql_backups/recent-1.sql.gz\n"},
			{substring: "recent-1.sql.gz", status: types.CommandInvocationStatusFailed, stdout: ""},
			{substring: "old-1.sql.gz", status: types.CommandInvocationStatusSuccess,
				stdout: "OK\tnewauthors/old-1.sql.gz\t1048576\n"},
			{substring: "rm -f", status: types.CommandInvocationStatusSuccess, stdout: ""},
			{substring: "fstrim", status: types.CommandInvocationStatusSuccess, stdout: ""},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("a failed copy of a file that wasn't going to be deleted must not fail the run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "failed to copy") {
		t.Errorf("expected a warning naming the failed file, got:\n%s", out)
	}
	if !strings.Contains(out, "deleted 1 local file(s)") {
		t.Errorf("the aged file must still be archived and trimmed, got:\n%s", out)
	}
}

// TestBackupArchiveAndTrim_AgedCopyFailureIsCalledOut covers DR-0171's
// consequence: a file old enough to trim whose copy failed is correctly
// kept, and must not be kept silently.
func TestBackupArchiveAndTrim_AgedCopyFailureIsCalledOut(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	oldEpoch := nowUnix() - int64(30*24*3600)
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n7\ni-1\n"

	term, le, buf := newPipeEditor(input)
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
		responses: []ssmCommandResponse{
			{substring: "command -v aws", status: types.CommandInvocationStatusSuccess, stdout: "/usr/bin/aws\n"},
			{substring: "find ", status: types.CommandInvocationStatusSuccess,
				stdout: "1048576\t" + itoa(oldEpoch) + "\t/opt/rdm_sql_backups/old-1.sql.gz\n"},
			{substring: "aws s3 cp", status: types.CommandInvocationStatusFailed, stdout: ""},
		},
	}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "old enough to trim and were deliberately left in place") {
		t.Errorf("expected the aged-but-unarchived file to be called out, got:\n%s", out)
	}
	if !strings.Contains(out, "deleted 0 local file(s)") {
		t.Errorf("nothing may be deleted when the copy failed, got:\n%s", out)
	}
	for _, cmd := range ssmClient.sentCommands {
		if strings.Contains(cmd, "rm -f") {
			t.Error("an unverified file must never reach the delete command")
		}
	}
}

func TestBackupArchiveAndTrim_EmptyDirectoryStops(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/rdm_sql_backups\nmy-backup-bucket\n90\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: ""}
	s3Client := &fakeS3Client{}

	err := backupArchiveAndTrim(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Nothing to do") {
		t.Errorf("expected a nothing-to-do message for an empty directory, got:\n%s", buf.String())
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
	// The bucket starts empty and the upload itself puts the object
	// there (s3Sink), rather than the test pre-seeding it -- a pre-seed
	// would make the already-archived pre-pass skip this very upload.
	s3Client := &fakeS3Client{}
	ssmClient := &fakeSSMClient{
		commandID: "cmd-1",
		s3Sink:    s3Client,
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
	realClient := &fakeS3Client{}
	ssmClient.s3Sink = realClient
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
	s3Client := &fakeS3Client{}
	ssmClient.s3Sink = s3Client
	// The instance reports OK for both files, but bad.sql.gz never
	// actually appears in the bucket -- the silent-failure case the
	// tool's own independent HeadObject verification exists to catch.
	ssmClient.s3SinkSkip = []string{"newauthors/bad.sql.gz"}

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
	s3Client := &fakeS3Client{}
	ssmClient.s3Sink = s3Client

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
	}
	ssmClient.s3Sink = s3Client

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
	}
	ssmClient.s3Sink = s3Client

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

// promptLocalTrimDays is tri-state (DR-0170, decision 4): blank and "0"
// are both valid and mean opposite things, which is why it returns a
// separate "requested" flag rather than a bare int.

func TestPromptLocalTrimDays_BlankMeansNoTrim(t *testing.T) {
	_, le, buf := newPipeEditor("\n")

	days, requested, err := promptLocalTrimDays(le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requested {
		t.Error("requested = true, want false -- blank means keep every local copy")
	}
	if days != 0 {
		t.Errorf("days = %d, want 0", days)
	}
}

func TestPromptLocalTrimDays_ZeroMeansTrimEverythingVerified(t *testing.T) {
	_, le, buf := newPipeEditor("0\n")

	days, requested, err := promptLocalTrimDays(le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !requested {
		t.Error("requested = false, want true -- 0 is an answer, not a blank")
	}
	if days != 0 {
		t.Errorf("days = %d, want 0", days)
	}
}

func TestPromptLocalTrimDays_PositiveThreshold(t *testing.T) {
	_, le, buf := newPipeEditor("7\n")

	days, requested, err := promptLocalTrimDays(le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !requested || days != 7 {
		t.Errorf("got (%d, %t), want (7, true)", days, requested)
	}
}

func TestPromptLocalTrimDays_RejectsNegativeAndNonNumeric(t *testing.T) {
	_, le, buf := newPipeEditor("-1\nabc\n3\n")

	days, requested, err := promptLocalTrimDays(le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !requested || days != 3 {
		t.Errorf("got (%d, %t), want (3, true) after two rejected answers", days, requested)
	}
}

func TestPromptLocalTrimDays_QuestionNamesTheEBSVolume(t *testing.T) {
	_, le, buf := newPipeEditor("\n")

	if _, _, err := promptLocalTrimDays(le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// DR-0170, decision 5 / DR-0151's lesson: this prompt deletes local
	// files on the instance, unlike the OpenSearch cleanup prompt it
	// otherwise mirrors, which deletes archived objects in S3.
	if !strings.Contains(buf.String(), "EBS") {
		t.Errorf("expected the question to name the EBS volume explicitly, got:\n%s", buf.String())
	}
}
