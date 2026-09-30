package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Restore OpenSearch form (DR-0177, DR-0180), the first
// destructive leaf built on the shared gate. Arguments, in the order of the
// interactive prompts:
//
//	restore-opensearch-snapshot-from-s3 [--confirm <instance-id-or-name>]
//	    <instance> <directory> <bucket> <source> <snapshot-or-latest> <index-prefix>
//
// A dry run by default: it reads (owner, existing indices, the S3 listing) and
// prints a plan, and changes nothing unless --confirm names the target or a
// terminal answers the prompt.

var restoreCLIInst = inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}

func restoreCLIParams() RestoreOpenSearchParams {
	return RestoreOpenSearchParams{
		Directory:   "/opt/rdm_opensearch_backups",
		Bucket:      "my-bucket",
		Source:      "caltechdata",
		Snapshot:    "rdm-20260819-160031",
		IndexPrefix: "caltechdata",
	}
}

func snapshotObjects(sourceName string, size int64, snapshotNames ...string) []s3types.Object {
	var out []s3types.Object
	for _, n := range snapshotNames {
		out = append(out, s3types.Object{
			Key:          aws.String(sourceName + "/opensearch-snapshots/" + n + "/index-0"),
			Size:         aws.Int64(size),
			LastModified: aws.Time(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)),
		})
	}
	return out
}

func runRestoreCLI(t *testing.T, existing string, objects []s3types.Object, p RestoreOpenSearchParams, confirm string, interactive bool, typed string) (*fakeSSMClient, *fakeS3Client, string, error) {
	t.Helper()
	ssmClient := restoreOpenSearchFake(existing, "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	s3Client := &fakeS3Client{allObjects: objects}
	var w io.Writer
	var in io.Reader
	var buf *bytes.Buffer
	if interactive {
		w, in, buf = newPipeEditor(typed)
	} else {
		buf = &bytes.Buffer{}
		w, in = buf, failingReader{t}
	}
	err := RunRestoreOpenSearchSnapshotAuto(context.Background(), w, ssmClient, s3Client, restoreCLIInst, p, confirm, interactive, in, buf)
	return ssmClient, s3Client, buf.String(), err
}

// --- argument parsing ---

func TestParseOpenSearchRestoreArgs_AllSixArgumentsAndConfirm(t *testing.T) {
	insts := []inventory.Instance{restoreCLIInst}
	inst, p, opts, err := ParseOpenSearchRestoreArgs([]string{"--confirm", "i-1", "caltechdata", "/opt/d", "my-bucket", "src", "latest", "pfx"}, insts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.InstanceID != "i-1" || opts.Confirm != "i-1" {
		t.Errorf("got instance %q confirm %q", inst.InstanceID, opts.Confirm)
	}
	want := RestoreOpenSearchParams{Directory: "/opt/d", Bucket: "my-bucket", Source: "src", Snapshot: "latest", IndexPrefix: "pfx"}
	if p != want {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestParseOpenSearchRestoreArgs_WrongArityIsAUsageErrorNamingTheLeaf(t *testing.T) {
	for _, args := range [][]string{nil, {"caltechdata"}, {"caltechdata", "/d", "b", "s", "latest"}, {"caltechdata", "/d", "b", "s", "latest", "p", "extra"}} {
		_, _, _, err := ParseOpenSearchRestoreArgs(args, []inventory.Instance{restoreCLIInst})
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: want a *UsageError, got: %v", args, err)
			continue
		}
		if !strings.Contains(err.Error(), RestoreOpenSearchSnapshotCLISlug) || !strings.Contains(err.Error(), "--confirm") {
			t.Errorf("%v: the error should carry the leaf name and usage, got: %v", args, err)
		}
	}
}

func TestParseOpenSearchRestoreArgs_UnknownInstanceAndEmptyArgsAreUsageErrors(t *testing.T) {
	insts := []inventory.Instance{restoreCLIInst}
	cases := map[string][]string{
		"unknown instance": {"nope", "/d", "b", "s", "latest", "p"},
		"empty directory":  {"caltechdata", "", "b", "s", "latest", "p"},
		"empty bucket":     {"caltechdata", "/d", "", "s", "latest", "p"},
		"empty source":     {"caltechdata", "/d", "b", "", "latest", "p"},
		"empty snapshot":   {"caltechdata", "/d", "b", "s", "", "p"},
		"empty prefix":     {"caltechdata", "/d", "b", "s", "latest", ""},
	}
	for name, args := range cases {
		_, _, _, err := ParseOpenSearchRestoreArgs(args, insts)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: want a *UsageError, got: %v", name, err)
		}
	}
}

func TestParseOpenSearchRestoreArgs_HelpAndUnknownOption(t *testing.T) {
	insts := []inventory.Instance{restoreCLIInst}
	_, _, _, err := ParseOpenSearchRestoreArgs([]string{"--help"}, insts)
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, RestoreOpenSearchSnapshotCLISlug) {
		t.Errorf("--help: want a *HelpRequested carrying the usage, got: %v", err)
	}
	_, _, _, err = ParseOpenSearchRestoreArgs([]string{"--force", "caltechdata", "/d", "b", "s", "latest", "p"}, insts)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("--force: want a *UsageError, got: %v", err)
	}
}

