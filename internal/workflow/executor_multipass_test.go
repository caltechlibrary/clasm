package workflow

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A fake `multipass` whose "guest" is this machine, so the real shell commands
// the executor sends are run for real, in a sandbox of temp directories. It
// logs the first six arguments of every call, and can be told (by environment)
// to hang after the command finished, to corrupt a transfer, or to say the VM
// does not exist.
const fakeMultipassScript = `#!/bin/bash
printf '%s|%s|%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" "$5" "$6" >> "$FAKE_MP_LOG"
strip() { case "$1" in "$FAKE_MP_VM":*) echo "${1#"$FAKE_MP_VM":}";; *) echo "$1";; esac; }
case "$1" in
exec)
  vm=$2
  if [ -n "$FAKE_MP_NO_VM" ]; then echo "instance \"$vm\" does not exist" >&2; exit 2; fi
  shift 2; [ "$1" = "--" ] && shift; [ "$1" = "sudo" ] && shift
  if [ -n "$FAKE_MP_OPEN_STDIN" ]; then sleep 20 | "$@"; rc=${PIPESTATUS[1]}; else "$@"; rc=$?; fi
  if [ -n "$FAKE_MP_HANG_AFTER" ]; then sleep 300; fi
  exit $rc ;;
transfer)
  src=$(strip "$2"); dst=$(strip "$3")
  cp "$src" "$dst" || exit 1
  chmod 600 "$dst" # a transfer need not preserve the mode
  if [ -n "$FAKE_MP_CORRUPT" ]; then echo junk >> "$dst"; fi ;;
*) echo "fake multipass: unsupported: $*" >&2; exit 64 ;;
esac
`

