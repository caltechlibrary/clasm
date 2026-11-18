package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// multipassSentinel marks the end of a command's output. The command's own
	// exit code follows it. It lets Run return the moment the command is done
	// even when `multipass exec` itself does not return (it can hang after
	// the guest command has finished).
	multipassSentinel = "__CLASM_EXIT__:"

	defaultMultipassGrace           = 2 * time.Second
	defaultMultipassLongStep        = 5 * time.Minute
	defaultMultipassTransferTimeout = 30 * time.Minute
	defaultMultipassStageDir        = "/var/tmp"

	// multipassControlTimeout bounds the short calls that start, poll and
	// clean up a detached step, and that verify a transferred file.
	multipassControlTimeout = 2 * time.Minute
)

// MultipassExecutor is the Executor for a local Multipass VM (DR-0201). It
// runs commands with `multipass exec NAME -- sudo bash -c`, moves files with
// `multipass transfer`, and carries the rules learned by hand: every call has
// a kill timer, there is no TTY, a command is written to a file and run with
// `bash FILE </dev/null` so it cannot swallow the script from stdin, a long
// step runs detached and is polled, and every transferred file is checked by
// checksum on both sides.
type MultipassExecutor struct {
	vm              string
	stageDir        string        // where the guest keeps scratch files; default /var/tmp
	grace           time.Duration // how long to wait for multipass to exit after the exit marker; default 2 s
	longStep        time.Duration // a Run with a longer timeout runs detached; default 5 min
	transferTimeout time.Duration // bound on one `multipass transfer`; default 30 min
}

// NewMultipassExecutor returns an Executor for the named Multipass VM.
//
// @param vm {string} the Multipass instance name
// @returns {*MultipassExecutor} the executor
// @example
//
//	ex := NewMultipassExecutor("caltechauthors-rsdoiel-v14")
//	out, status, err := ex.Run(ctx, "uname -a", time.Minute, 5*time.Second)
func NewMultipassExecutor(vm string) *MultipassExecutor {
	return &MultipassExecutor{vm: vm}
}

// Name implements Executor and returns the VM name.
//
// @returns {string} the Multipass instance name
func (m *MultipassExecutor) Name() string { return m.vm }

func (m *MultipassExecutor) bin() string { return "multipass" }

func (m *MultipassExecutor) stage() string {
	return cmpOr(m.stageDir, defaultMultipassStageDir)
}

func (m *MultipassExecutor) graceOrDefault() time.Duration {
	if m.grace > 0 {
		return m.grace
	}
	return defaultMultipassGrace
}

func (m *MultipassExecutor) longStepLimit() time.Duration {
	if m.longStep > 0 {
		return m.longStep
	}
	return defaultMultipassLongStep
}

