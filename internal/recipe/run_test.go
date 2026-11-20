package recipe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// localExec is an Executor whose "host" is this machine: it runs the exact
// command text with sh -c, as SSM's RunShellScript does, so the runner's whole
// remote protocol (script transport, exports, exit codes, detach and poll) is
// exercised, not mocked.
type localExec struct {
	mu       sync.Mutex
	commands []string
	canned   func(command string) (string, executor.CommandStatus, error) // when set, replaces running
	stdin    string                                                       // what the command may read from its standard input
	failPoll int                                                          // the next N poll commands fail with an error
}

func (l *localExec) calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.commands)
}

func (l *localExec) Run(ctx context.Context, command string, timeout, _ time.Duration) (string, executor.CommandStatus, error) {
	l.mu.Lock()
	l.commands = append(l.commands, command)
	l.mu.Unlock()
	if l.canned != nil {
		return l.canned(command)
	}
	l.mu.Lock()
	fail := strings.Contains(command, runningMarker) && l.failPoll > 0
	if fail {
		l.failPoll--
	}
	l.mu.Unlock()
	if fail {
		return "", "", errors.New("transient")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stdin = strings.NewReader(l.stdin)
	cmd.WaitDelay = 500 * time.Millisecond
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return out.String(), "", fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), executor.StatusFailed, nil
		}
		return out.String(), "", err
	}
	return out.String(), executor.StatusSuccess, nil
}
func (l *localExec) Put(context.Context, string, string) error { return errors.New("not used") }
func (l *localExec) Get(context.Context, string, string) error { return errors.New("not used") }
func (l *localExec) Name() string                              { return "fake-host" }

func step(t *testing.T, file, name, changes, body string, extra ...string) Step {
	t.Helper()
	src := "#!/bin/bash\n# step: " + name + "\n# changes: " + changes + "\n" + strings.Join(extra, "") + body + "\n"
	s, err := ParseStep(file, []byte(src))
	if err != nil {
		t.Fatalf("test recipe: %v", err)
	}
	return s
}

type harness struct {
	ex    *localExec
	text  *bytes.Buffer
	jsonl *bytes.Buffer
	dir   string
}

func newHarness(t *testing.T) (*harness, Options) {
	h := &harness{ex: &localExec{}, text: &bytes.Buffer{}, jsonl: &bytes.Buffer{}, dir: t.TempDir()}
	return h, Options{Apply: true, RemoteDir: filepath.Join(h.dir, "remote"), RunID: "run1", Poll: 50 * time.Millisecond, Text: h.text, JSONL: h.jsonl, HostNames: []string{"i-0abc", "box"}}
}

