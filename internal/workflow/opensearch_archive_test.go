package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// echoingS3Client wraps a *fakeS3Client, auto-satisfying
// VerifySyncedSnapshot's post-sync listing -- whose key prefix embeds a
// runtime-generated, unpredictable timestamped snapshot name
// (archiveOpenSearchSnapshot computes it via time.Now().UTC(), so a test
// can't pre-seed the fake with the exact key ahead of time) -- by echoing
// back one object under whatever prefix it's asked for, but only for a
// non-Delimiter'd call (VerifySyncedSnapshot's own shape). A Delimiter'd
// call (ListArchivedSnapshotPrefixes' own shape) still defers to the
// embedded fakeS3Client's normal, preset-allObjects-based behavior, so
// tests can still control which "already archived" candidates the
// cleanup phase sees.
type echoingS3Client struct {
	*fakeS3Client
}

func (e *echoingS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if aws.ToString(params.Delimiter) == "" {
		e.fakeS3Client.listObjectsV2Calls = append(e.fakeS3Client.listObjectsV2Calls, *params)
		if e.fakeS3Client.listObjectsV2Err != nil {
			return nil, e.fakeS3Client.listObjectsV2Err
		}
		return &s3.ListObjectsV2Output{Contents: []s3types.Object{
			{Key: aws.String(aws.ToString(params.Prefix) + "index-0"), Size: aws.Int64(1024)},
		}}, nil
	}
	return e.fakeS3Client.ListObjectsV2(ctx, params, optFns...)
}

// openSearchHappyPathResponses covers every distinct remote command
// archiveOpenSearchSnapshot's happy path sends, matched by a substring
// stable regardless of the runtime-generated snapshot name (repo/create/
// poll/delete commands all embed that name, so tests match on each
// command's distinguishing shape instead -- its JSON body or HTTP verb).
func openSearchHappyPathResponses() []ssmCommandResponse {
	return []ssmCommandResponse{
		{substring: "command -v aws", status: types.CommandInvocationStatusSuccess},
		{substring: `"type":"fs"`, status: types.CommandInvocationStatusSuccess},
		{substring: `"indices"`, status: types.CommandInvocationStatusSuccess},
		{substring: "-X GET", status: types.CommandInvocationStatusSuccess, stdout: `{"snapshots":[{"state":"SUCCESS"}]}`},
		{substring: "aws s3 sync", status: types.CommandInvocationStatusSuccess},
		{substring: "-X DELETE", status: types.CommandInvocationStatusSuccess},
	}
}

func TestArchiveOpenSearchSnapshot_HappyPathNoCleanupThreshold(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" + // accept the default directory
		"my-os-bucket\n" + // bucket
		"\n" // blank cleanup threshold -- skip cleanup

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Archived OpenSearch snapshot") {
		t.Errorf("expected a success report, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "Removed") {
		t.Errorf("no cleanup was requested, expected no 'Removed' line, got:\n%s", buf.String())
	}
	for _, call := range s3Client.fakeS3Client.listObjectsV2Calls {
		if aws.ToString(call.Delimiter) == "/" {
			t.Error("no cleanup threshold was given -- ListArchivedSnapshotPrefixes (Delimiter-based listing) must not run")
		}
	}
}

// TestArchiveOpenSearchSnapshot_RegistersRepoWithContainerPathNotHostDirectory
// is a regression test for a real incident (2026-07-29, CaltechAUTHORS
// production, i-0c4c81336aea33d27): the repo-registration call must use
// the fixed container-internal path.repo location
// (DefaultOpenSearchContainerRepoPath), never the operator-typed *host*
// directory -- OpenSearch runs inside the search container and has no
// visibility into host paths at all, so registering with the host
// directory fails path.repo's own check even once path.repo is
// correctly configured. The sync command, which runs directly on the
// host (outside any container), must still use the host directory.
func TestArchiveOpenSearchSnapshot_RegistersRepoWithContainerPathNotHostDirectory(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "/opt/custom_backups_dir\n" + // a host directory distinct from the container path
		"my-os-bucket\n" +
		"\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var registerCmd, syncCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, `"type":"fs"`) {
			registerCmd = c
		}
		if strings.Contains(c, "aws s3 sync") {
			syncCmd = c
		}
	}
	if !strings.Contains(registerCmd, `"location":"/usr/share/opensearch/backups"`) {
		t.Errorf("register-repo command = %q, want location %q (the fixed container path, not the host directory)", registerCmd, DefaultOpenSearchContainerRepoPath)
	}
	if strings.Contains(registerCmd, "/opt/custom_backups_dir") {
		t.Errorf("register-repo command = %q, must NOT use the host directory as location", registerCmd)
	}
	if !strings.Contains(syncCmd, "/opt/custom_backups_dir") {
		t.Errorf("sync command = %q, want the operator-typed host directory (sync runs on the host, outside any container)", syncCmd)
	}
}

