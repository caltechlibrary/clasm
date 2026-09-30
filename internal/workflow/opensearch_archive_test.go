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
		{substring: "id -u", status: types.CommandInvocationStatusSuccess, stdout: "1000 1001\n"},
		{substring: "install -d", status: types.CommandInvocationStatusSuccess},
		{substring: "clasm-owner-probe", status: types.CommandInvocationStatusSuccess, stdout: "clasm-owner-probe 0 yes\n"}, // a clean tree: nothing to repair
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

// TestArchiveOpenSearchSnapshot_IndexPatternsUseProjectTagNotNameTag is a
// regression test for a real incident (2026-08-17, CaltechAUTHORS
// production, i-0c4c81336aea33d27): CreateSnapshot's indices patterns
// were built from inst.Name, silently matching zero real indices when
// an instance's Name tag (a legacy label, "newauthors" for this
// instance) differs from its actual OpenSearch index prefix. The
// instance's Project tag ("caltechauthors") matched the real index
// prefix exactly -- confirmed live: OpenSearch reported the snapshot as
// state SUCCESS with shards.total: 0, an entirely empty backup that
// looked like a working one. This is the identical shape of mistake
// Phase 20.52 already found and fixed once for Postgres db_name/db_user
// defaulting (DECISIONS.md, "Default db_name/db_user to the instance's
// Project tag, not its Name tag") -- same fix here: the OpenSearch
// index-match prefix must prefer inst.Project, falling back to
// inst.Name only when Project is blank. The S3 destination path is
// unrelated and must keep using inst.Name unchanged (DECISIONS.md,
// "CaltechAUTHORS's Name tag drives its S3 upload prefix, by design" --
// cosmetic, already accepted, not part of this fix).
func TestArchiveOpenSearchSnapshot_IndexPatternsUseProjectTagNotNameTag(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Project: "caltechauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var createCmd, syncCmd string
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, `"indices"`) {
			createCmd = c
		}
		if strings.Contains(c, "aws s3 sync") {
			syncCmd = c
		}
	}
	if !strings.Contains(createCmd, "caltechauthors-rdmrecords-") {
		t.Errorf("create-snapshot command = %q, want index patterns built from the Project tag (caltechauthors-*)", createCmd)
	}
	if strings.Contains(createCmd, "newauthors-rdmrecords-") {
		t.Errorf("create-snapshot command = %q, must NOT use the Name tag (newauthors-*) when Project is set", createCmd)
	}
	if !strings.Contains(syncCmd, "newauthors/opensearch-snapshots") {
		t.Errorf("sync command = %q, want the S3 destination path to still use the Name tag (unaffected, cosmetic, unchanged)", syncCmd)
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
		{substring: "id -u", status: types.CommandInvocationStatusSuccess, stdout: "1000 1001\n"},
		{substring: "install -d", status: types.CommandInvocationStatusSuccess},
		{substring: "clasm-owner-probe", status: types.CommandInvocationStatusSuccess, stdout: "clasm-owner-probe 0 yes\n"},
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
		{substring: "id -u", status: types.CommandInvocationStatusSuccess, stdout: "1000 1001\n"},
		{substring: "install -d", status: types.CommandInvocationStatusSuccess},
		{substring: "clasm-owner-probe", status: types.CommandInvocationStatusSuccess, stdout: "clasm-owner-probe 0 yes\n"},
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

// TestRunArchiveOpenSearchSnapshot_CleansUpWithoutPrompting drives Archive
// OpenSearch Snapshot to S3's params-driven core directly (PLAN.md Phase
// 20.64) -- no input/output pipes, since prompting has already happened
// by this point and the cleanup confirmation gate is the injected
// confirm, not a real prompt. Same scenario as
// TestArchiveOpenSearchSnapshot_ThresholdWithRealCandidates_CleansUpAfterNewSnapshot
// one layer up, asserting the extraction changed nothing about the
// actual work done.
func TestRunArchiveOpenSearchSnapshot_CleansUpWithoutPrompting(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}

	term, buf := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	var confirmCalls int
	confirm := func(candidates []SnapshotPrefixInfo) (bool, error) {
		confirmCalls++
		return true, nil
	}

	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 30, true, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if confirmCalls != 1 {
		t.Errorf("confirm called %d times, want 1", confirmCalls)
	}
	if !strings.Contains(buf.String(), "Removed 1 old snapshot") {
		t.Errorf("expected a 'Removed 1 old snapshot' report, got:\n%s", buf.String())
	}
	if len(inner.deleteObjectsCalls) != 1 {
		t.Fatalf("deleteObjectsCalls = %d, want 1 (exactly the pre-captured old candidate)", len(inner.deleteObjectsCalls))
	}
}