func (m *MultipassExecutor) transferLimit() time.Duration {
	if m.transferTimeout > 0 {
		return m.transferTimeout
	}
	return defaultMultipassTransferTimeout
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func statusOfExit(rc int) CommandStatus {
	if rc == 0 {
		return StatusSuccess
	}
	return StatusFailed
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sentinelWriter collects a command's stdout and signals when the exit marker
// line is complete.
type sentinelWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
	once sync.Once
}

func (w *sentinelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	b := w.buf.Bytes()
	tail := b[max(0, len(b)-512):]
	if i := bytes.LastIndex(tail, []byte("\n"+multipassSentinel)); i >= 0 && bytes.IndexByte(tail[i+1:], '\n') >= 0 {
		w.once.Do(func() { close(w.done) })
	}
	return len(p), nil
}

func (w *sentinelWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// stepScript wraps body so the guest runs it from a file with stdin closed and
// prints the exit marker last.
func (m *MultipassExecutor) stepScript(body string) string {
	return fmt.Sprintf("F=$(mktemp %s)\nbase64 -d > \"$F\" <<'__CLASM_B64__'\n%s\n__CLASM_B64__\nchmod 700 \"$F\"; bash \"$F\" </dev/null; rc=$?; rm -f \"$F\"; printf '\\n%s%%s\\n' \"$rc\"; exit $rc\n",
		shellQuote(m.stage()+"/clasm-step.XXXXXX"), base64.StdEncoding.EncodeToString([]byte(body)), multipassSentinel)
}

// runScript runs body on the guest through one `multipass exec` and returns its
// stdout and exit code. An error means multipass could not run it or it did not
// finish within timeout.
func (m *MultipassExecutor) runScript(ctx context.Context, body string, timeout time.Duration) (string, int, error) {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx2, m.bin(), "exec", m.vm, "--", "sudo", "bash", "-c", m.stepScript(body))
	prepareProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	w := &sentinelWriter{done: make(chan struct{})}
	var stderr bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", 0, fmt.Errorf("starting multipass: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-w.done:
		select {
		case waitErr = <-waitCh:
		case <-time.After(m.graceOrDefault()):
			killProcess(cmd) // the command is done; multipass did not return
			waitErr = <-waitCh
		}
	}
	out := w.String()
	if i := strings.LastIndex(out, "\n"+multipassSentinel); i >= 0 {
		rc, convErr := strconv.Atoi(strings.TrimSpace(out[i+1+len(multipassSentinel):]))
		if convErr != nil {
			return "", 0, fmt.Errorf("multipass exec on %s returned an unreadable exit marker: %q", m.vm, out[i+1:])
		}
		return out[:i], rc, nil
	}
	if ctx.Err() != nil {
		return "", 0, fmt.Errorf("multipass exec on %s was cancelled: %w", m.vm, ctx.Err())
	}
	if ctx2.Err() != nil {
		return "", 0, fmt.Errorf("timed out after %s waiting for a command to finish on %s", timeout, m.vm)
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(out)
	}
	if waitErr == nil {
		return "", 0, fmt.Errorf("multipass exec on %s returned without the command's exit marker: %s", m.vm, msg)
	}
	return "", 0, fmt.Errorf("multipass exec on %s failed: %s (%v)", m.vm, msg, waitErr)
}

// Run implements Executor. A command with a timeout above the long-step limit
// runs detached and is polled (see Start); a shorter one runs in one call.
//
// @param ctx {context.Context} cancels the call
// @param command {string} the shell text to run as root on the VM
// @param timeout {time.Duration} how long the command may take
// @param pollInterval {time.Duration} how often to poll a detached step
// @returns {string} the command's standard output
// @returns {CommandStatus} Success or Failed from the command's exit code
// @returns {error} why it could not be run or did not finish in time, or nil
func (m *MultipassExecutor) Run(ctx context.Context, command string, timeout, pollInterval time.Duration) (string, CommandStatus, error) {
	if timeout > m.longStepLimit() {
		d, err := m.Start(ctx, command)
		if err != nil {
			return "", "", err
		}
		return d.Wait(ctx, timeout, pollInterval)
	}
	out, rc, err := m.runScript(ctx, command, timeout)
	if err != nil {
		return "", "", err
	}
	return out, statusOfExit(rc), nil
}

// DetachedRun is a step started on the VM that keeps running with no
// connection held open: its output goes to a log and its exit code to a marker
// file, which Wait polls.
type DetachedRun struct {
	m   *MultipassExecutor
	dir string
}