func TestArchiveOpenSearchSnapshot_ThresholdGivenButNoMatchingCandidates(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"90\n" // threshold given, but nothing exists yet to match it

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "DRY RUN") {
		t.Errorf("no candidates matched -- expected no dry-run/confirm shown, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "Removed") {
		t.Errorf("no candidates matched -- expected no 'Removed' line, got:\n%s", buf.String())
	}
}

func TestArchiveOpenSearchSnapshot_ThresholdWithRealCandidates_CleansUpAfterNewSnapshot(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"30\n" + // threshold, matches the old fixture snapshot below
		"i-1\n" // ConfirmDestructive: type the exact instance ID

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		// Far in the past -- guaranteed older than 30 days regardless of
		// when this test actually runs.
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "DRY RUN") {
		t.Errorf("expected a dry-run listing of the old candidate, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Removed 1 old snapshot") {
		t.Errorf("expected a 'Removed 1 old snapshot' report, got:\n%s", buf.String())
	}
	if len(inner.deleteObjectsCalls) != 1 {
		t.Fatalf("deleteObjectsCalls = %d, want 1 (exactly the pre-captured old candidate)", len(inner.deleteObjectsCalls))
	}
	deletedKey := aws.ToString(inner.deleteObjectsCalls[0].Delete.Objects[0].Key)
	if deletedKey != "newauthors/opensearch-snapshots/rdm-20200101-000000/index-0" {
		t.Errorf("deleted key = %q, want the old candidate's own key", deletedKey)
	}

	// The cleanup delete must run strictly after the new snapshot's own
	// EBS-side delete (DESIGN.md step 10, "runs after step 9, never
	// before") -- assert ordering via the SSM command sequence (delete
	// snapshot, "-X DELETE") preceding the S3 DeleteObjects call, which
	// we can only observe indirectly here: at minimum, both must have
	// happened (checked above) and no error must have surfaced from
	// either.
	var sawDeleteSnapshot bool
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "-X DELETE") {
			sawDeleteSnapshot = true
		}
	}
	if !sawDeleteSnapshot {
		t.Error("expected the EBS-side DeleteSnapshot SSM command to have been sent")
	}
}

func TestArchiveOpenSearchSnapshot_ConfirmMismatchCancelsBeforeSnapshotCreated(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"30\n" +
		"wrong-identifier\n" // mismatch -- ConfirmDestructive cancels, single attempt

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a Cancelled message, got:\n%s", buf.String())
	}
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, `"indices"`) {
			t.Error("a mismatched confirm must cancel the entire run before the new snapshot is even created")
		}
	}
	if len(inner.deleteObjectsCalls) != 0 {
		t.Error("a mismatched confirm must not delete anything")
	}
}

func TestArchiveOpenSearchSnapshot_BucketInaccessibleAbortsBeforeRepoRegistration(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{headBucketErr: errors.New("Forbidden")}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when the S3 bucket is inaccessible")
	}
	if ssmClient.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the CLI check; repo registration must not run before the bucket check)", ssmClient.sendCommandCalls())
	}
}

func TestArchiveOpenSearchSnapshot_FailedSnapshotStateAbortsBeforeSyncDeleteCleanup(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"\n"

	responses := []ssmCommandResponse{
		{substring: "command -v aws", status: types.CommandInvocationStatusSuccess},
		{substring: `"type":"fs"`, status: types.CommandInvocationStatusSuccess},
		{substring: `"indices"`, status: types.CommandInvocationStatusSuccess},
		{substring: "-X GET", status: types.CommandInvocationStatusSuccess, stdout: `{"snapshots":[{"state":"FAILED"}]}`},
		{substring: "aws s3 sync", status: types.CommandInvocationStatusSuccess},
		{substring: "-X DELETE", status: types.CommandInvocationStatusSuccess},
	}
	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: responses}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when the snapshot ends in state FAILED")
	}
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "aws s3 sync") {
			t.Error("a FAILED snapshot state must abort before the sync phase")
		}
		if strings.Contains(c, "-X DELETE") {
			t.Error("a FAILED snapshot state must abort before the EBS-side delete")
		}
	}
}

func TestArchiveOpenSearchSnapshot_SyncFailureAbortsBeforeEBSDelete(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"\n"

	responses := []ssmCommandResponse{
		{substring: "command -v aws", status: types.CommandInvocationStatusSuccess},
		{substring: `"type":"fs"`, status: types.CommandInvocationStatusSuccess},
		{substring: `"indices"`, status: types.CommandInvocationStatusSuccess},
		{substring: "-X GET", status: types.CommandInvocationStatusSuccess, stdout: `{"snapshots":[{"state":"SUCCESS"}]}`},
		{substring: "aws s3 sync", status: types.CommandInvocationStatusFailed},
		{substring: "-X DELETE", status: types.CommandInvocationStatusSuccess},
	}
	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: responses}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err == nil {
		t.Fatal("expected an error when the sync phase fails")
	}
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, "-X DELETE") {
			t.Error("a sync failure must abort before the EBS-side delete -- the local snapshot must survive an unverified sync")
		}
	}
}