// TestRunArchiveOpenSearchSnapshot_ConfirmDeclinedCancelsBeforeSnapshotCreated
// pins that a false confirm result behaves exactly like a declined
// interactive prompt: the run is cancelled before the new snapshot is
// even created, and nothing is deleted.
func TestRunArchiveOpenSearchSnapshot_ConfirmDeclinedCancelsBeforeSnapshotCreated(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}

	term, buf := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	confirm := func(candidates []SnapshotPrefixInfo) (bool, error) { return false, nil }

	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 30, true, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a Cancelled message, got:\n%s", buf.String())
	}
	for _, c := range ssmClient.sentCommands {
		if strings.Contains(c, `"indices"`) {
			t.Error("a declined confirm must cancel the entire run before the new snapshot is even created")
		}
	}
	if len(inner.deleteObjectsCalls) != 0 {
		t.Error("a declined confirm must not delete anything")
	}
}

// TestRunArchiveOpenSearchSnapshotAuto_NeverPromptsAndCleansUp pins the
// non-interactive entry point's whole contract (PLAN.md Phase 20.64),
// the OpenSearch-side analog of TestRunBackupArchiveAndTrimAuto_
// NeverPromptsAndUploads.
func TestRunArchiveOpenSearchSnapshotAuto_NeverPromptsAndCleansUp(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}

	term, buf := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	err := RunArchiveOpenSearchSnapshotAuto(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 30, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Removed 1 old snapshot") {
		t.Errorf("expected a 'Removed 1 old snapshot' report, got:\n%s", buf.String())
	}
	if len(inner.deleteObjectsCalls) != 1 {
		t.Errorf("deleteObjectsCalls = %d, want 1 (confirmed without prompting)", len(inner.deleteObjectsCalls))
	}
}

// TestArchiveOpenSearchSnapshot_ReportsResolvedParamsOnceParamsAreKnown
// is the OpenSearch-side analog of
// TestBackupArchiveAndTrim_ReportsResolvedParamsOnceParamsAreKnown
// (PLAN.md Phase 20.64 item 5).
func TestArchiveOpenSearchSnapshot_ReportsResolvedParamsOnceParamsAreKnown(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n" +
		"30\n" +
		"i-1\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}

	var reported []ArchiveOpenSearchParams
	report := func(p ArchiveOpenSearchParams) { reported = append(reported, p) }

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ArchiveOpenSearchParams{InstanceID: "i-1", Directory: "/opt/rdm_opensearch_backups", Bucket: "my-os-bucket", CleanupDays: 30, CleanupRequested: true}
	if len(reported) != 1 || reported[0] != want {
		t.Errorf("reported = %+v, want exactly one call with %+v", reported, want)
	}
}

// TestArchiveOpenSearchSnapshot_DoesNotReportWhenAbortedBeforeParamsAreKnown
// pins that an abort before params are ever resolved (here: the S3
// bucket inaccessible, which happens before the cleanup prompt) never
// invokes report.
func TestArchiveOpenSearchSnapshot_DoesNotReportWhenAbortedBeforeParamsAreKnown(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	input := "\n" +
		"my-os-bucket\n"

	term, le, buf := newPipeEditor(input)
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{headBucketErr: errors.New("Forbidden")}}

	var reportCalls int
	report := func(p ArchiveOpenSearchParams) { reportCalls++ }

	err := archiveOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, BackupHistory{}, le, buf, report)
	if err == nil {
		t.Fatal("expected an error when the S3 bucket is inaccessible")
	}
	if reportCalls != 0 {
		t.Errorf("report called %d times, want 0 (aborted before params were resolved)", reportCalls)
	}
}

// --- ownership (Phase 20.65 item 5, DR-0176) ---

const archiveEnsureExact = "install -d -o 1000 -g 1001 -m 0775 '/opt/rdm_opensearch_backups'"

func setArchiveOwnerStdout(f *fakeSSMClient, stdout string) {
	for i := range f.responses {
		if f.responses[i].substring == "id -u" {
			f.responses[i].stdout = stdout
			return
		}
	}
	panic("no id -u response scripted")
}