// Start starts command detached on the VM and returns as soon as it is
// running. Use Wait to collect it, or ignore the result to fire and forget.
//
// @param ctx {context.Context} cancels the start call
// @param command {string} the shell text to run as root on the VM
// @returns {*DetachedRun} a handle to the running step
// @returns {error} why the step could not be started
// @example
//
//	d, err := ex.Start(ctx, "docker exec db pg_dump ...")
//	out, status, err := d.Wait(ctx, time.Hour, 10*time.Second)
func (m *MultipassExecutor) Start(ctx context.Context, command string) (*DetachedRun, error) {
	dir := m.stage() + "/clasm-run-" + randHex(6)
	q := shellQuote
	inner := "bash " + q(dir+"/step.sh") + " </dev/null > " + q(dir+"/log") + " 2>&1; echo $? > " + q(dir+"/rc.tmp") + "; mv " + q(dir+"/rc.tmp") + " " + q(dir+"/rc")
	body := fmt.Sprintf("set -e\nmkdir -p %[1]s\nbase64 -d > %[2]s <<'__CLASM_B64__'\n%[3]s\n__CLASM_B64__\nchmod 700 %[2]s\nnohup bash -c %[4]s >/dev/null 2>&1 </dev/null &\necho started\n",
		q(dir), q(dir+"/step.sh"), base64.StdEncoding.EncodeToString([]byte(command)), q(inner))
	out, rc, err := m.runScript(ctx, body, multipassControlTimeout)
	if err != nil {
		return nil, fmt.Errorf("starting a detached step on %s: %w", m.vm, err)
	}
	if rc != 0 || !strings.Contains(out, "started") {
		return nil, fmt.Errorf("starting a detached step on %s failed (exit %d): %s", m.vm, rc, strings.TrimSpace(out))
	}
	return &DetachedRun{m: m, dir: dir}, nil
}

