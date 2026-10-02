package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCreateDirIfMissingCommand(t *testing.T) {
	got := buildCreateDirIfMissingCommand("/opt/rdm_sql_backups", 1000, "www-data", "0770")
	want := "[ -d '/opt/rdm_sql_backups' ] || install -d -o 1000 -g 'www-data' -m 0770 '/opt/rdm_sql_backups'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCreateDirIfMissingCommand_NeverRecursesOrRemoves(t *testing.T) {
	got := buildCreateDirIfMissingCommand("/opt/rdm_sql_backups", 1000, "www-data", "0770")
	for _, banned := range []string{" -R", "--recursive", "rm ", "rmdir", "chown", "chmod", "find "} {
		if strings.Contains(got, banned) {
			t.Errorf("command %q contains %q", got, banned)
		}
	}
}

// Behaviour, with a real shell. An existing directory keeps its mode and group
// exactly as they were -- the regression -- and a missing one is created.
func TestBuildCreateDirIfMissingCommand_LeavesAnExistingDirectoryAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil { // not subject to the umask
		t.Fatal(err)
	}
	group := currentGroupName(t)
	cmd := buildCreateDirIfMissingCommand(dir, os.Getuid(), group, "0770")
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v: %s", err, out)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("existing directory mode = %#o, want 0750 untouched (the old install -d would have made it 0770)", got)
	}
}

func TestBuildCreateDirIfMissingCommand_CreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	cmd := buildCreateDirIfMissingCommand(dir, os.Getuid(), currentGroupName(t), "0770")
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Skipf("install -d unavailable or refused in this environment: %v: %s", err, out)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory was not created: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o770 {
		t.Errorf("created directory mode = %#o, want 0770", got)
	}
}

func TestBuildHandOverDumpCommand(t *testing.T) {
	got := buildHandOverDumpCommand("/opt/rdm_sql_backups", "/opt/rdm_sql_backups/c-d-2026-10-02.sql.gz", 1000)
	want := `set -e; chown 1000:"$(stat -c %g '/opt/rdm_sql_backups')" '/opt/rdm_sql_backups/c-d-2026-10-02.sql.gz'; chmod 0664 '/opt/rdm_sql_backups/c-d-2026-10-02.sql.gz'`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, banned := range []string{" -R", "--recursive", "rm ", "find "} {
		if strings.Contains(got, banned) {
			t.Errorf("command %q contains %q; it must touch the one dump only", got, banned)
		}
	}
}

// Behaviour, with a real shell: only the named dump changes, it ends up
// group-writable in the directory's group, and the directory and every other
// file are left exactly as they were.
func TestBuildHandOverDumpCommand_ChangesOnlyTheNewDump(t *testing.T) {
	if err := exec.Command("sh", "-c", "stat -c %g /").Run(); err != nil {
		t.Skip("GNU stat -c is not available here; the command targets Ubuntu")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	newDump := filepath.Join(dir, "c-d-2026-10-02.sql.gz")
	other := filepath.Join(dir, "c-d-2026-10-01.sql.gz")
	for _, f := range []string{newDump, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := buildHandOverDumpCommand(dir, newDump, os.Getuid())
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v: %s", err, out)
	}
	if info, _ := os.Stat(newDump); info.Mode().Perm() != 0o664 {
		t.Errorf("new dump mode = %#o, want 0664", info.Mode().Perm())
	}
	if info, _ := os.Stat(other); info.Mode().Perm() != 0o600 {
		t.Errorf("another dump's mode = %#o, want 0600 untouched", info.Mode().Perm())
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o770 {
		t.Errorf("directory mode = %#o, want 0770 untouched", info.Mode().Perm())
	}
}

func currentGroupName(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("id", "-gn").Output()
	if err != nil {
		t.Skipf("id -gn unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}
