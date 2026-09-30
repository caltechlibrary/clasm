package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Found live 2026-09-30 on caltechauthors-test-v13 (real-AWS step 4 of
// Phase 20.65): Restore's single comma-joined DELETE included
// `.ds-caltechauthors-auditlog-audit-log-v1.0.0-000001`, the write index of
// a data stream, and OpenSearch answered HTTP 400 "is the write index for
// data stream [...] and cannot be deleted". A backing index can only go
// with its stream, via DELETE _data_stream/<name>. The unit tests never saw
// this because the fake has no data streams.

const auditBackingIndex = ".ds-caltechdata-auditlog-audit-log-v1.0.0-000001"

func TestDataStreamOfBackingIndex(t *testing.T) {
	tests := []struct {
		index      string
		wantStream string
		wantOK     bool
	}{
		{auditBackingIndex, "caltechdata-auditlog-audit-log-v1.0.0", true},
		{".ds-caltechdata-job-logs-000003", "caltechdata-job-logs", true},
		{"caltechdata-users-user-v3.0.0", "", false},
		{"caltechdata-auditlog-audit-log-v1.0.0", "", false},
		{".ds-caltechdata-job-logs", "", false}, // no generation suffix, not a backing index
	}
	for _, tt := range tests {
		got, ok := dataStreamOfBackingIndex(tt.index)
		if got != tt.wantStream || ok != tt.wantOK {
			t.Errorf("dataStreamOfBackingIndex(%q) = %q, %v; want %q, %v", tt.index, got, ok, tt.wantStream, tt.wantOK)
		}
	}
}

func TestDeleteConflictingIndices_BackingIndexGoesThroughTheDataStreamAPI(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	err := DeleteConflictingIndices(context.Background(), fake, "i-1",
		[]string{auditBackingIndex, "caltechdata-users-user-v3.0.0"}, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stream := commandIndex(t, fake.sentCommands, "-X DELETE 'localhost:9200/_data_stream/caltechdata-auditlog-audit-log-v1.0.0'")
	plain := commandIndex(t, fake.sentCommands, "-X DELETE 'localhost:9200/"+auditBackingIndex+",caltechdata-users-user-v3.0.0?ignore_unavailable=true'")
	if stream > plain {
		t.Errorf("the stream must be deleted before the plain index delete names its backing index (OpenSearch answers 400 for a stream's write index); sent: %v", fake.sentCommands)
	}
}

// Found live 2026-09-30: a restore leaves the audit log's backing index as a
// plain index with no stream (DR-0173), so deleting by stream name removed
// nothing and the later _restore failed with "an open index with same name
// already exists". Every listed name, backing indices included, is therefore
// also deleted by name, after the streams, tolerating "already gone".
func TestDeleteConflictingIndices_AnOrphanedBackingIndexIsAlsoDeletedByName(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	if err := DeleteConflictingIndices(context.Background(), fake, "i-1", []string{auditBackingIndex}, time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.sentCommands) != 2 {
		t.Fatalf("want the stream delete then a plain delete, sent: %v", fake.sentCommands)
	}
	if !strings.Contains(fake.sentCommands[0], "_data_stream/caltechdata-auditlog-audit-log-v1.0.0") {
		t.Errorf("the stream delete comes first, got: %s", fake.sentCommands[0])
	}
	if !strings.Contains(fake.sentCommands[1], "'localhost:9200/"+auditBackingIndex+"?ignore_unavailable=true'") {
		t.Errorf("the orphan must be deleted by name, tolerating one already gone, got: %s", fake.sentCommands[1])
	}
}

func TestBuildDeleteIndicesCommand_ToleratesAlreadyDeletedIndices(t *testing.T) {
	got := buildDeleteIndicesCommand([]string{"a-1", "b-2"})
	if !strings.Contains(got, "'localhost:9200/a-1,b-2?ignore_unavailable=true'") {
		t.Errorf("got %s", got)
	}
}

func TestDeleteConflictingIndices_DataStreamFailureNamesTheStream(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed,
		stdout: `{"error":{"root_cause":[{"type":"resource_not_found_exception","reason":"no such data stream"}],"type":"resource_not_found_exception","reason":"no such data stream"},"status":404}`}
	err := DeleteConflictingIndices(context.Background(), fake, "i-1", []string{auditBackingIndex}, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "caltechdata-auditlog-audit-log-v1.0.0") {
		t.Errorf("the error should name the data stream it was deleting, got: %v", err)
	}
}

// A restore that matches nothing is accepted by OpenSearch (HTTP 200,
// "indices":[], 0 shards) because ignore_unavailable is true. Found live
// 2026-09-30: the operator typed the S3 source name (caltechauthors-v13)
// as the index prefix, the restore restored nothing, and the run died two
// steps later on a bare 404 from _cat/recovery for an index that never
// existed. It must stop at the empty result and say why.
func TestRestoreOpenSearchSnapshot_StopsWhenTheSnapshotRestoredNothing(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "wrong-prefix\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "wrong-prefix-rdmrecords-a yellow open 1\n")
	ssmClient.responses = append([]ssmCommandResponse{{
		substring: "/_restore",
		stdout:    `{"snapshot":{"snapshot":"rdm-20260819-160031","indices":[],"shards":{"total":0,"failed":0,"successful":0}}}`,
		status:    types.CommandInvocationStatusSuccess,
	}}, ssmClient.responses...)
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}

	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf)
	if err == nil {
		t.Fatal("expected an error: the restore matched no indices")
	}
	for _, want := range []string{"wrong-prefix", "rdm-20260819-160031", "no indices"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if commandSent(ssmClient.sentCommands, "_cat/recovery") {
		t.Errorf("must stop before polling recovery for indices that were never restored, sent: %v", ssmClient.sentCommands)
	}
	if commandSent(ssmClient.sentCommands, deleteSnapshotCmdFragment) {
		t.Errorf("must leave the synced snapshot in place as evidence, sent: %v", ssmClient.sentCommands)
	}
}

