package mprestore

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
)

// TestLive_AcquirersOnARealVM sends a tiny synthetic dump and snapshot to a
// real Multipass VM. It uses no production data. It is skipped unless
// CLASM_LIVE_MULTIPASS_VM names a running throwaway VM; it writes the restore
// scratch files and a directory under /var/tmp on it.
func TestLive_AcquirersOnARealVM(t *testing.T) {
	vm := os.Getenv("CLASM_LIVE_MULTIPASS_VM")
	if vm == "" {
		t.Skip("set CLASM_LIVE_MULTIPASS_VM to a throwaway VM to run this")
	}
	ctx := context.Background()
	ex := executor.NewMultipassExecutor(vm)
	dir := t.TempDir()

	// a gzip dump
	dumpPath := filepath.Join(dir, "d.sql.gz")
	fh, _ := os.Create(dumpPath)
	zw := gzip.NewWriter(fh)
	zw.Write([]byte("SELECT 'synthetic';\n"))
	zw.Close()
	fh.Close()
	os.Chmod(dumpPath, 0o600)
	path, err := SQLAcquirer(rdmfetch.DumpReport{Path: dumpPath, Key: "src/d.sql.gz"})(ctx, ex)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := ex.Run(ctx, "cat "+path+"; stat -c '%U %a' "+path+" /var/tmp/clasm-sql-restore.download", time.Minute, time.Second)
	if err != nil || !strings.Contains(out, "SELECT 'synthetic';") || !strings.Contains(out, "root 600") {
		t.Fatalf("dump on the VM: %q %v", out, err)
	}

	// a snapshot repository
	repo := filepath.Join(dir, "repo")
	files := []string{"index-3", "index.latest", "snap-x.dat", "indices/aaa/0/d.dat", "indices/aaa/1/d.dat", "indices/bbb/0/d.dat"}
	for _, rel := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("content of "+rel), 0o644)
	}
	tarPath := filepath.Join(dir, "repo.tar")
	if _, err := rdmfetch.PackSnapshot(repo, tarPath); err != nil {
		t.Fatal(err)
	}
	dest := "/var/tmp/clasm-live-repo with space"
	if err := SnapshotAcquirer(Snapshot{TarPath: tarPath, Objects: len(files)})(ctx, ex, dest); err != nil {
		t.Fatal(err)
	}
	out, _, err = ex.Run(ctx, "find '"+dest+"' -type f | wc -l; cat '"+dest+"/indices/bbb/0/d.dat'; echo; stat -c '%U' '"+dest+"/index-3'; ls "+ScratchTarPath+" 2>&1; rm -rf '"+dest+"' /var/tmp/clasm-sql-restore.*", time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"6\n", "content of indices/bbb/0/d.dat", "root", "No such file"} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot on the VM: missing %q in %q", want, out)
		}
	}
}
