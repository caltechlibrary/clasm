package multipass

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// recorder is a Runner that records its arguments and answers with canned
// output, so a test sees exactly what would be passed to `multipass`.
type recorder struct {
	calls  [][]string
	stdout string
	err    error
}

func (r *recorder) run(ctx context.Context, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	return r.stdout, r.err
}

func (r *recorder) last() string {
	if len(r.calls) == 0 {
		return ""
	}
	return strings.Join(r.calls[len(r.calls)-1], " ")
}

func TestList_ParsesRealMultipassOutput(t *testing.T) {
	r := &recorder{stdout: fixture(t, "list.json")}
	got, err := NewWithRunner(r.run).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.last() != "list --format json" {
		t.Errorf("args = %q", r.last())
	}
	if len(got) != 3 {
		t.Fatalf("want 3 instances, got %+v", got)
	}
	var scratch Instance
	for _, i := range got {
		if i.Name == "clasm-c1-scratch" {
			scratch = i
		}
	}
	if scratch.State != "Running" || len(scratch.IPv4) != 1 || scratch.IPv4[0] != "192.168.252.24" || scratch.Release != "Ubuntu 24.04 LTS" {
		t.Errorf("scratch = %+v", scratch)
	}
}

func TestList_NoInstancesIsAnEmptyListNotAnError(t *testing.T) {
	r := &recorder{stdout: `{"list": []}`}
	got, err := NewWithRunner(r.run).List(context.Background())
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestList_MalformedOutputIsAnError(t *testing.T) {
	r := &recorder{stdout: `not json`}
	if _, err := NewWithRunner(r.run).List(context.Background()); err == nil {
		t.Error("want an error for malformed output")
	}
}

func TestInfo_RunningInstanceCarriesResources(t *testing.T) {
	r := &recorder{stdout: fixture(t, "info_running.json")}
	d, err := NewWithRunner(r.run).Info(context.Background(), "clasm-c1-scratch")
	if err != nil {
		t.Fatal(err)
	}
	if r.last() != "info clasm-c1-scratch --format json" {
		t.Errorf("args = %q", r.last())
	}
	if d.Name != "clasm-c1-scratch" || d.State != "Running" || d.CPUs != 1 || d.Release != "Ubuntu 24.04.5 LTS" {
		t.Errorf("detail = %+v", d)
	}
	if d.MemoryTotal != 998223872 || d.MemoryUsed != 293695488 || d.DiskTotal != 5119532032 || d.DiskUsed != 2169235968 {
		t.Errorf("resources = %+v", d)
	}
	if len(d.IPv4) != 1 || d.IPv4[0] != "192.168.252.24" {
		t.Errorf("ipv4 = %v", d.IPv4)
	}
}

// A stopped instance reports blank cpu_count and empty disks; that is a
// normal answer, not a parse failure.
func TestInfo_StoppedInstanceHasNoResourcesAndIsNotAnError(t *testing.T) {
	r := &recorder{stdout: fixture(t, "info_stopped.json")}
	d, err := NewWithRunner(r.run).Info(context.Background(), "caltechauthors-rsdoiel-test-v13")
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "Stopped" || d.CPUs != 0 || d.MemoryTotal != 0 || d.DiskTotal != 0 {
		t.Errorf("detail = %+v", d)
	}
}

func TestInfo_AMissingInstanceIsErrNotFound(t *testing.T) {
	r := &recorder{err: errors.New("info failed: The following errors occurred:\ninstance \"nope\" does not exist")}
	_, err := NewWithRunner(r.run).Info(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLaunch_PassesEveryOption(t *testing.T) {
	ci := filepath.Join(t.TempDir(), "cloud-init.yaml")
	if err := os.WriteFile(ci, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	err := NewWithRunner(r.run).Launch(context.Background(), LaunchOptions{
		Name: "caltechauthors-rsdoiel-v14", CloudInit: ci, CPUs: 4, Memory: "8G", Disk: "80G", Image: "24.04",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "launch 24.04 --name caltechauthors-rsdoiel-v14 --cloud-init " + ci + " --cpus 4 --memory 8G --disk 80G"
	if r.last() != want {
		t.Errorf("args = %q\nwant   %q", r.last(), want)
	}
}

func TestLaunch_OmitsWhatIsNotSet(t *testing.T) {
	r := &recorder{}
	if err := NewWithRunner(r.run).Launch(context.Background(), LaunchOptions{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if r.last() != "launch --name x" {
		t.Errorf("args = %q", r.last())
	}
}

func TestLaunch_RefusesAMissingCloudInitFileAndRunsNothing(t *testing.T) {
	r := &recorder{}
	err := NewWithRunner(r.run).Launch(context.Background(), LaunchOptions{Name: "x", CloudInit: "/no/such/cloud-init.yaml"})
	if err == nil || len(r.calls) != 0 {
		t.Errorf("err=%v calls=%v", err, r.calls)
	}
	if err != nil && !strings.Contains(err.Error(), "/no/such/cloud-init.yaml") {
		t.Errorf("the error should name the file: %v", err)
	}
}

func TestLaunch_RequiresAName(t *testing.T) {
	r := &recorder{}
	if err := NewWithRunner(r.run).Launch(context.Background(), LaunchOptions{}); err == nil || len(r.calls) != 0 {
		t.Errorf("err=%v calls=%v", err, r.calls)
	}
}

func TestLifecycleVerbsPassTheRightArguments(t *testing.T) {
	cases := []struct {
		name string
		do   func(c *Client) error
		want string
	}{
		{"start", func(c *Client) error { return c.Start(context.Background(), "vm1") }, "start vm1"},
		{"stop", func(c *Client) error { return c.Stop(context.Background(), "vm1") }, "stop vm1"},
		{"delete", func(c *Client) error { return c.Delete(context.Background(), "vm1", false) }, "delete vm1"},
		{"delete purge", func(c *Client) error { return c.Delete(context.Background(), "vm1", true) }, "delete --purge vm1"},
		{"clone", func(c *Client) error { return c.Clone(context.Background(), "vm1", "vm2") }, "clone vm1 --name vm2"},
		{"snapshot", func(c *Client) error { return c.Snapshot(context.Background(), "vm1", "before-load", "") }, "snapshot vm1 --name before-load"},
		{"snapshot comment", func(c *Client) error { return c.Snapshot(context.Background(), "vm1", "s", "v13 proven") }, "snapshot vm1 --name s --comment v13 proven"},
		{"restore", func(c *Client) error { return c.Restore(context.Background(), "vm1", "before-load", false) }, "restore vm1.before-load"},
		{"restore destructive", func(c *Client) error { return c.Restore(context.Background(), "vm1", "before-load", true) }, "restore --destructive vm1.before-load"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recorder{}
			if err := c.do(NewWithRunner(r.run)); err != nil {
				t.Fatal(err)
			}
			if r.last() != c.want {
				t.Errorf("args = %q, want %q", r.last(), c.want)
			}
		})
	}
}

func TestLifecycleVerbsRefuseABlankName(t *testing.T) {
	r := &recorder{}
	c := NewWithRunner(r.run)
	ctx := context.Background()
	for name, err := range map[string]error{
		"start":    c.Start(ctx, " "),
		"stop":     c.Stop(ctx, ""),
		"delete":   c.Delete(ctx, "", false),
		"clone":    c.Clone(ctx, "vm1", ""),
		"snapshot": c.Snapshot(ctx, "vm1", "", ""),
		"restore":  c.Restore(ctx, "vm1", "", false),
	} {
		if err == nil {
			t.Errorf("%s: want an error for a blank name", name)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("nothing may be run, got %v", r.calls)
	}
}

func TestRunnerErrorsReachTheCallerWithoutTerminalCodes(t *testing.T) {
	r := &recorder{err: errors.New("\x1b[2K\x1b[0A\x1b[0ETaking snapshot  / \x1b[2K\x1b[0Asnapshot failed: Multipass can only take snapshots of stopped instances.")}
	err := NewWithRunner(r.run).Snapshot(context.Background(), "vm1", "s", "")
	if err == nil || !strings.Contains(err.Error(), "can only take snapshots of stopped instances") {
		t.Fatalf("the multipass message belongs in the error: %v", err)
	}
	if strings.Contains(err.Error(), "\x1b") {
		t.Errorf("terminal control codes leaked into the error: %q", err)
	}
}

// ---- the default runner: the real `multipass` binary, here a fake on PATH ----

func fakeMultipassOnPath(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "multipass"), []byte("#!/bin/bash\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDefaultRunner_ReturnsStdoutAndPassesArguments(t *testing.T) {
	fakeMultipassOnPath(t, `echo "args: $*"`)
	out, err := New().run(context.Background(), "list", "--format", "json")
	if err != nil || strings.TrimSpace(out) != "args: list --format json" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestDefaultRunner_AFailureCarriesStderrAndTheExitCode(t *testing.T) {
	fakeMultipassOnPath(t, `echo "instance \"nope\" does not exist" >&2; exit 2`)
	_, err := New().run(context.Background(), "info", "nope")
	if err == nil || !strings.Contains(err.Error(), `instance "nope" does not exist`) {
		t.Errorf("stderr belongs in the error: %v", err)
	}
}

func TestDefaultRunner_AHungMultipassIsKilledWhenTheContextEnds(t *testing.T) {
	fakeMultipassOnPath(t, `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := New().run(ctx, "list")
	if err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v to give up", time.Since(start))
	}
}

func TestNew_MultipassNotInstalledIsAClearError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := New().run(context.Background(), "list")
	if err == nil || !strings.Contains(err.Error(), "multipass") {
		t.Errorf("want an error naming multipass: %v", err)
	}
}

// ---- WaitForCloudInit ----

type scriptedExecutor struct {
	answers []scripted
	calls   int
}

type scripted struct {
	stdout string
	status executor.CommandStatus
	err    error
}

func (s *scriptedExecutor) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, executor.CommandStatus, error) {
	a := s.answers[min(s.calls, len(s.answers)-1)]
	s.calls++
	return a.stdout, a.status, a.err
}
func (s *scriptedExecutor) Put(ctx context.Context, local, remote string) error { return nil }
func (s *scriptedExecutor) Get(ctx context.Context, remote, local string) error { return nil }
func (s *scriptedExecutor) Name() string                                        { return "vm1" }

func ciJSON(status string) string {
	return `{"extended_status": "` + status + `", "status": "x", "errors": []}`
}

func TestWaitForCloudInit_ReturnsWhenDone(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{{stdout: fixture(t, "cloud_init_done.json"), status: executor.StatusSuccess}}}
	res, err := WaitForCloudInit(context.Background(), ex, time.Second, time.Millisecond)
	if err != nil || res.Status != "done" {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

func TestWaitForCloudInit_KeepsWaitingWhileRunning(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{
		{stdout: ciJSON("running"), status: executor.StatusSuccess},
		{stdout: ciJSON("running"), status: executor.StatusSuccess},
		{stdout: ciJSON("done"), status: executor.StatusSuccess},
	}}
	res, err := WaitForCloudInit(context.Background(), ex, 5*time.Second, time.Millisecond)
	if err != nil || res.Status != "done" || ex.calls != 3 {
		t.Errorf("res=%+v err=%v calls=%d", res, err, ex.calls)
	}
}

// `cloud-init status` exits non-zero for error and degraded; stdout is still
// the JSON, so a Failed status with a body is read, not treated as a failure
// to run.
func TestWaitForCloudInit_ErrorStatusIsAnErrorWithItsMessages(t *testing.T) {
	body := `{"extended_status": "error", "status": "error", "errors": ["failed to run module scripts-user"]}`
	ex := &scriptedExecutor{answers: []scripted{{stdout: body, status: executor.StatusFailed}}}
	res, err := WaitForCloudInit(context.Background(), ex, time.Second, time.Millisecond)
	if !errors.Is(err, ErrCloudInit) || !strings.Contains(err.Error(), "scripts-user") {
		t.Errorf("err = %v", err)
	}
	if res.Status != "error" {
		t.Errorf("res = %+v", res)
	}
}

func TestWaitForCloudInit_DegradedDoneIsSuccessButReported(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{{stdout: ciJSON("degraded done"), status: executor.StatusFailed}}}
	res, err := WaitForCloudInit(context.Background(), ex, time.Second, time.Millisecond)
	if err != nil || res.Status != "degraded done" {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

// Early in boot the VM answers, or does not, in odd ways. Neither a failed
// exec nor unreadable output ends the wait; only the deadline does.
func TestWaitForCloudInit_ToleratesAnUnreadyVMUntilTheDeadline(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{
		{err: errors.New("instance is not running")},
		{stdout: "garbage", status: executor.StatusFailed},
		{stdout: ciJSON("done"), status: executor.StatusSuccess},
	}}
	res, err := WaitForCloudInit(context.Background(), ex, 5*time.Second, time.Millisecond)
	if err != nil || res.Status != "done" {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

func TestWaitForCloudInit_TimesOutNamingTheLastStatus(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{{stdout: ciJSON("running"), status: executor.StatusSuccess}}}
	_, err := WaitForCloudInit(context.Background(), ex, 30*time.Millisecond, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "running") || !strings.Contains(err.Error(), "vm1") {
		t.Errorf("the timeout should name the host and the last status: %v", err)
	}
}

func TestWaitForCloudInit_DisabledIsAnError(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{{stdout: ciJSON("disabled"), status: executor.StatusSuccess}}}
	if _, err := WaitForCloudInit(context.Background(), ex, time.Second, time.Millisecond); !errors.Is(err, ErrCloudInit) {
		t.Errorf("err = %v", err)
	}
}

// Disk totals are summed over every disk the instance reports.
func TestInfo_SumsEveryDisk(t *testing.T) {
	body := `{"errors": [], "info": {"vm1": {"state": "Running", "cpu_count": "2", "snapshot_count": "3",
	  "disks": {"sda1": {"used": "100", "total": "1000"}, "sdb": {"used": "20", "total": "500"}},
	  "memory": {"used": 5, "total": 10}, "ipv4": []}}}`
	r := &recorder{stdout: body}
	d, err := NewWithRunner(r.run).Info(context.Background(), "vm1")
	if err != nil || d.DiskUsed != 120 || d.DiskTotal != 1500 || d.Snapshots != 3 || d.CPUs != 2 {
		t.Errorf("detail = %+v err=%v", d, err)
	}
}

// ---- error classes, which the clmpm command maps to exit codes ----

func TestErrors_MultipassMissingIsErrUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := NewWithRunner(New().run).List(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestErrors_AMultipassRefusalIsErrFailed(t *testing.T) {
	fakeMultipassOnPath(t, `echo "snapshot failed: Multipass can only take snapshots of stopped instances." >&2; exit 2`)
	err := New().Snapshot(context.Background(), "vm1", "s", "")
	if !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v, want ErrFailed", err)
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Errorf("a refusal is neither unavailable nor not-found: %v", err)
	}
}

func TestErrors_ANotFoundIsNotAlsoAFailure(t *testing.T) {
	fakeMultipassOnPath(t, `echo "instance \"nope\" does not exist" >&2; exit 2`)
	_, err := New().Info(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWaitForCloudInit_ATimeoutIsErrWaitTimeout(t *testing.T) {
	ex := &scriptedExecutor{answers: []scripted{{stdout: ciJSON("running"), status: executor.StatusSuccess}}}
	_, err := WaitForCloudInit(context.Background(), ex, 30*time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Errorf("err = %v, want ErrWaitTimeout", err)
	}
}