// The directory is ensured before the repository is registered. Registration
// only verifies a write into the top-level directory, so a wrongly owned or
// missing directory passes every pre-flight and the snapshot then fails with
// an opaque 404; ensuring it first is the only thing that protects an
// instance that was never restored to. Ensure only -- no chown -R: residue
// on an existing tree is reported by the readiness check, not repaired by an
// archive (DR-0175 decision 4).
func TestRunArchiveOpenSearchSnapshot_EnsuresDirectoryBeforeRegisteringRepo(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 0, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	lookup := commandIndex(t, sent, "id -u")
	ensure := commandIndex(t, sent, archiveEnsureExact)
	register := commandIndex(t, sent, `"type":"fs"`)
	sync := commandIndex(t, sent, "aws s3 sync")
	if !(lookup < ensure && ensure < register && register < sync) {
		t.Errorf("want lookup < ensure < register < sync, got %d %d %d %d; sent: %v", lookup, ensure, register, sync, sent)
	}
	if commandSent(sent, "chown -R") {
		t.Errorf("an archive must not chown -R the repository; sent: %v", sent)
	}
	if n := countCommandsContaining(sent, "install -d"); n != 1 {
		t.Errorf("install -d sent %d times, want 1", n)
	}
}

// The params-driven core is shared with the non-interactive form (Phase
// 20.64), so the non-interactive path is covered by construction -- pin it.
func TestRunArchiveOpenSearchSnapshotAuto_EnsuresDirectory(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}

	if err := RunArchiveOpenSearchSnapshotAuto(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 0, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !commandSent(ssmClient.sentCommands, archiveEnsureExact) {
		t.Errorf("expected %q; sent: %v", archiveEnsureExact, ssmClient.sentCommands)
	}
}

// DR-0176 decision 5, "before writing anything": a uid mismatch stops the
// run before the directory is touched, before the repository is registered,
// before a snapshot exists -- and before the S3 cleanup, which deletes
// objects. The cleanup is requested here with a real candidate and a
// confirm that would say yes, so a check placed after it would delete an
// old snapshot from S3 and only then refuse.
func TestRunArchiveOpenSearchSnapshot_OwnerUIDMismatchStopsBeforeAnyWriteOrS3Deletion(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	setArchiveOwnerStdout(ssmClient, "1001 1001\n")
	inner := &fakeS3Client{allObjects: []s3types.Object{
		{Key: aws.String("newauthors/opensearch-snapshots/rdm-20200101-000000/index-0")},
	}}
	s3Client := &echoingS3Client{fakeS3Client: inner}
	var confirmCalls int
	confirm := func(candidates []SnapshotPrefixInfo) (bool, error) {
		confirmCalls++
		return true, nil
	}

	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 30, true, confirm)
	if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("expected an error naming both uids, got: %v", err)
	}
	if confirmCalls != 0 {
		t.Errorf("the cleanup confirm ran %d times before the uid check", confirmCalls)
	}
	if len(inner.deleteObjectsCalls) != 0 {
		t.Errorf("S3 objects were deleted before the uid check: %d DeleteObjects calls", len(inner.deleteObjectsCalls))
	}
	for _, f := range []string{"install -d", `"type":"fs"`, `"indices"`, "aws s3 sync", "-X DELETE"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("a command containing %q was sent after a uid mismatch; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

func TestRunArchiveOpenSearchSnapshot_FailedOwnerLookupAbortsBeforeAnyWrite(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: append([]ssmCommandResponse{{substring: "id -u", stdout: "id: 'ubuntu': no such user", status: types.CommandInvocationStatusFailed}}, openSearchHappyPathResponses()...)}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 0, false, nil)
	if err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Fatalf("expected the lookup failure, got: %v", err)
	}
	for _, f := range []string{"install -d", `"type":"fs"`, "aws s3 sync"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q sent after a failed lookup; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

func TestRunArchiveOpenSearchSnapshot_FailedEnsureAbortsBeforeRegistration(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: append([]ssmCommandResponse{{substring: "install -d", stdout: "install: cannot change owner", status: types.CommandInvocationStatusFailed}}, openSearchHappyPathResponses()...)}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/opt/rdm_opensearch_backups", "my-os-bucket", "newauthors", "newauthors", 0, false, nil)
	if err == nil || !strings.Contains(err.Error(), "/opt/rdm_opensearch_backups") {
		t.Fatalf("expected an error naming the directory, got: %v", err)
	}
	for _, f := range []string{`"type":"fs"`, "aws s3 sync"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q sent after a failed ensure; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

// The ensure runs on the directory the operator actually typed, and quotes it.
func TestRunArchiveOpenSearchSnapshot_EnsuresTheOperatorsDirectory(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	if err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, "/srv/os backups", "my-os-bucket", "newauthors", "newauthors", 0, false, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "install -d -o 1000 -g 1001 -m 0775 '/srv/os backups'"; !commandSent(ssmClient.sentCommands, want) {
		t.Errorf("expected %q; sent: %v", want, ssmClient.sentCommands)
	}
}