func (h *harness) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(h.jsonl.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("a JSON line is not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func (h *harness) exists(name string) bool {
	_, err := os.Stat(filepath.Join(h.dir, name))
	return err == nil
}

// toy builds the three-step toy recipe of the plan: s2 fails when $FLAG exists.
func toy(t *testing.T, h *harness) []Step {
	m := func(n string) string { return `touch "` + h.dir + `/ran_` + n + `"` }
	return []Step{
		step(t, "00_pre.sh", "pre", "nothing", m("pre")+"\necho 'COUNT records=148425'\necho 'COUNT note=ok'\necho 'token=hunter2'"),
		step(t, "10_mid.sh", "mid", "nothing", m("mid")+"\n[ -e \""+h.dir+"/fail\" ] && { echo boom; exit 3; }\necho 'COUNT n=2'"),
		step(t, "20_post.sh", "post", "nothing", m("post")+"\necho 'COUNT n=3'"),
	}
}

func TestRun_DryRunRunsNothingAndPrintsThePlan(t *testing.T) {
	h, opts := newHarness(t)
	opts.Apply = false
	res, err := Run(context.Background(), h.ex, toy(t, h), opts)
	if err != nil || len(res) != 0 || h.ex.calls() != 0 {
		t.Fatalf("a dry run touches nothing: results=%d calls=%d err=%v", len(res), h.ex.calls(), err)
	}
	for _, want := range []string{"pre", "mid", "post", "--apply"} {
		if !strings.Contains(h.text.String(), want) {
			t.Errorf("the plan lacks %q:\n%s", want, h.text.String())
		}
	}
	if h.jsonl.Len() != 0 {
		t.Error("a dry run writes no step lines")
	}
}

func TestRun_AppliesInOrderAndWritesOneJSONLinePerStep(t *testing.T) {
	h, opts := newHarness(t)
	res, err := Run(context.Background(), h.ex, toy(t, h), opts)
	if err != nil || len(res) != 3 {
		t.Fatalf("err=%v results=%d", err, len(res))
	}
	lines := h.lines(t)
	if len(lines) != 3 {
		t.Fatalf("want 3 JSON lines, got %d:\n%s", len(lines), h.jsonl.String())
	}
	for i, name := range []string{"pre", "mid", "post"} {
		l := lines[i]
		if l["step"] != name || l["exit_class"] != "ok" || l["exit_code"] != float64(0) {
			t.Errorf("line %d = %v", i, l)
		}
		for _, k := range []string{"file", "start", "seconds", "counts"} {
			if _, ok := l[k]; !ok {
				t.Errorf("line %d lacks %q: %v", i, k, l)
			}
		}
	}
	counts := lines[0]["counts"].(map[string]any)
	if counts["records"] != float64(148425) || counts["note"] != "ok" || len(counts) != 2 {
		t.Errorf("counts = %v", counts)
	}
	// Only COUNT lines travel into the JSON: other output is for the person.
	if strings.Contains(h.jsonl.String(), "hunter2") {
		t.Error("the JSON log carried output that was not a COUNT line")
	}
	if !strings.Contains(h.text.String(), "token=hunter2") {
		t.Error("the step's own output is shown to the person")
	}
	if !h.exists("ran_pre") || !h.exists("ran_post") {
		t.Error("steps did not run")
	}
}

func TestRun_ParametersAndApplyAreExported(t *testing.T) {
	h, opts := newHarness(t)
	opts.Params = []Param{{"SNAP", "rdm-1"}, {"BUCKET", "sql.backups/x"}}
	s := step(t, "00_p.sh", "p", "nothing", `echo "COUNT snap=$SNAP"; echo "COUNT bucket=$BUCKET"; echo "COUNT apply=$APPLY"`)
	if _, err := Run(context.Background(), h.ex, []Step{s}, opts); err != nil {
		t.Fatal(err)
	}
	c := h.lines(t)[0]["counts"].(map[string]any)
	if c["snap"] != "rdm-1" || c["bucket"] != "sql.backups/x" || c["apply"] != "yes" {
		t.Errorf("counts = %v", c)
	}
}

func TestRun_AScriptWithQuotesAndHeredocsTravelsIntact(t *testing.T) {
	h, opts := newHarness(t)
	s := step(t, "00_q.sh", "q", "nothing", "cat <<'EOF'\nit's \"quoted\" $HOME `id`\nEOF\necho 'COUNT ok=1'")
	if _, err := Run(context.Background(), h.ex, []Step{s}, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.text.String(), "it's \"quoted\" $HOME `id`") {
		t.Errorf("the script was altered in transit:\n%s", h.text.String())
	}
}

func TestRun_StopsAtTheFirstFailureAndNamesTheStepToResumeFrom(t *testing.T) {
	h, opts := newHarness(t)
	os.WriteFile(filepath.Join(h.dir, "fail"), nil, 0o644)
	res, err := Run(context.Background(), h.ex, toy(t, h), opts)
	var se *StepError
	if !errors.As(err, &se) {
		t.Fatalf("want a *StepError, got %v", err)
	}
	if se.Resume != "mid" || se.Result.Step != "mid" || se.Result.ExitCode != 3 {
		t.Errorf("StepError = %+v", se)
	}
	if len(res) != 2 || h.exists("ran_post") {
		t.Errorf("the run must stop: %d results, post ran=%v", len(res), h.exists("ran_post"))
	}
	if n := len(h.lines(t)); n != 2 {
		t.Errorf("the failed step leaves its line too: %d lines", n)
	}
	if !strings.Contains(err.Error(), "--from mid") {
		t.Errorf("the message must say how to resume: %v", err)
	}
	// Fix it and resume: the first step is not run again.
	os.Remove(filepath.Join(h.dir, "fail"))
	os.Remove(filepath.Join(h.dir, "ran_pre"))
	opts.From = "mid"
	res, err = Run(context.Background(), h.ex, toy(t, h), opts)
	if err != nil || len(res) != 2 || h.exists("ran_pre") || !h.exists("ran_post") {
		t.Errorf("resume: err=%v results=%d pre=%v post=%v", err, len(res), h.exists("ran_pre"), h.exists("ran_post"))
	}
}

func TestRun_AnUnknownFromIsAUsageErrorBeforeAnyCall(t *testing.T) {
	h, opts := newHarness(t)
	opts.From = "nope"
	_, err := Run(context.Background(), h.ex, toy(t, h), opts)
	var ue *UsageError
	if !errors.As(err, &ue) || h.ex.calls() != 0 {
		t.Errorf("err=%v calls=%d", err, h.ex.calls())
	}
}

func TestRun_ExitClassesFollowTheWorkspaceTable(t *testing.T) {
	for code, class := range map[int]string{1: "negative", 2: "usage", 65: "data", 66: "no_input", 69: "unavailable", 70: "internal", 73: "cant_create", 74: "io", 75: "temp_fail", 77: "no_permission", 78: "config", 7: "internal", 126: "internal"} {
		h, opts := newHarness(t)
		s := step(t, "00_x.sh", "x", "nothing", fmt.Sprintf("exit %d", code))
		_, err := Run(context.Background(), h.ex, []Step{s}, opts)
		var se *StepError
		if !errors.As(err, &se) || se.Result.ExitClass != class || se.Result.ExitCode != code {
			t.Errorf("exit %d: want class %s, got %+v (%v)", code, class, se, err)
		}
	}
}

func TestRun_ATimedOutStepIsClassTimeout(t *testing.T) {
	h, opts := newHarness(t)
	s := step(t, "00_slow.sh", "slow", "nothing", "sleep 20", "# timeout: 1\n")
	start := time.Now()
	_, err := Run(context.Background(), h.ex, []Step{s}, opts)
	var se *StepError
	if !errors.As(err, &se) || se.Result.ExitClass != "timeout" {
		t.Fatalf("want a timeout StepError, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the timeout did not stop the wait")
	}
}

func TestRun_AnExecutorErrorIsAHostErrorNotAStepFailure(t *testing.T) {
	h, opts := newHarness(t)
	h.ex.canned = func(string) (string, executor.CommandStatus, error) { return "", "", errors.New("SSM unreachable") }
	_, err := Run(context.Background(), h.ex, toy(t, h), opts)
	var he *HostError
	var se *StepError
	if !errors.As(err, &he) || errors.As(err, &se) {
		t.Errorf("want a *HostError, got %v", err)
	}
}

// driverOf decodes the driver script a runner command writes onto the host.
func driverOf(t *testing.T, commands []string) string {
	t.Helper()
	all := strings.Join(commands, "\n")
	const open, shut = "<<'__CLASM_DRIVER__'\n", "\n__CLASM_DRIVER__"
	i := strings.Index(all, open)
	if i < 0 {
		t.Fatal("no driver in the commands")
	}
	rest := all[i+len(open):]
	enc := strings.ReplaceAll(rest[:strings.Index(rest, shut)], "\n", "")
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRun_UbuntuStepsRunAsUbuntu(t *testing.T) {
	h, opts := newHarness(t)
	h.ex.canned = func(string) (string, executor.CommandStatus, error) {
		return "\n" + countsMarker + "\n" + rcMarker + "0\n", executor.StatusSuccess, nil
	}
	s := step(t, "00_u.sh", "u", "nothing", "true", "# user: ubuntu\n")
	if _, err := Run(context.Background(), h.ex, []Step{s}, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(driverOf(t, h.ex.commands), "sudo -u ubuntu") {
		t.Error("an ubuntu step must be run through sudo -u ubuntu")
	}
	h2, opts2 := newHarness(t)
	h2.ex.canned = h.ex.canned
	r := step(t, "00_r.sh", "r", "nothing", "true")
	Run(context.Background(), h2.ex, []Step{r}, opts2)
	if strings.Contains(driverOf(t, h2.ex.commands), "sudo") {
		t.Error("a root step needs no sudo")
	}
}

func TestRun_ADetachedStepIsStartedPolledAndReported(t *testing.T) {
	h, opts := newHarness(t)
	s := step(t, "00_long.sh", "long", "nothing", "sleep 1\necho 'COUNT done=1'", "# detached: yes\n# timeout: 30\n")
	res, err := Run(context.Background(), h.ex, []Step{s}, opts)
	if err != nil || len(res) != 1 || res[0].ExitClass != "ok" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res[0].Counts["done"] != int64(1) && res[0].Counts["done"] != 1 {
		t.Errorf("counts = %v", res[0].Counts)
	}
	if h.ex.calls() < 3 {
		t.Errorf("a detached step is started, then polled at least once: %d calls", h.ex.calls())
	}
	if res[0].Seconds < 0.9 {
		t.Errorf("seconds = %v, the step took at least a second", res[0].Seconds)
	}
	if !strings.Contains(h.text.String(), "running") {
		t.Errorf("a poll reports progress:\n%s", h.text.String())
	}
}

func TestRun_ADetachedFailureAndTimeout(t *testing.T) {
	h, opts := newHarness(t)
	s := step(t, "00_f.sh", "f", "nothing", "echo partial\nexit 9", "# detached: yes\n")
	_, err := Run(context.Background(), h.ex, []Step{s}, opts)
	var se *StepError
	if !errors.As(err, &se) || se.Result.ExitCode != 9 || se.Result.ExitClass != "internal" {
		t.Errorf("failure: %v", err)
	}
	if !strings.Contains(h.text.String(), "partial") {
		t.Errorf("the failed step's output is shown:\n%s", h.text.String())
	}

	h, opts = newHarness(t)
	s = step(t, "00_t.sh", "t", "nothing", "sleep 5", "# detached: yes\n# timeout: 1\n")
	_, err = Run(context.Background(), h.ex, []Step{s}, opts)
	if !errors.As(err, &se) || se.Result.ExitClass != "timeout" {
		t.Errorf("timeout: %v", err)
	}
}

func gatedRecipe(t *testing.T, h *harness) []Step {
	return []Step{
		step(t, "00_pre.sh", "pre", "nothing", `touch "`+h.dir+`/ran_pre"`),
		step(t, "10_load.sh", "load", "database", `touch "`+h.dir+`/ran_load"`),
		step(t, "20_idx.sh", "idx", "indices", `touch "`+h.dir+`/ran_idx"`),
		step(t, "30_host.sh", "hostfix", "host", `touch "`+h.dir+`/ran_host"`),
	}
}

func TestRun_AProtectedHostRefusesDatabaseAndIndexStepsWithoutConfirmBeforeAnyStepRuns(t *testing.T) {
	h, opts := newHarness(t)
	opts.Protected = true
	_, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts)
	var ge *GateError
	if !errors.As(err, &ge) || ge.Step != "load" {
		t.Fatalf("want a *GateError for load, got %v", err)
	}
	if h.ex.calls() != 0 || h.exists("ran_pre") {
		t.Error("the gate refuses before anything runs, not part way")
	}
}

func TestRun_ConfirmMustBeTheHostIDOrName(t *testing.T) {
	for _, confirm := range []string{"i-0abc", "box"} {
		h, opts := newHarness(t)
		opts.Protected, opts.Confirm = true, []string{confirm}
		if _, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts); err != nil || !h.exists("ran_idx") {
			t.Errorf("--confirm %s: err=%v", confirm, err)
		}
	}
	h, opts := newHarness(t)
	opts.Protected, opts.Confirm = true, []string{"other-box"}
	_, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts)
	var ue *UsageError
	if !errors.As(err, &ue) || h.ex.calls() != 0 {
		t.Errorf("a wrong --confirm is a usage error before any call: %v, calls %d", err, h.ex.calls())
	}
}

