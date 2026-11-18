package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/multipass"
)

const listJSON = `{"list": [
 {"name": "caltechauthors-rsdoiel-v14", "state": "Running", "ipv4": ["192.168.252.24"], "release": "Ubuntu 24.04 LTS"},
 {"name": "nginxtest", "state": "Stopped", "ipv4": [], "release": "Ubuntu 24.04 LTS"}]}`

const infoJSON = `{"errors": [], "info": {"caltechauthors-rsdoiel-v14": {"state": "Running", "release": "Ubuntu 24.04.5 LTS",
 "image_release": "24.04 LTS", "cpu_count": "4", "snapshot_count": "2", "ipv4": ["192.168.252.24"],
 "memory": {"used": 1000, "total": 8000}, "disks": {"sda1": {"used": "10", "total": "80"}}}}}`

// mp is a fake multipass: it records every call and answers by the first
// argument. A verb with no entry in errs succeeds with the canned stdout.
type mp struct {
	calls  []string
	stdout map[string]string
	errs   map[string]error
}

func (m *mp) run(ctx context.Context, args ...string) (string, error) {
	m.calls = append(m.calls, strings.Join(args, " "))
	if err := m.errs[args[0]]; err != nil {
		return "", err
	}
	return m.stdout[args[0]], nil
}

func (m *mp) called(prefix string) bool {
	for _, c := range m.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type scriptedExecutor struct {
	stdout string
	status executor.CommandStatus
}

func (s scriptedExecutor) Run(ctx context.Context, c string, t, p time.Duration) (string, executor.CommandStatus, error) {
	return s.stdout, s.status, nil
}
func (scriptedExecutor) Put(ctx context.Context, l, r string) error { return nil }
func (scriptedExecutor) Get(ctx context.Context, r, l string) error { return nil }
func (scriptedExecutor) Name() string                               { return "vm" }

// do runs the command with a fake multipass and returns its exit code and
// what it wrote.
func do(t *testing.T, m *mp, ex executor.Executor, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	d := deps{client: multipass.NewWithRunner(m.run), newExecutor: func(string) executor.Executor { return ex }}
	code = run(args, &o, &e, d)
	return code, o.String(), e.String()
}

func newMP() *mp {
	return &mp{stdout: map[string]string{"list": listJSON, "info": infoJSON}, errs: map[string]error{}}
}

func TestUsage_NoVerbAndUnknownVerbAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"frobnicate"}} {
		m := newMP()
		code, _, errOut := do(t, m, nil, args...)
		if code != 2 || !strings.Contains(errOut, "list-instances") {
			t.Errorf("args %v: code=%d err=%q", args, code, errOut)
		}
		if len(m.calls) != 0 {
			t.Errorf("args %v: multipass must not run, got %v", args, m.calls)
		}
	}
}

func TestHelpAndVersionExitZero(t *testing.T) {
	for _, arg := range []string{"-help", "-version", "-license"} {
		code, out, _ := do(t, newMP(), nil, arg)
		if code != 0 || strings.TrimSpace(out) == "" {
			t.Errorf("%s: code=%d out=%q", arg, code, out)
		}
	}
}

