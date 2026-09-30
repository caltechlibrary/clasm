package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Found live 2026-09-30 on caltechauthors-test-v13: the source-name prompt
// defaults to the target's own Name, accepting it listed an empty S3 prefix,
// and Restore had already deleted the live indices -- it deleted right after
// the index-prefix prompt, before the bucket, source or snapshot was chosen.
// That is the failure DR-0175 decision 2 exists to refuse: never destroy the
// current state before a verified replacement exists. The conflict check and
// its type-to-confirm stay early (they only need the target); the deletion
// itself must wait until the snapshot is chosen, downloaded, owned by the
// service user and registered, and go immediately before the _restore.
//
// Each test below scripts one way the run can stop before that point and
// asserts that no index deletion was sent. The fixture always has a
// conflicting index and always confirms, so the deletion is reachable.

// conflictInput drives the prompts for a run with a conflicting index:
// index-prefix default, the type-to-confirm, directory, bucket, source name,
// and the pick of the (only) snapshot.
const conflictInput = "\n" + "i-1\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"

func runConflictingRestore(t *testing.T, ssmClient *fakeSSMClient, s3Client *fakeS3Client) error {
	t.Helper()
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	term, le, buf := newPipeEditor(conflictInput)
	return restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
}

func failingResponse(substring, stdout string) ssmCommandResponse {
	return ssmCommandResponse{substring: substring, stdout: stdout, status: types.CommandInvocationStatusFailed}
}

func TestRestoreOpenSearchSnapshot_NoSnapshotsFoundDeletesNothing(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{} // the source prefix lists no snapshots
	if err := runConflictingRestore(t, ssmClient, s3Client); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("live indices were deleted although no snapshot was found to replace them; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreOpenSearchSnapshot_FailedSyncDeletesNothing(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	ssmClient.responses = append([]ssmCommandResponse{failingResponse("aws s3 sync", "download failed")}, ssmClient.responses...)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	if err := runConflictingRestore(t, ssmClient, s3Client); err == nil {
		t.Fatal("expected the sync failure to propagate")
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("live indices were deleted although the snapshot never downloaded; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreOpenSearchSnapshot_FailedRegisterDeletesNothing(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	ssmClient.responses = append([]ssmCommandResponse{failingResponse(registerRepoCmdFragment, "repository_exception")}, ssmClient.responses...)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	if err := runConflictingRestore(t, ssmClient, s3Client); err == nil {
		t.Fatal("expected the register failure to propagate")
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("live indices were deleted although the repository could not be registered; sent: %v", ssmClient.sentCommands)
	}
}

// The guard. Moving the deletion later still leaves one window: an index
// prefix that matches live indices but not the snapshot (a cross-instance
// restore with the wrong prefix) would delete them and then restore nothing.
// Once the repository is registered, the snapshot can be asked what it
// holds, so Restore checks that at least one of its indices matches the
// prefix's patterns *before* deleting anything.

const getSnapshotCmdFragment = "-X GET 'localhost:9200/_snapshot/rdm_backup_repo/rdm-20260819-160031'"

func snapshotHolding(indices ...string) string {
	quoted := ""
	for i, n := range indices {
		if i > 0 {
			quoted += ","
		}
		quoted += `"` + n + `"`
	}
	return `{"snapshots":[{"snapshot":"rdm-20260819-160031","state":"SUCCESS","indices":[` + quoted + `]}]}`
}

func withSnapshotListing(f *fakeSSMClient, stdout string, status types.CommandInvocationStatus) {
	f.responses = append([]ssmCommandResponse{{substring: getSnapshotCmdFragment, stdout: stdout, status: status}}, f.responses...)
}

func TestRestoreOpenSearchSnapshot_SnapshotWithNoMatchingIndicesDeletesNothing(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	withSnapshotListing(ssmClient, snapshotHolding("other-rdmrecords-records-record-v1.0.0", "other-users-user-v1.0.0"), types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := runConflictingRestore(t, ssmClient, s3Client)
	if err == nil {
		t.Fatal("expected an error: the snapshot holds nothing matching the index prefix")
	}
	for _, want := range []string{"caltechdata", "rdm-20260819-160031", "other-users-user-v1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q (the prefix, the snapshot, and what the snapshot does hold), got: %v", want, err)
		}
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("live indices were deleted although the snapshot holds nothing that would replace them; sent: %v", ssmClient.sentCommands)
	}
	if commandSent(ssmClient.sentCommands, "/_restore") {
		t.Errorf("a restore was requested for a snapshot with no matching indices; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreOpenSearchSnapshot_SnapshotHoldingMatchingIndicesProceeds(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	withSnapshotListing(ssmClient, snapshotHolding("caltechdata-rdmrecords-records-record-v1.0.0", "other-users-user-v1.0.0"), types.CommandInvocationStatusSuccess)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	if err := runConflictingRestore(t, ssmClient, s3Client); err != nil {
		t.Fatalf("a snapshot that holds a matching index must not be refused: %v", err)
	}
	sent := ssmClient.sentCommands
	if !deleteIndicesCommandSent(sent) || !commandSent(sent, "/_restore") {
		t.Errorf("expected the delete and the restore to go ahead; sent: %v", sent)
	}
	// The listing is asked for after registration and before the delete.
	if !(commandIndex(t, sent, registerRepoCmdFragment) < commandIndex(t, sent, getSnapshotCmdFragment)) {
		t.Errorf("the snapshot must be listed after the repository is registered; sent: %v", sent)
	}
}

func TestRestoreOpenSearchSnapshot_FailedSnapshotListingDeletesNothing(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	withSnapshotListing(ssmClient, `{"error":{"type":"snapshot_missing_exception","reason":"[rdm_backup_repo:rdm-20260819-160031] is missing"},"status":404}`, types.CommandInvocationStatusFailed)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := runConflictingRestore(t, ssmClient, s3Client)
	if err == nil || !strings.Contains(err.Error(), "snapshot_missing_exception") {
		t.Fatalf("expected the listing failure, with OpenSearch's reason, to propagate, got: %v", err)
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("live indices were deleted although the snapshot could not be inspected; sent: %v", ssmClient.sentCommands)
	}
}
