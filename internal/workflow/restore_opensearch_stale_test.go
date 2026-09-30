package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Found live 2026-09-30 on caltechauthors-test-v13: Restore synced a snapshot
// into a repository directory that held leftovers from earlier runs. Every
// Archive and Restore leaves its metadata behind (index-N, index.latest) with N
// one higher each time, and OpenSearch uses the highest index-N it finds, not
// the one index.latest names -- so a leftover index-31 (an empty repository)
// shadowed the synced snapshot's own index-24 and the repository reported no
// snapshots. The guard stopped the run before anything was deleted. The fix:
// before the sync, deregister any rdm_backup_repo and move the generation files
// already in the directory aside, to a directory OUTSIDE the repository. A move,
// never a delete, so it is reversible, and the next Archive's sync does not
// upload it.

func TestBuildMoveStaleGenerationsCommand_MovesNeverDeletesAndQuotes(t *testing.T) {
	got := buildMoveStaleGenerationsCommand("/opt/dir with space", "/var/tmp/clasm-stale-repo-20260930T120000")
	for _, bad := range []string{"rm ", "rm-", "-delete", "unlink", "shred", ">"} {
		if strings.Contains(got, bad) {
			t.Errorf("the command must only move, but contains %q: %s", bad, got)
		}
	}
	for _, want := range []string{"mv ", "index-*", "index.latest", "'/opt/dir with space'", "/var/tmp/clasm-stale-repo-20260930T120000", "clasm-stale-moved"} {
		if !strings.Contains(got, want) {
			t.Errorf("the command should contain %q: %s", want, got)
		}
	}
}

// The real command, under a real sh, against a real directory.
func TestBuildMoveStaleGenerationsCommand_RunsAgainstARealDirectory(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	run := func(dir, stale string) (int, string) {
		out, err := exec.Command("sh", "-c", buildMoveStaleGenerationsCommand(dir, stale)).Output()
		if err != nil {
			t.Fatalf("command failed: %v", err)
		}
		n, where, ok := parseStaleMove(string(out))
		if !ok {
			t.Fatalf("could not parse %q", out)
		}
		return n, where
	}
	repo := filepath.Join(t.TempDir(), "with space")
	for _, d := range []string{"indices/abc", "snapshot_shard_paths"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"index-24", "index-31", "index.latest", "snap-x.dat", "meta-x.dat", "indices/abc/0"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(t.TempDir(), "stale")
	n, where := run(repo, stale)
	if n != 3 || where != stale {
		t.Errorf("got %d files moved to %q, want 3 to %q", n, where, stale)
	}
	for _, f := range []string{"index-24", "index-31", "index.latest"} {
		if _, err := os.Stat(filepath.Join(repo, f)); !os.IsNotExist(err) {
			t.Errorf("%s should have been moved out of the repository", f)
		}
		if b, err := os.ReadFile(filepath.Join(stale, f)); err != nil || string(b) != f {
			t.Errorf("%s should be intact in the stale directory: %v %q", f, err, b)
		}
	}
	for _, f := range []string{"snap-x.dat", "meta-x.dat", "indices/abc/0"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("%s must be left alone: %v", f, err)
		}
	}
	// Nothing left to move: reports zero and creates no stale directory.
	stale2 := filepath.Join(t.TempDir(), "stale2")
	if n, _ := run(repo, stale2); n != 0 {
		t.Errorf("a second run moved %d files, want 0", n)
	}
	if _, err := os.Stat(stale2); !os.IsNotExist(err) {
		t.Error("the stale directory must not be created when there is nothing to move")
	}
}

func TestParseStaleMove(t *testing.T) {
	tests := []struct {
		in    string
		n     int
		where string
		ok    bool
	}{
		{"clasm-stale-moved 3 /var/tmp/x\n", 3, "/var/tmp/x", true},
		{"noise\nclasm-stale-moved 0\n", 0, "", true},
		{"clasm-stale-moved x /p", 0, "", false},
		{"", 0, "", false},
	}
	for _, tt := range tests {
		n, where, ok := parseStaleMove(tt.in)
		if n != tt.n || where != tt.where || ok != tt.ok {
			t.Errorf("parseStaleMove(%q) = %d, %q, %v; want %d, %q, %v", tt.in, n, where, ok, tt.n, tt.where, tt.ok)
		}
	}
}