// Wait polls until the detached step has finished and returns its output and
// status. If timeout passes first it returns an error and leaves the step
// running; the error names its log.
//
// @param ctx {context.Context} cancels the wait
// @param timeout {time.Duration} how long to wait
// @param pollInterval {time.Duration} how often to look
// @returns {string} the step's combined output, at most its last megabyte
// @returns {CommandStatus} Success or Failed from the step's exit code
// @returns {error} why the wait could not finish, or nil
func (d *DetachedRun) Wait(ctx context.Context, timeout, pollInterval time.Duration) (string, CommandStatus, error) {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	q := shellQuote
	poll := fmt.Sprintf("if [ -f %[1]s ]; then echo DONE; cat %[1]s; tail -c 1048576 %[2]s; else echo RUNNING; fi\n", q(d.dir+"/rc"), q(d.dir+"/log"))
	deadline := time.Now().Add(timeout)
	for {
		out, rc, err := d.m.runScript(ctx, poll, multipassControlTimeout)
		if err != nil {
			return "", "", err
		}
		if rc == 0 && strings.HasPrefix(out, "DONE\n") {
			rest := out[len("DONE\n"):]
			nl := strings.IndexByte(rest, '\n')
			if nl < 0 {
				return "", "", fmt.Errorf("unreadable poll of the step on %s: %q", d.m.vm, out)
			}
			code, convErr := strconv.Atoi(strings.TrimSpace(rest[:nl]))
			if convErr != nil {
				return "", "", fmt.Errorf("unreadable exit code of the step on %s: %q", d.m.vm, rest[:nl])
			}
			d.m.runScript(ctx, "rm -rf "+q(d.dir)+"\n", multipassControlTimeout) // best effort
			return rest[nl+1:], statusOfExit(code), nil
		}
		if !time.Now().Before(deadline) {
			return "", "", fmt.Errorf("timed out after %s: the step is still running on %s (its log is %s/log)", timeout, d.m.vm, d.dir)
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("cancelled while waiting for the step on %s: %w", d.m.vm, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// transfer runs one `multipass transfer`, under a kill timer.
func (m *MultipassExecutor) transfer(ctx context.Context, src, dst string) error {
	ctx2, cancel := context.WithTimeout(ctx, m.transferLimit())
	defer cancel()
	cmd := exec.CommandContext(ctx2, m.bin(), "transfer", src, dst)
	prepareProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("multipass transfer %s %s: %s (%v)", src, dst, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Put implements Executor: it sends local to a scratch path on the VM, checks
// the checksum there, then installs it at remote (creating parent directories)
// owned by root with local's mode. A corrupt transfer installs nothing.
//
// @param ctx {context.Context} cancels the copy
// @param local {string} the local file to send
// @param remote {string} the absolute destination path on the VM
// @returns {error} why the file did not arrive intact, or nil
func (m *MultipassExecutor) Put(ctx context.Context, local, remote string) error {
	if !path.IsAbs(remote) {
		return fmt.Errorf("MultipassExecutor.Put: the destination must be an absolute path, got %q", remote)
	}
	info, err := os.Stat(local)
	if err != nil {
		return fmt.Errorf("MultipassExecutor.Put: %w", err)
	}
	want, err := fileSHA256(local)
	if err != nil {
		return fmt.Errorf("MultipassExecutor.Put: %w", err)
	}
	tmp := m.stage() + "/clasm-put-" + randHex(6)
	if err := m.transfer(ctx, local, m.vm+":"+tmp); err != nil {
		return fmt.Errorf("copying %s to %s:%s: %w", local, m.vm, remote, err)
	}
	q := shellQuote
	body := fmt.Sprintf("set -e\ngot=$(sha256sum %[1]s | cut -d' ' -f1)\nif [ \"$got\" != %[2]s ]; then rm -f %[1]s; echo \"checksum mismatch: the VM has $got\"; exit 3; fi\nmkdir -p %[3]s\nchmod %[4]s %[1]s\nchown \"$(id -u):$(id -g)\" %[1]s\nmv -f %[1]s %[5]s\necho ok\n",
		q(tmp), q(want), q(path.Dir(remote)), fmt.Sprintf("%04o", info.Mode().Perm()), q(remote))
	out, rc, err := m.runScript(ctx, body, multipassControlTimeout)
	if err != nil {
		return fmt.Errorf("installing %s on %s: %w", remote, m.vm, err)
	}
	if rc != 0 {
		return fmt.Errorf("copying %s to %s:%s failed (exit %d); nothing was installed: %s", local, m.vm, remote, rc, strings.TrimSpace(out))
	}
	return nil
}

// Get implements Executor: it copies remote to a world-readable scratch path on
// the VM, transfers it to a temporary file beside local, checks the checksum,
// and only then moves it into place. Nothing is left at local if it fails.
//
// @param ctx {context.Context} cancels the copy
// @param remote {string} the absolute path of the file on the VM
// @param local {string} where to write it locally
// @returns {error} why the file did not arrive intact, or nil
func (m *MultipassExecutor) Get(ctx context.Context, remote, local string) error {
	if !path.IsAbs(remote) {
		return fmt.Errorf("MultipassExecutor.Get: the source must be an absolute path, got %q", remote)
	}
	tmp := m.stage() + "/clasm-get-" + randHex(6)
	q := shellQuote
	body := fmt.Sprintf("set -e\nsum=$(sha256sum %[1]s | cut -d' ' -f1)\ncp %[1]s %[2]s\nchmod 0644 %[2]s\necho \"$sum\"\n", q(remote), q(tmp))
	out, rc, err := m.runScript(ctx, body, multipassControlTimeout)
	if err != nil {
		return fmt.Errorf("reading %s on %s: %w", remote, m.vm, err)
	}
	if rc != 0 {
		return fmt.Errorf("reading %s on %s failed (exit %d): %s", remote, m.vm, rc, strings.TrimSpace(out))
	}
	defer m.runScript(ctx, "rm -f "+q(tmp)+"\n", multipassControlTimeout) // best effort
	want := ""
	if f := strings.Fields(out); len(f) > 0 {
		want = f[0]
	}
	part := local + ".clasm-part"
	if err := m.transfer(ctx, m.vm+":"+tmp, part); err != nil {
		os.Remove(part)
		return fmt.Errorf("copying %s:%s to %s: %w", m.vm, remote, local, err)
	}
	got, err := fileSHA256(part)
	if err != nil || got != want {
		os.Remove(part)
		if err != nil {
			return fmt.Errorf("MultipassExecutor.Get: %w", err)
		}
		return fmt.Errorf("checksum mismatch copying %s:%s to %s: the VM has %s, the local copy has %s", m.vm, remote, local, want, got)
	}
	if err := os.Rename(part, local); err != nil {
		os.Remove(part)
		return fmt.Errorf("MultipassExecutor.Get: %w", err)
	}
	return nil
}