func TestRun_TheGateOnlyLooksAtStepsThatWillRun(t *testing.T) {
	h, opts := newHarness(t)
	opts.Protected, opts.From = true, "hostfix"
	if _, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts); err != nil || !h.exists("ran_host") || h.exists("ran_load") {
		t.Errorf("resuming past the gated steps needs no confirm: %v", err)
	}
}

func TestRun_UnprotectedHostsAndReadOnlyStepsNeedNoConfirm(t *testing.T) {
	h, opts := newHarness(t)
	if _, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts); err != nil || !h.exists("ran_idx") {
		t.Errorf("unprotected: %v", err)
	}
	h, opts = newHarness(t)
	opts.Protected = true
	ro := []Step{step(t, "00_pre.sh", "pre", "nothing", `touch "`+h.dir+`/ran_pre"`), step(t, "10_h.sh", "h", "host", `touch "`+h.dir+`/ran_h"`)}
	if _, err := Run(context.Background(), h.ex, ro, opts); err != nil || !h.exists("ran_h") {
		t.Errorf("nothing and host are not gated: %v", err)
	}
}

func TestRun_ADryRunOnAProtectedHostSaysWhatWouldBeRefusedAndIsNotAnError(t *testing.T) {
	h, opts := newHarness(t)
	opts.Protected, opts.Apply = true, false
	if _, err := Run(context.Background(), h.ex, gatedRecipe(t, h), opts); err != nil || h.ex.calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, h.ex.calls())
	}
	if !strings.Contains(h.text.String(), "--confirm") {
		t.Errorf("the plan should say the gate would stop it:\n%s", h.text.String())
	}
}