// --- the dry run: read-only, prints a real plan ---

func TestRestoreCLI_DryRunChangesNothingAndPrintsARealPlan(t *testing.T) {
	ssmClient, _, out, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\ncaltechdata-users-b\n",
		snapshotObjects("caltechdata", 2048, "rdm-20260819-160031"), restoreCLIParams(), "", false, "")
	if err != nil {
		t.Fatalf("a dry run is not an error: %v", err)
	}
	for _, want := range []string{
		"rdm-20260819-160031", "s3://my-bucket/caltechdata/opensearch-snapshots/",
		"delete 2 existing indices", "caltechdata-rdmrecords-a", "caltechdata-users-b",
		"/opt/rdm_opensearch_backups", "2.0 KiB",
		"Nothing was changed", "--confirm caltechdata",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan should mention %q, got:\n%s", want, out)
		}
	}
	// Only read-only commands: the owner lookup and the index listing.
	for _, c := range ssmClient.sentCommands {
		if !strings.Contains(c, "id -u") && !strings.Contains(c, "_cat/indices/") {
			t.Errorf("a dry run sent a command that is not a read: %q", c)
		}
	}
}

func TestRestoreCLI_DryRunWithNoExistingIndicesSaysThereIsNothingToDelete(t *testing.T) {
	_, _, out, err := runRestoreCLI(t, "", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no existing indices") {
		t.Errorf("the plan should say nothing will be deleted, got:\n%s", out)
	}
}

func TestRestoreCLI_PlanTruncatesALongListOfExistingIndices(t *testing.T) {
	var names []string
	for i := 0; i < 7; i++ {
		names = append(names, fmt.Sprintf("caltechdata-rdmrecords-%d", i))
	}
	_, _, out, err := runRestoreCLI(t, strings.Join(names, "\n")+"\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "delete 7 existing indices") || !strings.Contains(out, "and 2 more") {
		t.Errorf("want the count and a truncated list, got:\n%s", out)
	}
	if strings.Contains(out, "caltechdata-rdmrecords-6") {
		t.Errorf("the seventh name should be summarised, not listed:\n%s", out)
	}
}

// --- --confirm ---

func TestRestoreCLI_ConfirmRunsTheWholeRestoreInTheDesignedOrder(t *testing.T) {
	ssmClient, _, out, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "caltechdata", false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	lookup := commandIndex(t, sent, "id -u")
	ensure := commandIndex(t, sent, openSearchEnsureExact)
	sync := commandIndex(t, sent, syncCmdFragment)
	chown := commandIndex(t, sent, chownCmdFragment)
	register := commandIndex(t, sent, registerRepoCmdFragment)
	var del int = -1
	for i, c := range sent {
		if isDeleteIndicesCommand(c) {
			del = i
			break
		}
	}
	restore := commandIndex(t, sent, "/_restore")
	if del < 0 || !(lookup < ensure && ensure < sync && sync < chown && chown < register && register < del && del < restore) {
		t.Errorf("want lookup < ensure < sync < chown < register < delete < restore, got %d %d %d %d %d %d %d; sent: %v", lookup, ensure, sync, chown, register, del, restore, sent)
	}
	if !strings.Contains(out, "Confirmed (--confirm caltechdata)") || !strings.Contains(out, "Restored OpenSearch snapshot") {
		t.Errorf("want the one-line confirmation and the result table, got:\n%s", out)
	}
}

func TestRestoreCLI_ConfirmAcceptsTheInstanceID(t *testing.T) {
	_, _, _, err := runRestoreCLI(t, "", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "i-1", false, "")
	if err != nil {
		t.Fatalf("--confirm with the instance ID must run: %v", err)
	}
}

// A mismatch fails before any AWS call, not after the reads that build the plan.
func TestRestoreCLI_ConfirmMismatchIsAUsageErrorBeforeAnyCall(t *testing.T) {
	ssmClient, s3Client, out, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "someone-else", false, "")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a *UsageError, got: %v", err)
	}
	if len(ssmClient.sentCommands) != 0 || len(s3Client.listObjectsV2Calls) != 0 {
		t.Errorf("no call may precede the mismatch: ssm %v, s3 %d", ssmClient.sentCommands, len(s3Client.listObjectsV2Calls))
	}
	if out != "" {
		t.Errorf("nothing may be printed, got:\n%s", out)
	}
}