func TestDeregisterSnapshotRepoIfPresent(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		status  types.CommandInvocationStatus
		wantErr bool
	}{
		{"registered", "200", types.CommandInvocationStatusSuccess, false},
		{"not registered", "404", types.CommandInvocationStatusSuccess, false},
		{"server error", "500", types.CommandInvocationStatusSuccess, true},
		{"no answer", "", types.CommandInvocationStatusSuccess, true},
		{"curl failed", "", types.CommandInvocationStatusFailed, true},
	}
	for _, c := range cases {
		fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: c.status, stdout: c.stdout}
		err := DeregisterSnapshotRepoIfPresent(context.Background(), fake, "i-1", "rdm_backup_repo", time.Second, testPollInterval)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if len(fake.sentCommands) == 1 && (!strings.Contains(fake.sentCommands[0], "-X DELETE") || !strings.Contains(fake.sentCommands[0], "_snapshot/rdm_backup_repo")) {
			t.Errorf("%s: sent %q", c.name, fake.sentCommands[0])
		}
	}
}

// --- in the workflow ---

var staleDirRE = regexp.MustCompile(`/var/tmp/clasm-stale-repo-\d{8}T\d{6}`)

func runConfirmedRestoreWithStaleFiles(t *testing.T, moveStdout string, moveStatus types.CommandInvocationStatus) (*fakeSSMClient, string, error) {
	t.Helper()
	ssmClient := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	ssmClient.responses = append([]ssmCommandResponse{{substring: "clasm-stale-moved", stdout: moveStdout, status: moveStatus}}, ssmClient.responses...)
	s3Client := &fakeS3Client{allObjects: snapshotObjects("caltechdata", 10, "rdm-20260819-160031")}
	var out strings.Builder
	err := RunRestoreOpenSearchSnapshotAuto(context.Background(), &out, ssmClient, s3Client, restoreCLIInst, restoreCLIParams(), "caltechdata", false, failingReader{t}, &out)
	return ssmClient, out.String(), err
}

func TestRestore_DeregistersThenMovesStaleGenerationsBeforeTheSync(t *testing.T) {
	ssmClient, out, err := runConfirmedRestoreWithStaleFiles(t, "clasm-stale-moved 3 /var/tmp/clasm-stale-repo-20260930T120000\n", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	ensure := commandIndex(t, sent, openSearchEnsureExact)
	dereg := commandIndex(t, sent, "%{http_code}")
	move := commandIndex(t, sent, "clasm-stale-moved")
	sync := commandIndex(t, sent, syncCmdFragment)
	register := commandIndex(t, sent, registerRepoCmdFragment)
	if !(ensure < dereg && dereg < move && move < sync && sync < register) {
		t.Errorf("want ensure < deregister < move-aside < sync < register, got %d %d %d %d %d; sent: %v", ensure, dereg, move, sync, register, sent)
	}
	if !strings.Contains(out, "3") || !strings.Contains(out, "/var/tmp/clasm-stale-repo-20260930T120000") {
		t.Errorf("the operator should be told how many files moved and where, got:\n%s", out)
	}
}

func TestRestore_SaysNothingAboutStaleFilesWhenThereAreNone(t *testing.T) {
	_, out, err := runConfirmedRestoreWithStaleFiles(t, "clasm-stale-moved 0\n", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "stale") {
		t.Errorf("nothing was moved, so nothing should be reported:\n%s", out)
	}
}

func TestRestore_FailedMoveAbortsBeforeTheSync(t *testing.T) {
	ssmClient, _, err := runConfirmedRestoreWithStaleFiles(t, "mv: cannot move: Permission denied", types.CommandInvocationStatusFailed)
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("want the move failure with its cause, got: %v", err)
	}
	for _, f := range []string{syncCmdFragment, "chown -R", registerRepoCmdFragment, "/_restore"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("%q was sent after a failed move", f)
		}
	}
	if deleteIndicesCommandSent(ssmClient.sentCommands) {
		t.Error("indices were deleted after a failed move")
	}
}

// The interactive form shares the execution steps, so it gets the fix too.
func TestRestoreInteractive_AlsoMovesStaleGenerationsBeforeTheSync(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	input := "\n" + "/opt/rdm_opensearch_backups\n" + "my-bucket\n" + "caltechdata\n" + "\n"
	term, le, buf := newPipeEditor(input)
	ssmClient := restoreOpenSearchFake("", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3Client := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	if err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, s3Client, sameS3Client(s3Client), inst, nil, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commandIndex(t, ssmClient.sentCommands, "clasm-stale-moved") > commandIndex(t, ssmClient.sentCommands, syncCmdFragment) {
		t.Errorf("the move-aside must precede the sync; sent: %v", ssmClient.sentCommands)
	}
}

func TestStaleDirNameIsOutsideTheRepositoryAndTimestamped(t *testing.T) {
	if !staleDirRE.MatchString("/var/tmp/clasm-stale-repo-20260930T120000") {
		t.Error("the pattern used by these tests does not match its own example")
	}
}
