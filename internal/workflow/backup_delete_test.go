package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestBuildDeleteCommand_QuotesPaths(t *testing.T) {
	cmd := buildDeleteCommand([]string{"/opt/rdm_sql_backups/foo.sql.gz", "/opt/rdm_sql_backups/it's a trap.sql.gz"})
	if !strings.Contains(cmd, "rm -f '/opt/rdm_sql_backups/foo.sql.gz'") {
		t.Errorf("expected a quoted rm command for the first path, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, `rm -f '/opt/rdm_sql_backups/it'\''s a trap.sql.gz'`) {
		t.Errorf("expected the second path's embedded quote to be escaped, got:\n%s", cmd)
	}
}

func TestDeleteVerifiedFiles_Success(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	err := DeleteVerifiedFiles(context.Background(), fake, "i-1", []string{"/opt/rdm_sql_backups/foo.sql.gz"}, testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteVerifiedFiles_NoOpOnEmptyList(t *testing.T) {
	fake := &fakeSSMClient{}
	err := DeleteVerifiedFiles(context.Background(), fake, "i-1", nil, testPollInterval, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 0 {
		t.Error("SendCommand was called despite an empty file list")
	}
}

func TestDeleteVerifiedFiles_CommandFailure(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := DeleteVerifiedFiles(context.Background(), fake, "i-1", []string{"/opt/rdm_sql_backups/foo.sql.gz"}, testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the delete command fails")
	}
}
