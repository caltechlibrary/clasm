package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Found live 2026-09-30 on caltechauthors-test-v13: loading a --column-inserts
// dump is one autocommitted INSERT per row, so every row waits for an fsync. The
// instance was 86% idle with 10% iowait and the database grew about 10 MB a
// minute; production's database is 3.3 GB, so the restore would have taken five
// hours. Setting synchronous_commit=off for the load session only (PGOPTIONS,
// which libpq reads) keeps everything else about the load as it was -- still one
// statement at a time, still tolerant of the harmless ownership errors these
// dumps carry, which a --single-transaction load would turn into a total
// failure -- and makes the commits asynchronous. A restore is rerunnable and
// drops the database on failure anyway, so giving up crash-durability for the
// duration of the load costs nothing.

func TestBuildRestoreSQLCommands_LoadRunsWithAsynchronousCommit(t *testing.T) {
	_, _, loadCmd := buildRestoreSQLCommands("caltechauthors-db-1", "caltechauthors", "caltechauthors", "/tmp/backup.sql")
	if !strings.Contains(loadCmd, "PGOPTIONS=") || !strings.Contains(loadCmd, "synchronous_commit=off") {
		t.Errorf("the load must run its session with synchronous_commit=off, got: %s", loadCmd)
	}
	if strings.Contains(loadCmd, "--single-transaction") || strings.Contains(loadCmd, " -1 ") {
		t.Errorf("a single transaction would make one harmless error fail the whole load, got: %s", loadCmd)
	}
}

// Only the load is sped up: the drop and create statements are not affected,
// and no server-wide setting is touched.
func TestBuildRestoreSQLCommands_OnlyTheLoadSessionChangesItsCommitMode(t *testing.T) {
	dropCmd, createCmd, _ := buildRestoreSQLCommands("c", "db", "u", "/tmp/b.sql")
	for _, c := range []string{dropCmd, createCmd} {
		if strings.Contains(c, "synchronous_commit") || strings.Contains(c, "ALTER SYSTEM") || strings.Contains(c, "ALTER ROLE") {
			t.Errorf("only the load session may change its commit mode, got: %s", c)
		}
	}
	_, _, loadCmd := buildRestoreSQLCommands("c", "db", "u", "/tmp/b.sql")
	for _, bad := range []string{"ALTER SYSTEM", "ALTER ROLE", "ALTER DATABASE", "pg_reload_conf"} {
		if strings.Contains(loadCmd, bad) {
			t.Errorf("the load must not change a server-wide or role-wide setting (%q): %s", bad, loadCmd)
		}
	}
}

// The real command under a real sh with a fake docker: the option must arrive as
// ONE argument to `docker exec -e`, spaces intact, before the container name,
// with the dump still redirected in as stdin.
func TestBuildRestoreSQLCommands_LoadPassesTheOptionAsOneArgumentToDockerExec(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Prints one argument per line, then the stdin it was given.
	fake := "#!/bin/sh\nfor a in \"$@\"; do echo \"ARG:$a\"; done\necho \"STDIN:$(cat)\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, "dump.sql")
	if err := os.WriteFile(dump, []byte("INSERT INTO t VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, loadCmd := buildRestoreSQLCommands("the-db-1", "mydb", "me", dump)
	cmd := exec.Command("sh", "-c", loadCmd)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, out)
	}
	args := strings.Split(strings.TrimSpace(string(out)), "\n")
	var seen []string
	for _, l := range args {
		if a, ok := strings.CutPrefix(l, "ARG:"); ok {
			seen = append(seen, a)
		}
	}
	want := []string{"exec", "-i", "-e", "PGOPTIONS=-c synchronous_commit=off", "the-db-1", "psql", "--username=me", "mydb"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("docker was called with %q, want %q", seen, want)
	}
	if !strings.Contains(string(out), "STDIN:INSERT INTO t VALUES (1);") {
		t.Errorf("the dump must still be redirected in as stdin, got:\n%s", out)
	}
}
