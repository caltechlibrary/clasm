package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// DR-0179 (design brief generate_sql_backup_preflight_and_archive_repair.md,
// Part 2). Found 2026-09-30 in step 5 of the Phase 20.65 real-AWS
// verification: with the repository tree root-owned *below* its top level,
// the ensure step repaired the top level, the search container was refused
// writing indices/..., and Archive failed after a multi-minute snapshot. The
// decision: Archive repairs ownership it finds wrong, but only in a directory
// that looks like a snapshot repository, so a mistyped path is never chowned
// recursively.

const repairDir = "/opt/rdm_opensearch_backups"

func archiveWithProbe(t *testing.T, probeStdout string, probeStatus types.CommandInvocationStatus) (*fakeSSMClient, string, error) {
	t.Helper()
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, buf := newTermOnly()
	// "chown -R" is scripted because this fake has no default status: an
	// unscripted command is never reported complete and the poll never ends.
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: append(
		[]ssmCommandResponse{
			{substring: "clasm-owner-probe", stdout: probeStdout, status: probeStatus},
			{substring: "chown -R", status: types.CommandInvocationStatusSuccess},
		},
		openSearchHappyPathResponses()...)}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, repairDir, "my-os-bucket", "newauthors", "newauthors", 0, false, nil)
	return ssmClient, buf.String(), err
}