func newFakeMultipass(t *testing.T) (*MultipassExecutor, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake multipass is a bash script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "multipass"), []byte(fakeMultipassScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Linux guests have sha256sum; macOS has shasum.
	if err := os.WriteFile(filepath.Join(dir, "sha256sum"), []byte("#!/bin/sh\nexec shasum -a 256 \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_MP_LOG", log)
	t.Setenv("FAKE_MP_VM", "vm-test")
	m := NewMultipassExecutor("vm-test")
	m.stageDir = t.TempDir()
	m.grace = 300 * time.Millisecond
	return m, log
}

func callLog(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

const mpPoll = 100 * time.Millisecond

func TestMultipassExecutor_Run_ReturnsStdoutAndSuccess(t *testing.T) {
	m, _ := newFakeMultipass(t)
	out, status, err := m.Run(context.Background(), "printf hello", 10*time.Second, mpPoll)
	if err != nil || status != StatusSuccess || out != "hello" {
		t.Errorf("out=%q status=%q err=%v, want \"hello\" Success nil", out, status, err)
	}
}

func TestMultipassExecutor_Run_KeepsATrailingNewline(t *testing.T) {
	m, _ := newFakeMultipass(t)
	out, _, err := m.Run(context.Background(), "echo hi", 10*time.Second, mpPoll)
	if err != nil || out != "hi\n" {
		t.Errorf("out=%q err=%v, want \"hi\\n\"", out, err)
	}
}

func TestMultipassExecutor_Run_RunsAMultiLineCommandWithQuotes(t *testing.T) {
	m, _ := newFakeMultipass(t)
	command := "cat <<'EOF'\nit's \"quoted\" $HOME\nEOF"
	out, status, err := m.Run(context.Background(), command, 10*time.Second, mpPoll)
	if err != nil || status != StatusSuccess || out != "it's \"quoted\" $HOME\n" {
		t.Errorf("out=%q status=%q err=%v", out, status, err)
	}
}

func TestMultipassExecutor_Run_StdinIsClosedSoACommandCannotSwallowTheScript(t *testing.T) {
	m, _ := newFakeMultipass(t)
	t.Setenv("FAKE_MP_OPEN_STDIN", "1") // a transport that leaves stdin attached and open
	out, _, err := m.Run(context.Background(), "cat; echo done", 10*time.Second, mpPoll)
	if err != nil || out != "done\n" {
		t.Errorf("out=%q err=%v, want \"done\\n\"", out, err)
	}
}

func TestMultipassExecutor_Run_ANonZeroExitIsFailedNotAnError(t *testing.T) {
	m, _ := newFakeMultipass(t)
	out, status, err := m.Run(context.Background(), "echo partial; exit 3", 10*time.Second, mpPoll)
	if err != nil || status != StatusFailed || out != "partial\n" {
		t.Errorf("out=%q status=%q err=%v, want \"partial\\n\" Failed nil", out, status, err)
	}
}

func TestMultipassExecutor_Run_SendsNoTTYFlags(t *testing.T) {
	m, log := newFakeMultipass(t)
	if _, _, err := m.Run(context.Background(), "true", 10*time.Second, mpPoll); err != nil {
		t.Fatal(err)
	}
	calls := callLog(t, log)
	if len(calls) != 1 || calls[0] != "exec|vm-test|--|sudo|bash|-c" {
		t.Errorf("calls = %q, want one plain `exec vm-test -- sudo bash -c`", calls)
	}
}

func TestMultipassExecutor_Run_ReturnsPromptlyWhenMultipassHangsAfterTheCommandFinished(t *testing.T) {
	m, _ := newFakeMultipass(t)
	t.Setenv("FAKE_MP_HANG_AFTER", "1") // the fake sleeps 300 s after the command is done
	start := time.Now()
	out, status, err := m.Run(context.Background(), "echo finished", 8*time.Second, mpPoll)
	if err != nil || status != StatusSuccess || out != "finished\n" {
		t.Errorf("out=%q status=%q err=%v", out, status, err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("took %s: a hang after the command finished must not be waited out", el)
	}
}

func TestMultipassExecutor_Run_TimesOutAndKillsTheCommand(t *testing.T) {
	m, _ := newFakeMultipass(t)
	marker := filepath.Join(t.TempDir(), "marker")
	_, _, err := m.Run(context.Background(), "sleep 1; touch "+marker, 300*time.Millisecond, mpPoll)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the command kept running after the timeout")
	}
}

func TestMultipassExecutor_Run_AnCancelledContextStopsIt(t *testing.T) {
	m, _ := newFakeMultipass(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _, err := m.Run(ctx, "sleep 5", 30*time.Second, mpPoll)
	if err == nil {
		t.Fatal("want an error")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("took %s to notice the cancellation", el)
	}
}

func TestMultipassExecutor_Run_AMissingVMIsAnErrorNamingIt(t *testing.T) {
	m, _ := newFakeMultipass(t)
	t.Setenv("FAKE_MP_NO_VM", "1")
	_, _, err := m.Run(context.Background(), "true", 10*time.Second, mpPoll)
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "vm-test") {
		t.Errorf("want an error saying vm-test does not exist, got %v", err)
	}
}

func TestMultipassExecutor_Run_ALongStepRunsDetachedAndIsPolled(t *testing.T) {
	m, log := newFakeMultipass(t)
	m.longStep = 500 * time.Millisecond
	out, status, err := m.Run(context.Background(), "sleep 1; echo hi", 30*time.Second, 200*time.Millisecond)
	if err != nil || status != StatusSuccess || out != "hi\n" {
		t.Errorf("out=%q status=%q err=%v", out, status, err)
	}
	if n := len(callLog(t, log)); n < 3 {
		t.Errorf("a detached step is one start call and at least two polls, got %d calls", n)
	}
}

func TestMultipassExecutor_StartReturnsBeforeTheStepFinishesAndWaitCollectsIt(t *testing.T) {
	m, _ := newFakeMultipass(t)
	start := time.Now()
	d, err := m.Start(context.Background(), "sleep 1; echo late")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if el := time.Since(start); el > 800*time.Millisecond {
		t.Errorf("Start took %s; it must return as soon as the step is started", el)
	}
	out, status, err := d.Wait(context.Background(), 30*time.Second, 200*time.Millisecond)
	if err != nil || status != StatusSuccess || out != "late\n" {
		t.Errorf("out=%q status=%q err=%v", out, status, err)
	}
}

func TestMultipassExecutor_WaitTimesOutWhileTheStepKeepsRunning(t *testing.T) {
	m, _ := newFakeMultipass(t)
	d, err := m.Start(context.Background(), "sleep 3")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = d.Wait(context.Background(), 600*time.Millisecond, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("want a still-running error, got %v", err)
	}
}

func TestMultipassExecutor_WaitReportsAFailedStep(t *testing.T) {
	m, _ := newFakeMultipass(t)
	d, err := m.Start(context.Background(), "echo oops; exit 4")
	if err != nil {
		t.Fatal(err)
	}
	out, status, err := d.Wait(context.Background(), 30*time.Second, 100*time.Millisecond)
	if err != nil || status != StatusFailed || out != "oops\n" {
		t.Errorf("out=%q status=%q err=%v", out, status, err)
	}
}

func TestMultipassExecutor_Put_CopiesAFileCreatingParentsAndKeepingItsMode(t *testing.T) {
	m, _ := newFakeMultipass(t)
	local := writeTempFile(t, []byte("payload\n")) // mode 0640
	remote := filepath.Join(t.TempDir(), "sub", "dir", "file.txt")
	if err := m.Put(context.Background(), local, remote); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(remote)
	if err != nil || string(got) != "payload\n" {
		t.Errorf("remote content = %q err=%v", got, err)
	}
	if fi, _ := os.Stat(remote); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi)
	}
}

