package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestBuildSyncCommand(t *testing.T) {
	got := buildSyncCommand("/opt/rdm_opensearch_backups", "opensearch-backups.library.caltech.edu", "caltechauthors", "rdm-20260729-000000")
	for _, want := range []string{
		"aws s3 sync --only-show-errors",
		"/opt/rdm_opensearch_backups",
		"s3://opensearch-backups.library.caltech.edu/caltechauthors/opensearch-snapshots/rdm-20260729-000000/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "--delete") {
		t.Errorf("command = %q, must NOT contain --delete (would defeat Restore's dated-backup retention)", got)
	}
}

func TestSyncOpenSearchBackupsToS3_PropagatesFailedStatus(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}
	err := SyncOpenSearchBackupsToS3(context.Background(), fake, "i-1", "/opt/rdm_opensearch_backups", "my-bucket", "caltechauthors", "rdm-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error for a failed SSM status")
	}
}

func TestVerifySyncedSnapshot_ConfirmsNonEmpty(t *testing.T) {
	s3Client := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-1/index-0"), Size: aws.Int64(1024)},
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-1/index-1"), Size: aws.Int64(2048)},
		{Key: aws.String("caltechauthors/opensearch-snapshots/rdm-2/index-0"), Size: aws.Int64(9999)}, // a different snapshot, must not be counted
	}}

	count, bytes, err := VerifySyncedSnapshot(context.Background(), s3Client, "my-bucket", "caltechauthors", "rdm-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if bytes != 3072 {
		t.Errorf("bytes = %d, want 3072", bytes)
	}
}

func TestVerifySyncedSnapshot_ErrorsWhenEmpty(t *testing.T) {
	s3Client := &fakeS3Client{}
	_, _, err := VerifySyncedSnapshot(context.Background(), s3Client, "my-bucket", "caltechauthors", "rdm-1")
	if err == nil {
		t.Fatal("expected an error for an empty sync result")
	}
}

func TestVerifySyncedSnapshot_PropagatesListError(t *testing.T) {
	s3Client := &fakeS3Client{listObjectsV2Err: errors.New("boom")}
	_, _, err := VerifySyncedSnapshot(context.Background(), s3Client, "my-bucket", "caltechauthors", "rdm-1")
	if err == nil {
		t.Fatal("expected an error")
	}
}