func TestListInstances_TextShowsNameStateAndAddress(t *testing.T) {
	code, out, _ := do(t, newMP(), nil, "list-instances")
	if code != 0 || !strings.Contains(out, "caltechauthors-rsdoiel-v14") || !strings.Contains(out, "Running") || !strings.Contains(out, "192.168.252.24") || !strings.Contains(out, "nginxtest") {
		t.Errorf("code=%d out=%q", code, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("plain text must carry no terminal codes: %q", out)
	}
}

func TestListInstances_JSONIsAnArray(t *testing.T) {
	code, out, _ := do(t, newMP(), nil, "list-instances", "-json")
	var got []multipass.Instance
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 2 || got[0].Name != "caltechauthors-rsdoiel-v14" {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestListInstances_NoneIsAnEmptyArrayAndExitZero(t *testing.T) {
	m := newMP()
	m.stdout["list"] = `{"list": []}`
	code, out, _ := do(t, m, nil, "list-instances", "-json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestShowInstanceDetail_TextAndJSON(t *testing.T) {
	code, out, _ := do(t, newMP(), nil, "show-instance-detail", "caltechauthors-rsdoiel-v14")
	if code != 0 || !strings.Contains(out, "Running") || !strings.Contains(out, "4") || !strings.Contains(out, "snapshots") {
		t.Errorf("code=%d out=%q", code, out)
	}
	code, out, _ = do(t, newMP(), nil, "show-instance-detail", "-json", "caltechauthors-rsdoiel-v14")
	var d multipass.Detail
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil || d.CPUs != 4 || d.MemoryTotal != 8000 || d.Snapshots != 2 {
		t.Errorf("code=%d out=%q d=%+v", code, out, d)
	}
}

func TestShowInstanceDetail_MissingInstanceIsExit1NamingIt(t *testing.T) {
	m := newMP()
	m.errs["info"] = errors.New(`instance "nope" does not exist`)
	code, _, errOut := do(t, m, nil, "show-instance-detail", "nope")
	if code != 1 || !strings.Contains(errOut, "nope") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestArity_MissingAndSurplusArgumentsAreUsageErrors(t *testing.T) {
	cases := [][]string{
		{"show-instance-detail"},
		{"show-instance-detail", "a", "b"},
		{"list-instances", "surplus"},
		{"start-instance"},
		{"stop-instance", "a", "b"},
		{"clone-instance", "only-one"},
		{"snapshot-instance", "vm"},
		{"restore-snapshot", "vm"},
		{"delete-instance"},
		{"wait-for-cloud-init"},
		{"launch-instance-from-cloud-init", "vm"},
	}
	for _, args := range cases {
		m := newMP()
		code, _, _ := do(t, m, nil, args...)
		if code != 2 || len(m.calls) != 0 {
			t.Errorf("%v: code=%d calls=%v", args, code, m.calls)
		}
	}
}

func TestBadFlagIsAUsageError(t *testing.T) {
	code, _, _ := do(t, newMP(), nil, "list-instances", "-nonsense")
	if code != 2 {
		t.Errorf("code = %d", code)
	}
}

func TestLaunch_PassesTheOptionsThrough(t *testing.T) {
	ci := filepath.Join(t.TempDir(), "cloud-init-multipass.yaml")
	if err := os.WriteFile(ci, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newMP()
	code, _, _ := do(t, m, nil, "launch-instance-from-cloud-init", "-cpus", "4", "-memory", "8G", "-disk", "80G", "-image", "24.04", "caltechauthors-rsdoiel-v14", ci)
	want := "launch 24.04 --name caltechauthors-rsdoiel-v14 --cloud-init " + ci + " --cpus 4 --memory 8G --disk 80G"
	if code != 0 || !m.called(want) {
		t.Errorf("code=%d calls=%v", code, m.calls)
	}
}

func TestLaunch_AMissingCloudInitFileIsExit66(t *testing.T) {
	m := newMP()
	code, _, errOut := do(t, m, nil, "launch-instance-from-cloud-init", "vm", "/no/such/file.yaml")
	if code != 66 || !strings.Contains(errOut, "/no/such/file.yaml") || len(m.calls) != 0 {
		t.Errorf("code=%d err=%q calls=%v", code, errOut, m.calls)
	}
}

func TestStartStopCloneSnapshot_RunTheRightMultipassCalls(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"start-instance", "vm1"}, "start vm1"},
		{[]string{"stop-instance", "vm1"}, "stop vm1"},
		{[]string{"clone-instance", "vm1", "vm2"}, "clone vm1 --name vm2"},
		{[]string{"snapshot-instance", "vm1", "before-load"}, "snapshot vm1 --name before-load"},
		{[]string{"snapshot-instance", "-comment", "proven v13", "vm1", "s"}, "snapshot vm1 --name s --comment proven v13"},
	}
	for _, c := range cases {
		m := newMP()
		code, out, _ := do(t, m, nil, c.args...)
		if code != 0 || !m.called(c.want) || strings.TrimSpace(out) == "" {
			t.Errorf("%v: code=%d calls=%v out=%q", c.args, code, m.calls, out)
		}
	}
}

func TestActionVerbs_JSONResultNamesTheActionAndInstance(t *testing.T) {
	code, out, _ := do(t, newMP(), nil, "start-instance", "-json", "vm1")
	var got map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got["instance"] != "vm1" || got["action"] != "start-instance" {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestSnapshotOfARunningInstanceIsExit1WithMultipassMessage(t *testing.T) {
	m := newMP()
	m.errs["snapshot"] = errors.New("snapshot failed: Multipass can only take snapshots of stopped instances.")
	code, _, errOut := do(t, m, nil, "snapshot-instance", "vm1", "s")
	if code != 1 || !strings.Contains(errOut, "stopped instances") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestDelete_IsADryRunUnlessConfirmed(t *testing.T) {
	m := newMP()
	code, out, _ := do(t, m, nil, "delete-instance", "caltechauthors-rsdoiel-v14")
	if code != 0 || m.called("delete") || !strings.Contains(strings.ToLower(out), "dry run") || !strings.Contains(out, "-confirm") {
		t.Errorf("code=%d calls=%v out=%q", code, m.calls, out)
	}
	if !m.called("info caltechauthors-rsdoiel-v14") {
		t.Errorf("the dry run should look the instance up so a typo is caught: %v", m.calls)
	}
}

func TestDelete_ADryRunOfAMissingInstanceIsExit1(t *testing.T) {
	m := newMP()
	m.errs["info"] = errors.New(`instance "nope" does not exist`)
	code, _, _ := do(t, m, nil, "delete-instance", "nope")
	if code != 1 || m.called("delete") {
		t.Errorf("code=%d calls=%v", code, m.calls)
	}
}

func TestDelete_ConfirmedRunsAndPurgeIsSeparate(t *testing.T) {
	m := newMP()
	if code, _, _ := do(t, m, nil, "delete-instance", "-confirm", "vm1"); code != 0 || !m.called("delete vm1") || m.called("delete --purge") {
		t.Errorf("confirm only: code=%d calls=%v", code, m.calls)
	}
	m = newMP()
	if code, _, _ := do(t, m, nil, "delete-instance", "-confirm", "-purge", "vm1"); code != 0 || !m.called("delete --purge vm1") {
		t.Errorf("confirm and purge: code=%d calls=%v", code, m.calls)
	}
}

func TestRestoreSnapshot_IsADryRunUnlessConfirmed(t *testing.T) {
	m := newMP()
	code, out, _ := do(t, m, nil, "restore-snapshot", "vm1", "before-load")
	if code != 0 || m.called("restore") || !strings.Contains(strings.ToLower(out), "dry run") || !strings.Contains(out, "-confirm") {
		t.Errorf("code=%d calls=%v out=%q", code, m.calls, out)
	}
	m = newMP()
	if code, _, _ := do(t, m, nil, "restore-snapshot", "-confirm", "vm1", "before-load"); code != 0 || !m.called("restore --destructive vm1.before-load") {
		t.Errorf("confirmed: code=%d calls=%v", code, m.calls)
	}
}

func TestMultipassNotInstalledIsExit69(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var o, e bytes.Buffer
	code := run([]string{"list-instances"}, &o, &e, deps{client: multipass.New(), newExecutor: func(string) executor.Executor { return nil }})
	if code != 69 || !strings.Contains(e.String(), "multipass") {
		t.Errorf("code=%d err=%q", code, e.String())
	}
}

func TestErrorsInJSONModeCarryTheClassAndCode(t *testing.T) {
	m := newMP()
	m.errs["info"] = errors.New(`instance "nope" does not exist`)
	code, _, errOut := do(t, m, nil, "show-instance-detail", "-json", "nope")
	var got struct {
		Error struct {
			Code    int    `json:"code"`
			Class   string `json:"class"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if code != 1 || json.Unmarshal([]byte(errOut), &got) != nil || got.Error.Code != 1 || got.Error.Class != "negative" || !strings.Contains(got.Error.Message, "nope") {
		t.Errorf("code=%d err=%q got=%+v", code, errOut, got)
	}
}

func TestWaitForCloudInit_DoneIsExit0(t *testing.T) {
	ex := scriptedExecutor{stdout: `{"extended_status": "done", "errors": []}`, status: executor.StatusSuccess}
	code, out, _ := do(t, newMP(), ex, "wait-for-cloud-init", "vm1")
	if code != 0 || !strings.Contains(out, "done") {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestWaitForCloudInit_ErrorIsExit1AndTimeoutIs75(t *testing.T) {
	bad := scriptedExecutor{stdout: `{"extended_status": "error", "errors": ["scripts-user failed"]}`, status: executor.StatusFailed}
	code, _, errOut := do(t, newMP(), bad, "wait-for-cloud-init", "vm1")
	if code != 1 || !strings.Contains(errOut, "scripts-user failed") {
		t.Errorf("error: code=%d err=%q", code, errOut)
	}
	slow := scriptedExecutor{stdout: `{"extended_status": "running", "errors": []}`, status: executor.StatusSuccess}
	code, _, _ = do(t, newMP(), slow, "wait-for-cloud-init", "-wait", "30ms", "-poll", "5ms", "vm1")
	if code != 75 {
		t.Errorf("timeout: code=%d, want 75", code)
	}
}

func TestWaitForCloudInit_ABadDurationIsAUsageError(t *testing.T) {
	code, _, _ := do(t, newMP(), nil, "wait-for-cloud-init", "-wait", "soon", "vm1")
	if code != 2 {
		t.Errorf("code = %d", code)
	}
}

// The RDM cloud-init files target Ubuntu 24.04, the AWS image. Multipass's own
// default moves to the newest LTS (a real launch gave 26.04), so clmpm pins it.
func TestLaunch_TheImageDefaultsToUbuntu2404(t *testing.T) {
	ci := filepath.Join(t.TempDir(), "ci.yaml")
	if err := os.WriteFile(ci, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newMP()
	if code, _, _ := do(t, m, nil, "launch-instance-from-cloud-init", "vm", ci); code != 0 || !m.called("launch 24.04 --name vm ") {
		t.Errorf("code=%d calls=%v", code, m.calls)
	}
}
