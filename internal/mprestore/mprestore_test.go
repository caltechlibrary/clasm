package mprestore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// recorder is an Executor that records the order of Put and Run calls.
type recorder struct {
	events    []string // "put <remote>" and "run <command>"
	timeouts  []time.Duration
	putErr    error
	runStdout string
	runStatus executor.CommandStatus
	runErr    error
}

func (r *recorder) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, executor.CommandStatus, error) {
	r.events = append(r.events, "run "+command)
	r.timeouts = append(r.timeouts, timeout)
	st := r.runStatus
	if st == "" {
		st = executor.StatusSuccess
	}
	return r.runStdout, st, r.runErr
}
func (r *recorder) Put(ctx context.Context, local, remote string) error {
	r.events = append(r.events, "put "+remote)
	return r.putErr
}
func (r *recorder) Get(ctx context.Context, remote, local string) error { return nil }
func (r *recorder) Name() string                                        { return "vm-1" }

func localFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- SQL ----

func TestSQLAcquirer_AGzipDumpIsPutThenDecompressedAndTheSQLPathReturned(t *testing.T) {
	r := &recorder{}
	path, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql.gz"), Key: "src/d.sql.gz"})(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if path != workflow.RestoreSQLPath {
		t.Errorf("path = %q, want %q", path, workflow.RestoreSQLPath)
	}
	if len(r.events) != 2 || r.events[0] != "put "+workflow.RestoreDownloadPath || r.events[1] != "run "+workflow.DecompressRestoreDownloadCommand() {
		t.Errorf("events = %q", r.events)
	}
}

func TestSQLAcquirer_APlainDumpIsPutAndNeedsNoDecompression(t *testing.T) {
	r := &recorder{}
	path, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql"), Key: "src/d.sql"})(context.Background(), r)
	if err != nil || path != workflow.RestoreDownloadPath {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if len(r.events) != 1 || r.events[0] != "put "+workflow.RestoreDownloadPath {
		t.Errorf("events = %q", r.events)
	}
}

func TestSQLAcquirer_APutFailureStopsBeforeDecompressing(t *testing.T) {
	boom := errors.New("transfer failed")
	r := &recorder{putErr: boom}
	_, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql.gz"), Key: "src/d.sql.gz"})(context.Background(), r)
	if !errors.Is(err, boom) || len(r.events) != 1 {
		t.Errorf("err=%v events=%q", err, r.events)
	}
}

func TestSQLAcquirer_AFailedDecompressionIsAnErrorWithItsOutput(t *testing.T) {
	r := &recorder{runStatus: executor.StatusFailed, runStdout: "gunzip: unexpected end of file"}
	_, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql.gz"), Key: "src/d.sql.gz"})(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "unexpected end of file") || !strings.Contains(err.Error(), "vm-1") {
		t.Errorf("err = %v", err)
	}
}

func TestSQLAcquirer_AMissingLocalFileIsRefusedBeforeAnythingIsSent(t *testing.T) {
	r := &recorder{}
	_, err := SQLAcquirer(rdmfetch.DumpReport{Path: "/no/such/dump.sql.gz", Key: "src/d.sql.gz"})(context.Background(), r)
	if err == nil || len(r.events) != 0 {
		t.Errorf("err=%v events=%q", err, r.events)
	}
}

func TestSQLAcquirer_ADecompressionErrorFromTheExecutorIsReturned(t *testing.T) {
	boom := errors.New("vm unreachable")
	r := &recorder{runErr: boom}
	_, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql.gz"), Key: "src/d.sql.gz"})(context.Background(), r)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

// ---- snapshot ----

func TestSnapshotAcquirer_PutsTheTarThenUnpacksItIntoTheDirectoryAndChecksTheCount(t *testing.T) {
	r := &recorder{runStdout: "clasm-extracted 7\n"}
	err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 7}, "/var/tmp/x.tar")(context.Background(), r, "/opt/rdm_opensearch_backups")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.events) != 2 || r.events[0] != "put /var/tmp/x.tar" || !strings.HasPrefix(r.events[1], "run ") {
		t.Fatalf("events = %q", r.events)
	}
	for _, want := range []string{"'/opt/rdm_opensearch_backups'", "'/var/tmp/x.tar'", "tar -xf", "rm -f"} {
		if !strings.Contains(r.events[1], want) {
			t.Errorf("the script is missing %q:\n%s", want, r.events[1])
		}
	}
}

func TestSnapshotAcquirer_ACountThatDiffersFromTheExpectedObjectsIsAnError(t *testing.T) {
	r := &recorder{runStdout: "clasm-extracted 6\n"}
	err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 7}, "/var/tmp/x.tar")(context.Background(), r, "/d")
	if err == nil || !strings.Contains(err.Error(), "6") || !strings.Contains(err.Error(), "7") {
		t.Errorf("err = %v", err)
	}
}