// --- the terminal branch ---

func TestRestoreCLI_TerminalPromptsAndProceedsOnTheExactName(t *testing.T) {
	ssmClient, _, out, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "", true, "caltechdata\n")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !commandSent(ssmClient.sentCommands, "/_restore") {
		t.Errorf("the restore did not run after the right answer; sent: %v", ssmClient.sentCommands)
	}
	if strings.Index(out, "delete 1 existing index") < 0 {
		t.Errorf("the plan should be printed before the prompt, got:\n%s", out)
	}
}

func TestRestoreCLI_TerminalWrongAnswerChangesNothing(t *testing.T) {
	ssmClient, _, out, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031"), restoreCLIParams(), "", true, "wrong\n")
	if err != nil {
		t.Fatalf("declining is not an error: %v", err)
	}
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("got:\n%s", out)
	}
	for _, f := range []string{"install -d", syncCmdFragment, "chown -R", registerRepoCmdFragment, "/_restore"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q was sent after a declined prompt", f)
		}
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Error("indices were deleted after a declined prompt")
	}
}

// --- snapshot selection ---

func TestRestoreCLI_LatestPicksTheMostRecentSnapshot(t *testing.T) {
	p := restoreCLIParams()
	p.Snapshot = "latest"
	ssmClient, _, _, err := runRestoreCLI(t, "", snapshotObjects("caltechdata", 10, "rdm-20260101-000000", "rdm-20260819-160031", "rdm-20260301-000000"), p, "caltechdata", false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !commandSent(ssmClient.sentCommands, "rdm-20260819-160031") || commandSent(ssmClient.sentCommands, "rdm-20260101-000000") {
		t.Errorf("latest must resolve to the newest snapshot; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreCLI_UnknownSnapshotNameListsWhatExistsAndChangesNothing(t *testing.T) {
	p := restoreCLIParams()
	p.Snapshot = "rdm-19990101-000000"
	ssmClient, _, _, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("caltechdata", 10, "rdm-20260819-160031", "rdm-20260101-000000"), p, "caltechdata", false, "")
	if err == nil {
		t.Fatal("expected an error for an unknown snapshot")
	}
	for _, want := range []string{"rdm-19990101-000000", "rdm-20260819-160031", "rdm-20260101-000000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	if CLIExitCode(err) != 1 {
		t.Errorf("an unknown snapshot is an action failure (exit 1), got %d", CLIExitCode(err))
	}
	if commandSent(ssmClient.sentCommands, syncCmdFragment) || deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Errorf("nothing may change for an unknown snapshot; sent: %v", ssmClient.sentCommands)
	}
}

func TestRestoreCLI_EmptySourceIsTheSameErrorAsTheInteractiveForm(t *testing.T) {
	ssmClient, _, _, err := runRestoreCLI(t, "caltechdata-rdmrecords-a\n", snapshotObjects("other-box", 10, "rdm-20260819-160031"), restoreCLIParams(), "caltechdata", false, "")
	if err == nil || !strings.Contains(err.Error(), "no OpenSearch snapshots found") || !strings.Contains(err.Error(), "other-box") {
		t.Fatalf("want the no-snapshots error naming the sources that do hold some, got: %v", err)
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Error("indices were deleted for an empty source")
	}
}

// --- safety checks the interactive form makes, made here too ---

func TestRestoreCLI_UIDMismatchStopsBeforeAnythingElse(t *testing.T) {
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "x yellow open 1\n")
	setRestoreOwnerStdout(ssmClient, "1001 1001\n")
	s3Client := &fakeS3Client{allObjects: snapshotObjects("caltechdata", 10, "rdm-20260819-160031")}
	var out bytes.Buffer
	err := RunRestoreOpenSearchSnapshotAuto(context.Background(), &out, ssmClient, s3Client, restoreCLIInst, restoreCLIParams(), "caltechdata", false, failingReader{t}, &out)
	if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("want an error naming both uids, got: %v", err)
	}
	if len(s3Client.listObjectsV2Calls) != 0 || commandSent(ssmClient.sentCommands, "_cat/indices/") {
		t.Errorf("nothing may follow the uid mismatch; ssm %v, s3 %d", ssmClient.sentCommands, len(s3Client.listObjectsV2Calls))
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 2048: "2.0 KiB", 1536: "1.5 KiB", 1 << 20: "1.0 MiB", 6898648135: "6.4 GiB", 1 << 40: "1.0 TiB"}
	for n, want := range tests {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
