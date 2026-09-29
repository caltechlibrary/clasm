package workflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

var testOwner = ServiceOwner{UID: 1000, GID: 1001}

func TestBuildEnsureDirCommand(t *testing.T) {
	got := buildEnsureDirCommand("/opt/rdm_sql_backups", testOwner, "0750")
	want := "install -d -o 1000 -g 1001 -m 0750 '/opt/rdm_sql_backups'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The directory is operator-typed (the backup-directory prompt), so it has
// to survive a space and a quote the way every other SSM-bound path here does.
func TestBuildEnsureDirCommand_QuotesDirectory(t *testing.T) {
	got := buildEnsureDirCommand("/opt/it's a dir", testOwner, "0750")
	want := `install -d -o 1000 -g 1001 -m 0750 '/opt/it'\''s a dir'`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// DR-0176 rejected an ensure step that recurses or removes, by name: it
// runs on directories that already hold backups. Pin it on the command
// text so a later edit cannot add either without a test noticing.
func TestBuildEnsureDirCommand_NeverRecursesOrRemoves(t *testing.T) {
	got := buildEnsureDirCommand("/opt/rdm_sql_backups", testOwner, "0750")
	for _, banned := range []string{" -R", "-R ", "--recursive", "rm ", "rmdir", "chown", "chmod", "find ", "&&", ";"} {
		if strings.Contains(got, banned) {
			t.Errorf("command %q contains %q; an ensure step must be a single non-recursive, non-removing install -d", got, banned)
		}
	}
}

// Behaviour, not just text: run the real command against a real directory
// that already has a file in it, and check the file and the directory
// contents survive. install -d is documented to set the mode of an
// existing directory; whether it repairs the *owner* needs root, so that
// half is item 8's job on a real host.
func TestBuildEnsureDirCommand_KeepsExistingContentsAndFixesMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "dump.sql.gz")
	if err := os.WriteFile(keep, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// os.Getuid/Getgid: the only ids a non-root test may hand to install.
	cmd := buildEnsureDirCommand(dir, ServiceOwner{UID: os.Getuid(), GID: os.Getgid()}, "0750")
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Skipf("install -d unavailable or refused in this environment: %v: %s", err, out)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("existing directory mode = %#o, want 0750 (install -d must repair an existing directory, not only create one)", got)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "data" {
		t.Errorf("existing file was disturbed: %q, %v", b, err)
	}
}

func TestEnsureBackupDirectory_SendsInstallD(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	if err := EnsureBackupDirectory(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", testOwner, "0750", time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 1 {
		t.Fatalf("expected exactly 1 SendCommand call, got %d", fake.sendCommandCalls())
	}
	if want := "install -d -o 1000 -g 1001 -m 0750 '/opt/rdm_sql_backups'"; fake.lastCommandText != want {
		t.Errorf("sent %q, want %q", fake.lastCommandText, want)
	}
}

func TestEnsureBackupDirectory_FailedStatusNamesDirectoryAndInstance(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "install: cannot change owner"}
	err := EnsureBackupDirectory(context.Background(), fake, "i-abc", "/opt/rdm_sql_backups", testOwner, "0750", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"/opt/rdm_sql_backups", "i-abc", "cannot change owner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestEnsureBackupDirectory_PropagatesTransportError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errors.New("network is unreachable")}
	err := EnsureBackupDirectory(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", testOwner, "0750", time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "network is unreachable") {
		t.Errorf("expected the transport error to propagate, got: %v", err)
	}
}

// This step runs as root and sets an owner, so a typed "/" would hand the
// filesystem root to the service account, and a relative path would land
// wherever SSM's working directory happens to be. Refuse both before any
// command is sent.
func TestEnsureBackupDirectory_RefusesUnsafeDirectories(t *testing.T) {
	for _, dir := range []string{"", "/", "//", "/.", "/opt/..", "relative/dir", "./x", "opt/rdm_sql_backups"} {
		fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
		err := EnsureBackupDirectory(context.Background(), fake, "i-1", dir, testOwner, "0750", time.Second, testPollInterval)
		if err == nil {
			t.Errorf("dir %q: expected an error, got none", dir)
		}
		if fake.sendCommandCalls() != 0 {
			t.Errorf("dir %q: sent %d commands; an unsafe directory must be refused before anything is sent", dir, fake.sendCommandCalls())
		}
	}
}

func TestEnsureBackupDirectory_RefusesBadMode(t *testing.T) {
	for _, mode := range []string{"", "750; rm -rf /", "u+rwx", "0999", "07500", "rwx"} {
		fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
		err := EnsureBackupDirectory(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", testOwner, mode, time.Second, testPollInterval)
		if err == nil {
			t.Errorf("mode %q: expected an error, got none", mode)
		}
		if fake.sendCommandCalls() != 0 {
			t.Errorf("mode %q: sent %d commands, want none", mode, fake.sendCommandCalls())
		}
	}
}

func TestChownBackupDirectory_SendsRecursiveChownToTheOwner(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
	if err := ChownBackupDirectory(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", testOwner, time.Second, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.sendCommandCalls() != 1 {
		t.Fatalf("expected exactly 1 SendCommand call, got %d", fake.sendCommandCalls())
	}
	if want := "chown -R 1000:1001 '/opt/rdm_sql_backups'"; fake.lastCommandText != want {
		t.Errorf("sent %q, want %q", fake.lastCommandText, want)
	}
}

func TestChownBackupDirectory_FailedStatusNamesDirectoryAndInstance(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "chown: invalid user"}
	err := ChownBackupDirectory(context.Background(), fake, "i-abc", "/opt/rdm_sql_backups", testOwner, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"/opt/rdm_sql_backups", "i-abc", "invalid user"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestChownBackupDirectory_PropagatesTransportError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errors.New("network is unreachable")}
	err := ChownBackupDirectory(context.Background(), fake, "i-1", "/opt/rdm_sql_backups", testOwner, time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "network is unreachable") {
		t.Errorf("expected the transport error to propagate, got: %v", err)
	}
}

// `chown -R /` as root is the worst thing this package could send, so the
// recursive step gets the same refusal the ensure step has, before anything
// is sent.
func TestChownBackupDirectory_RefusesUnsafeDirectories(t *testing.T) {
	for _, dir := range []string{"", "/", "//", "/.", "/opt/..", "relative/dir"} {
		fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess}
		err := ChownBackupDirectory(context.Background(), fake, "i-1", dir, testOwner, time.Second, testPollInterval)
		if err == nil {
			t.Errorf("dir %q: expected an error, got none", dir)
		}
		if fake.sendCommandCalls() != 0 {
			t.Errorf("dir %q: sent %d commands; an unsafe directory must be refused before anything is sent", dir, fake.sendCommandCalls())
		}
	}
}

func TestDefaultOwnershipTimeout(t *testing.T) {
	if DefaultOwnershipTimeout < time.Minute {
		t.Errorf("DefaultOwnershipTimeout = %v; a recursive chown over a directory of dumps needs more than a minute of headroom", DefaultOwnershipTimeout)
	}
}