func TestArchiveOpenSearch_RepairsARootOwnedTreeBeforeRegistering(t *testing.T) {
	ssmClient, out, err := archiveWithProbe(t, "clasm-owner-probe 7 yes\n", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sent := ssmClient.sentCommands
	lookup := commandIndex(t, sent, "id -u")
	ensure := commandIndex(t, sent, archiveEnsureExact)
	probe := commandIndex(t, sent, "clasm-owner-probe")
	chown := commandIndex(t, sent, "chown -R 1000:1001 '"+repairDir+"'")
	register := commandIndex(t, sent, `"type":"fs"`)
	if !(lookup < ensure && ensure < probe && probe < chown && chown < register) {
		t.Errorf("want lookup < ensure < probe < chown < register, got %d %d %d %d %d; sent: %v", lookup, ensure, probe, chown, register, sent)
	}
	if !strings.Contains(out, "7") || !strings.Contains(out, repairDir) {
		t.Errorf("the repair should be announced with the count and the directory, got output:\n%s", out)
	}
}

func TestArchiveOpenSearch_CleanTreeSendsOnlyTheReadOnlyProbe(t *testing.T) {
	ssmClient, _, err := archiveWithProbe(t, "clasm-owner-probe 0 yes\n", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commandSent(ssmClient.sentCommands, "chown -R") {
		t.Errorf("a correctly owned tree must not be chowned; sent: %v", ssmClient.sentCommands)
	}
	if n := countCommandsContaining(ssmClient.sentCommands, "clasm-owner-probe"); n != 1 {
		t.Errorf("the probe ran %d times, want exactly 1", n)
	}
}

// The guard. A directory that holds entries not owned by the service user but
// does not look like a snapshot repository (no index.latest, no indices/) is
// never chowned recursively: the path was operator-typed.
func TestArchiveOpenSearch_NotARepositoryIsNeverChownedRecursively(t *testing.T) {
	ssmClient, _, err := archiveWithProbe(t, "clasm-owner-probe 5 no\n", types.CommandInvocationStatusSuccess)
	if err == nil {
		t.Fatal("expected a stop: the directory does not look like a snapshot repository")
	}
	for _, want := range []string{repairDir, "5", "snapshot repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	for _, f := range []string{"chown -R", `"type":"fs"`, `"indices"`, "aws s3 sync"} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("a command containing %q was sent after the guard stopped the run; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

// An empty directory has no entries to repair, so it is neither repaired nor refused.
func TestArchiveOpenSearch_EmptyDirectoryIsNeitherRepairedNorRefused(t *testing.T) {
	ssmClient, _, err := archiveWithProbe(t, "clasm-owner-probe 0 no\n", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatalf("an empty directory must not be refused: %v", err)
	}
	if commandSent(ssmClient.sentCommands, "chown -R") {
		t.Errorf("nothing to repair in an empty directory; sent: %v", ssmClient.sentCommands)
	}
}

func TestArchiveOpenSearch_FailedProbeAbortsBeforeAnythingIsChowned(t *testing.T) {
	ssmClient, _, err := archiveWithProbe(t, "find: permission denied", types.CommandInvocationStatusFailed)
	if err == nil {
		t.Fatal("expected the probe failure to propagate")
	}
	for _, f := range []string{"chown -R", `"type":"fs"`} {
		if commandSent(ssmClient.sentCommands, f) {
			t.Errorf("a command containing %q was sent after a failed probe; sent: %v", f, ssmClient.sentCommands)
		}
	}
}

func TestArchiveOpenSearch_FailedRepairAbortsBeforeRegistering(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: append([]ssmCommandResponse{
		{substring: "clasm-owner-probe", stdout: "clasm-owner-probe 3 yes\n", status: types.CommandInvocationStatusSuccess},
		{substring: "chown -R", stdout: "chown: Operation not permitted", status: types.CommandInvocationStatusFailed},
	}, openSearchHappyPathResponses()...)}
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, repairDir, "my-os-bucket", "newauthors", "newauthors", 0, false, nil)
	if err == nil || !strings.Contains(err.Error(), "Operation not permitted") {
		t.Fatalf("expected the repair failure with its cause, got: %v", err)
	}
	if commandSent(ssmClient.sentCommands, `"type":"fs"`) {
		t.Errorf("the repository was registered after a failed repair; sent: %v", ssmClient.sentCommands)
	}
}

// A uid mismatch stops the run first, before the probe: nothing is read or
// written on an instance the workflow refuses to touch.
func TestArchiveOpenSearch_UIDMismatchStopsBeforeTheProbe(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Region: "us-east-1"}
	term, _ := newTermOnly()
	ssmClient := &fakeSSMClient{commandID: "cmd-1", responses: openSearchHappyPathResponses()}
	setArchiveOwnerStdout(ssmClient, "1001 1001\n")
	s3Client := &echoingS3Client{fakeS3Client: &fakeS3Client{}}
	if err := runArchiveOpenSearchSnapshot(context.Background(), term, ssmClient, s3Client, inst, repairDir, "my-os-bucket", "newauthors", "newauthors", 0, false, nil); err == nil {
		t.Fatal("expected a uid-mismatch stop")
	}
	if commandSent(ssmClient.sentCommands, "clasm-owner-probe") {
		t.Errorf("the probe ran after a uid mismatch; sent: %v", ssmClient.sentCommands)
	}
}

// The probe only reads: nothing in it may change ownership, mode or content.
func TestBuildOwnershipProbeCommand_IsReadOnlyAndQuotesTheDirectory(t *testing.T) {
	got := buildOwnershipProbeCommand("/opt/dir with space", ServiceOwner{UID: 1000, GID: 1001})
	for _, bad := range []string{"chown", "chmod", "rm ", "-delete", "-exec", ">", "mv "} {
		if strings.Contains(got, bad) {
			t.Errorf("the probe must be read-only but contains %q: %s", bad, got)
		}
	}
	for _, want := range []string{"-mindepth 1", "! -user 1000", "index.latest", "indices", "'/opt/dir with space'"} {
		if !strings.Contains(got, want) {
			t.Errorf("the probe should contain %q: %s", want, got)
		}
	}
}

func TestParseOwnershipProbe(t *testing.T) {
	tests := []struct {
		in         string
		n          int
		repo, know bool
	}{
		{"clasm-owner-probe 7 yes\n", 7, true, true},
		{"noise\nclasm-owner-probe 0 no\n", 0, false, true},
		{"clasm-owner-probe 12 no", 12, false, true},
		{"", 0, false, false},
		{"clasm-owner-probe x yes", 0, false, false},
		{"clasm-owner-probe 3 maybe", 0, false, false},
	}
	for _, tt := range tests {
		n, repo, known := parseOwnershipProbe(tt.in)
		if n != tt.n || repo != tt.repo || known != tt.know {
			t.Errorf("parseOwnershipProbe(%q) = %d, %v, %v; want %d, %v, %v", tt.in, n, repo, known, tt.n, tt.repo, tt.know)
		}
	}
}

// An unreadable probe must not block the archive (the repair is a courtesy,
// the archive's own failure modes still report themselves) and must say so.
func TestArchiveOpenSearch_UnreadableProbeWarnsAndProceeds(t *testing.T) {
	ssmClient, out, err := archiveWithProbe(t, "", types.CommandInvocationStatusSuccess)
	if err != nil {
		t.Fatalf("an unreadable probe must not stop the archive: %v", err)
	}
	if commandSent(ssmClient.sentCommands, "chown -R") {
		t.Errorf("nothing may be chowned on an unreadable probe; sent: %v", ssmClient.sentCommands)
	}
	if !strings.Contains(out, "could not") {
		t.Errorf("the skipped check should be reported, got output:\n%s", out)
	}
}

// The probe command itself, run under a real sh and find against a real
// directory, so the fake is not the only thing that has ever executed it.
func TestOwnershipProbeCommand_RunsAgainstARealDirectory(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	probe := func(dir string, uid int) (int, bool) {
		out, err := exec.Command("sh", "-c", buildOwnershipProbeCommand(dir, ServiceOwner{UID: uid, GID: 1001})).Output()
		if err != nil {
			t.Fatalf("probe failed: %v", err)
		}
		n, repo, known := parseOwnershipProbe(string(out))
		if !known {
			t.Fatalf("could not parse the probe's output %q", out)
		}
		return n, repo
	}
	repo := filepath.Join(t.TempDir(), "with space") // a space: the quoting must hold
	if err := os.MkdirAll(filepath.Join(repo, "indices", "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "index.latest"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	if n, isRepo := probe(repo, me); n != 0 || !isRepo {
		t.Errorf("entries owned by the service user: got %d, repo=%v; want 0, true", n, isRepo)
	}
	// Three entries below the top level (indices, indices/abc, index.latest), none owned by uid+1.
	if n, isRepo := probe(repo, me+1); n != 3 || !isRepo {
		t.Errorf("entries owned by someone else: got %d, repo=%v; want 3, true", n, isRepo)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, isRepo := probe(other, me+1); n != 1 || isRepo {
		t.Errorf("a directory that is not a repository: got %d, repo=%v; want 1, false", n, isRepo)
	}
	empty := t.TempDir()
	if n, isRepo := probe(empty, me+1); n != 0 || isRepo {
		t.Errorf("an empty directory: got %d, repo=%v; want 0, false", n, isRepo)
	}
}
