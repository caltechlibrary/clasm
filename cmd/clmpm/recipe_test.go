package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/multipass"
)

// shHost is an Executor whose host is this machine; it runs the command text
// with sh -c, as the real executors' shells do.
type shHost struct {
	calls int
	err   error
}

func (h *shHost) Run(ctx context.Context, command string, timeout, _ time.Duration) (string, executor.CommandStatus, error) {
	h.calls++
	if h.err != nil {
		return "", "", h.err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), executor.StatusFailed, nil
		}
		return out.String(), "", err
	}
	return out.String(), executor.StatusSuccess, nil
}
func (*shHost) Put(context.Context, string, string) error { return nil }
func (*shHost) Get(context.Context, string, string) error { return nil }
func (*shHost) Name() string                              { return "scratch-vm" }

func recipeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rstep(name, changes, body string) string {
	return "#!/bin/bash\n# step: " + name + "\n# changes: " + changes + "\n" + body + "\n"
}

// runRecipe runs clmpm run-recipe against h, with the host's files under a temp dir.
func runRecipe(t *testing.T, h executor.Executor, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	d := deps{client: multipass.NewWithRunner(newMP().run), newExecutor: func(string) executor.Executor { return h }, recipeRemoteDir: filepath.Join(t.TempDir(), "remote")}
	code = run(append([]string{"run-recipe"}, args...), &o, &e, d)
	return code, o.String(), e.String()
}

func toyRecipe(t *testing.T) string {
	return recipeDir(t, map[string]string{
		"00_pre.sh":  rstep("pre", "nothing", "echo 'COUNT records=7'"),
		"10_post.sh": rstep("post", "nothing", "echo 'COUNT n=2'"),
		"README.md":  "notes",
	})
}

func TestRunRecipe_ADryRunPrintsThePlanAndTouchesNothing(t *testing.T) {
	h := &shHost{}
	code, out, _ := runRecipe(t, h, "scratch-vm", toyRecipe(t))
	if code != 0 || h.calls != 0 {
		t.Fatalf("code=%d calls=%d", code, h.calls)
	}
	for _, want := range []string{"pre", "post", "-apply", "README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
}

func TestRunRecipe_ApplyRunsTheStepsAndSaysSo(t *testing.T) {
	h := &shHost{}
	code, out, errOut := runRecipe(t, h, "-apply", "scratch-vm", toyRecipe(t))
	if code != 0 || h.calls == 0 {
		t.Fatalf("code=%d calls=%d stderr=%s", code, h.calls, errOut)
	}
	if !strings.Contains(out, "pre") || !strings.Contains(out, "post") || strings.Contains(out, `"exit_class"`) {
		t.Errorf("text mode prints progress, not JSON:\n%s", out)
	}
}

func TestRunRecipe_JSONModePutsOneLinePerStepOnStdoutAndProgressOnStderr(t *testing.T) {
	h := &shHost{}
	code, out, errOut := runRecipe(t, h, "-json", "-apply", "scratch-vm", toyRecipe(t))
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"step":"pre"`) || !strings.Contains(lines[0], `"records":7`) || !strings.Contains(lines[1], `"step":"post"`) {
		t.Errorf("stdout should be exactly the two step lines:\n%s", out)
	}
	if !strings.Contains(errOut, "[1/2] pre") {
		t.Errorf("progress belongs on stderr in -json mode:\n%s", errOut)
	}
}

func TestRunRecipe_EveryMultipassVMIsProtectedForDatabaseAndIndexSteps(t *testing.T) {
	dir := recipeDir(t, map[string]string{"00_load.sh": rstep("load", "database", "true")})
	h := &shHost{}
	code, _, errOut := runRecipe(t, h, "-apply", "scratch-vm", dir)
	if code != 1 || h.calls != 0 || !strings.Contains(errOut, "-confirm") {
		t.Errorf("without -confirm: code=%d calls=%d stderr=%s", code, h.calls, errOut)
	}
	if code, _, errOut := runRecipe(t, h, "-apply", "-confirm", "scratch-vm", dir); code != 0 {
		t.Errorf("with -confirm: code=%d stderr=%s", code, errOut)
	}
}

func TestRunRecipe_AFailedStepExitsWithItsClassAndNamesTheResumePoint(t *testing.T) {
	dir := recipeDir(t, map[string]string{"00_a.sh": rstep("a", "nothing", "true"), "10_b.sh": rstep("b", "nothing", "exit 65"), "20_c.sh": rstep("c", "nothing", "true")})
	h := &shHost{}
	code, _, errOut := runRecipe(t, h, "-apply", "scratch-vm", dir)
	if code != 65 || !strings.Contains(errOut, "-from b") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
	code, out, _ := runRecipe(t, h, "-apply", "-from", "c", "scratch-vm", dir)
	if code != 0 || strings.Contains(out, "] a ") {
		t.Errorf("resume from c: code=%d\n%s", code, out)
	}
}

func TestRunRecipe_ExitCodesForEachKindOfFault(t *testing.T) {
	good := toyRecipe(t)
	cases := []struct {
		name string
		host *shHost
		args []string
		want int
	}{
		{"no arguments", &shHost{}, nil, 2},
		{"only the VM", &shHost{}, []string{"scratch-vm"}, 2},
		{"bad parameter", &shHost{}, []string{"-apply", "scratch-vm", good, "lower=x"}, 2},
		{"unknown step to resume from", &shHost{}, []string{"-apply", "-from", "nope", "scratch-vm", good}, 2},
		{"missing directory", &shHost{}, []string{"scratch-vm", filepath.Join(t.TempDir(), "absent")}, 66},
		{"header-less script", &shHost{}, []string{"scratch-vm", recipeDir(t, map[string]string{"00_a.sh": "echo hi\n"})}, 65},
		{"host unreachable", &shHost{err: errors.New("multipass: not running")}, []string{"-apply", "scratch-vm", good}, 69},
	}
	for _, c := range cases {
		if code, _, errOut := runRecipe(t, c.host, c.args...); code != c.want {
			t.Errorf("%s: exit %d, want %d (%s)", c.name, code, c.want, errOut)
		}
	}
}

func TestRunRecipe_ParametersReachTheSteps(t *testing.T) {
	dir := recipeDir(t, map[string]string{"00_p.sh": rstep("p", "nothing", `echo "COUNT snap=$SNAP"`)})
	code, out, _ := runRecipe(t, &shHost{}, "-json", "-apply", "scratch-vm", dir, "SNAP=rdm-1")
	if code != 0 || !strings.Contains(out, `"snap":"rdm-1"`) {
		t.Errorf("code=%d\n%s", code, out)
	}
}