// A command inside a step must not be able to swallow the runner's own script
// from standard input: the step is run with stdin closed.
func TestRun_TheStepsStandardInputIsClosed(t *testing.T) {
	h, opts := newHarness(t)
	h.ex.stdin = "SWALLOWED"
	s := step(t, "00_in.sh", "in", "nothing", `echo "COUNT stdin=$(cat | wc -c | tr -d ' ')"`)
	if _, err := Run(context.Background(), h.ex, []Step{s}, opts); err != nil {
		t.Fatal(err)
	}
	if got := h.lines(t)[0]["counts"].(map[string]any)["stdin"]; got != float64(0) {
		t.Errorf("the step read %v bytes from stdin", got)
	}
}

func TestRun_ADetachedStepToleratesTwoFailedPollsButNotThree(t *testing.T) {
	long := func() Step {
		return step(t, "00_long.sh", "long", "nothing", "sleep 1\necho 'COUNT done=1'", "# detached: yes\n# timeout: 30\n")
	}
	h, opts := newHarness(t)
	h.ex.failPoll = 2
	if res, err := Run(context.Background(), h.ex, []Step{long()}, opts); err != nil || len(res) != 1 || res[0].ExitClass != "ok" {
		t.Errorf("two failed polls are retried: %v", err)
	}
	h, opts = newHarness(t)
	h.ex.failPoll = 3
	_, err := Run(context.Background(), h.ex, []Step{long()}, opts)
	var he *HostError
	if !errors.As(err, &he) {
		t.Errorf("three in a row is a *HostError, got %v", err)
	}
}

func TestRun_TheRunIDDefaultsFromTheClock(t *testing.T) {
	h, opts := newHarness(t)
	opts.RunID = ""
	opts.Now = func() time.Time { return time.Date(2026, 11, 19, 10, 15, 0, 0, time.UTC) }
	s := step(t, "00_a.sh", "a", "nothing", "true")
	if _, err := Run(context.Background(), h.ex, []Step{s}, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(h.ex.commands, "\n"), "/20261119T101500Z/") {
		t.Error("the run directory should be named from the clock")
	}
}