func TestMultipassExecutor_Put_RefusesACorruptTransferAndInstallsNothing(t *testing.T) {
	m, _ := newFakeMultipass(t)
	t.Setenv("FAKE_MP_CORRUPT", "1")
	remote := filepath.Join(t.TempDir(), "file.txt")
	err := m.Put(context.Background(), writeTempFile(t, []byte("payload\n")), remote)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("want a checksum error, got %v", err)
	}
	if _, statErr := os.Stat(remote); statErr == nil {
		t.Error("a corrupt file must not be installed at the destination")
	}
}

func TestMultipassExecutor_Put_RefusesARelativeRemotePath(t *testing.T) {
	m, log := newFakeMultipass(t)
	if err := m.Put(context.Background(), writeTempFile(t, []byte("x")), "relative/path"); err == nil {
		t.Error("a relative destination must be refused")
	}
	if n := len(callLog(t, log)); n != 0 {
		t.Errorf("nothing may be sent, got %d calls", n)
	}
}

func TestMultipassExecutor_Get_CopiesAFileAndVerifiesIt(t *testing.T) {
	m, _ := newFakeMultipass(t)
	remote := filepath.Join(t.TempDir(), "from-vm.txt")
	if err := os.WriteFile(remote, []byte("from-vm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "got.txt")
	if err := m.Get(context.Background(), remote, local); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b, err := os.ReadFile(local); err != nil || string(b) != "from-vm\n" {
		t.Errorf("local content = %q err=%v", b, err)
	}
}

func TestMultipassExecutor_Get_RefusesACorruptTransferAndLeavesNoLocalFile(t *testing.T) {
	m, _ := newFakeMultipass(t)
	t.Setenv("FAKE_MP_CORRUPT", "1")
	remote := filepath.Join(t.TempDir(), "from-vm.txt")
	if err := os.WriteFile(remote, []byte("from-vm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	local := filepath.Join(dir, "got.txt")
	err := m.Get(context.Background(), remote, local)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("want a checksum error, got %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("nothing may be left locally, found %v", ents)
	}
}

func TestMultipassExecutor_Name_IsTheVMName(t *testing.T) {
	if got := NewMultipassExecutor("caltechauthors-rsdoiel-v14").Name(); got != "caltechauthors-rsdoiel-v14" {
		t.Errorf("Name() = %q", got)
	}
}

var _ Executor = (*MultipassExecutor)(nil)

func TestMultipassExecutor_StartFailsWhenTheStepCannotBeStarted(t *testing.T) {
	m, _ := newFakeMultipass(t)
	m.stageDir = "/dev/null/no-such-dir" // nothing can be created under a device file
	if _, err := m.Start(context.Background(), "echo never"); err == nil {
		t.Error("Start must report a step that could not be started")
	}
}