func TestOpenSearchErrorReason(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			"write index of a data stream",
			`{"error":{"root_cause":[{"type":"illegal_argument_exception","reason":"index [.ds-x-000001] is the write index for data stream [x] and cannot be deleted"}],"type":"illegal_argument_exception","reason":"index [.ds-x-000001] is the write index for data stream [x] and cannot be deleted"},"status":400}`,
			"illegal_argument_exception: index [.ds-x-000001] is the write index for data stream [x] and cannot be deleted",
		},
		{
			"missing index",
			`{"error":{"root_cause":[{"type":"index_not_found_exception","reason":"no such index [a-b]","index":"a-b"}],"type":"index_not_found_exception","reason":"no such index [a-b]","index":"a-b"},"status":404}`,
			"index_not_found_exception: no such index [a-b]",
		},
		{"not JSON", "curl: (7) Failed to connect", ""},
		{"JSON without an error object", `{"acknowledged":true}`, ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		if got := openSearchErrorReason(tt.stdout); got != tt.want {
			t.Errorf("%s: openSearchErrorReason = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCurlFailureError_LeadsWithTheOpenSearchReasonNotRawJSON(t *testing.T) {
	stdout := `{"error":{"root_cause":[{"type":"index_not_found_exception","reason":"no such index [a-b]","index":"a-b"}],"type":"index_not_found_exception","reason":"no such index [a-b]","index":"a-b"},"status":404}`
	err := curlFailureError("restore recovery check on i-1 failed", types.CommandInvocationStatusFailed, stdout)
	msg := err.Error()
	if !strings.Contains(msg, "no such index [a-b]") {
		t.Errorf("message should carry OpenSearch's reason, got: %s", msg)
	}
	if strings.Contains(msg, "root_cause") {
		t.Errorf("message should not dump the raw JSON envelope, got: %s", msg)
	}
}