func TestSnapshotAcquirer_NoCountInTheOutputIsAnError(t *testing.T) {
	r := &recorder{runStdout: "something else\n"}
	if err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 7}, "/var/tmp/x.tar")(context.Background(), r, "/d"); err == nil {
		t.Error("want an error when the script did not report a count")
	}
}

func TestSnapshotAcquirer_FailuresAreErrorsAndAPutFailureRunsNothing(t *testing.T) {
	boom := errors.New("transfer failed")
	r := &recorder{putErr: boom}
	if err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 1}, "/var/tmp/x.tar")(context.Background(), r, "/d"); !errors.Is(err, boom) || len(r.events) != 1 {
		t.Errorf("put failure: err=%v events=%q", err, r.events)
	}
	r = &recorder{runStatus: executor.StatusFailed, runStdout: "tar: Unexpected EOF"}
	if err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 1}, "/var/tmp/x.tar")(context.Background(), r, "/d"); err == nil || !strings.Contains(err.Error(), "Unexpected EOF") || !strings.Contains(err.Error(), "vm-1") || !strings.Contains(err.Error(), "failed") {
		t.Errorf("unpack failure: err=%v", err)
	}
}

func TestSnapshotAcquirer_AMissingTarIsRefusedBeforeAnythingIsSent(t *testing.T) {
	r := &recorder{}
	if err := snapshotAcquirer(Snapshot{TarPath: "/no/such.tar", Objects: 1}, "/var/tmp/x.tar")(context.Background(), r, "/d"); err == nil || len(r.events) != 0 {
		t.Errorf("err=%v events=%q", err, r.events)
	}
}

// localExecutor runs commands with the local bash and copies files, so the real
// unpack script is run for real against a real tar.
type localExecutor struct{}

func (localExecutor) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, executor.CommandStatus, error) {
	out, err := exec.CommandContext(ctx, "bash", "-c", command).CombinedOutput()
	if err != nil {
		return string(out), executor.StatusFailed, nil
	}
	return string(out), executor.StatusSuccess, nil
}
func (localExecutor) Put(ctx context.Context, local, remote string) error {
	b, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	return os.WriteFile(remote, b, 0o600)
}
func (localExecutor) Get(ctx context.Context, remote, local string) error { return nil }
func (localExecutor) Name() string                                        { return "local" }

func TestSnapshotAcquirer_UnpacksARealRepositoryTarAndRemovesTheScratchTar(t *testing.T) {
	src := t.TempDir()
	files := map[string]string{"index-5": "g", "snap-a.dat": "s", "indices/aaa/0/d.dat": "d0", "indices/bbb/0/d.dat": "d1"}
	for rel, c := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tarPath := filepath.Join(t.TempDir(), "repo.tar")
	if _, err := rdmfetch.PackSnapshot(src, tarPath); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "repo with space's dir")
	scratch := filepath.Join(t.TempDir(), "scratch dir's.tar")
	if err := snapshotAcquirer(Snapshot{TarPath: tarPath, Objects: len(files)}, scratch)(context.Background(), localExecutor{}, dest); err != nil {
		t.Fatal(err)
	}
	for rel, want := range files {
		if b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel))); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", rel, b, err)
		}
	}
	if _, err := os.Stat(scratch); err == nil {
		t.Error("the scratch tar must be removed after unpacking")
	}
	// A count that does not match is caught by the real script's own output.
	if err := snapshotAcquirer(Snapshot{TarPath: tarPath, Objects: len(files) + 1}, scratch)(context.Background(), localExecutor{}, dest); err == nil {
		t.Error("a wrong expected count must be an error")
	}
}

// A dump or a snapshot of this size takes minutes to unpack; the executor runs a
// step with a long timeout detached, so the timeout must be a long one.
func TestAcquirers_GiveTheVMLongStepsALongTimeout(t *testing.T) {
	r := &recorder{runStdout: "clasm-extracted 1\n"}
	if err := snapshotAcquirer(Snapshot{TarPath: localFile(t, "r.tar"), Objects: 1}, "/var/tmp/x.tar")(context.Background(), r, "/d"); err != nil {
		t.Fatal(err)
	}
	if _, err := SQLAcquirer(rdmfetch.DumpReport{Path: localFile(t, "d.sql.gz"), Key: "k.sql.gz"})(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if len(r.timeouts) != 2 || r.timeouts[0] < 10*time.Minute || r.timeouts[1] < 10*time.Minute {
		t.Errorf("timeouts = %v", r.timeouts)
	}
}
