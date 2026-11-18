package workflow

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeExecutor is an Executor that is not SSM: it records the commands it is
// given and answers with canned output. A function that works through it does
// not depend on SSM, which is what a Multipass host needs (DR-0201).
type fakeExecutor struct {
	name     string
	stdout   string
	status   CommandStatus
	err      error
	commands []string
}

func (f *fakeExecutor) Run(ctx context.Context, command string, timeout, pollInterval time.Duration) (string, CommandStatus, error) {
	f.commands = append(f.commands, command)
	return f.stdout, f.status, f.err
}

func (f *fakeExecutor) Name() string { return f.name }

var _ Executor = (*fakeExecutor)(nil)

// sectionsStdout joins "@@ key" sections the way the on-host scripts print them.
func sectionsStdout(s map[string]string) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("@@ " + k + "\n" + s[k])
		if !strings.HasSuffix(s[k], "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestCheckRDMIndexHealth_RunsThroughAnyExecutor(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusSuccess, stdout: indexHealthStdout(healthySections())}
	r, err := CheckRDMIndexHealth(context.Background(), ex, "caltechauthors-rsdoiel-v14", 0, time.Millisecond)
	if err != nil {
		t.Fatalf("CheckRDMIndexHealth: %v", err)
	}
	if !r.Healthy {
		t.Errorf("want healthy, got %+v", r)
	}
	if r.InstanceID != "vm-7" || r.Instance != "caltechauthors-rsdoiel-v14" {
		t.Errorf("the id comes from the executor and the name from the caller: %+v", r)
	}
	if len(ex.commands) != 1 || !strings.Contains(ex.commands[0], "SEARCH_INDEX_PREFIX") {
		t.Errorf("want one health script, got %q", ex.commands)
	}
}

func TestCheckRDMIndexHealth_ExecutorErrorIsExit69NamingTheHost(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", err: errors.New("no route to host")}
	_, err := CheckRDMIndexHealth(context.Background(), ex, "x", 0, time.Millisecond)
	if exitCodeOf(err) != ExitUnavailable {
		t.Fatalf("err = %v, want exit 69", err)
	}
	if !strings.Contains(err.Error(), "vm-7") {
		t.Errorf("the error should name the host: %v", err)
	}
}

func TestInstallRDMIndexTemplates_RunsThroughAnyExecutor(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusSuccess, stdout: installStdout(installSections())}
	r, err := InstallRDMIndexTemplates(context.Background(), ex, "caltechauthors-rsdoiel-v14", 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.InstanceID != "vm-7" || r.Instance != "caltechauthors-rsdoiel-v14" {
		t.Errorf("report = %+v", r)
	}
	if len(ex.commands) != 1 || !strings.Contains(ex.commands[0], "_put_template") {
		t.Errorf("want one install script, got %q", ex.commands)
	}
}

func TestDiscoverLogConversion_RunsThroughAnyExecutor(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusSuccess, stdout: sectionsStdout(cvSections())}
	if _, err := DiscoverLogConversion(context.Background(), ex, 0, time.Millisecond); err != nil {
		t.Fatalf("DiscoverLogConversion: %v", err)
	}
	if len(ex.commands) != 1 {
		t.Errorf("want one discovery script, got %d commands", len(ex.commands))
	}
}

func TestNewIndexOps_SendsPlainCurlThroughAnyExecutor(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusSuccess, stdout: `{"acknowledged":true}`}
	ops := newIndexOps(ex, 0, 0)
	if err := ops.DeleteIndex(context.Background(), "clasm-fix-y"); err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}
	if len(ex.commands) != 1 || !strings.Contains(ex.commands[0], "clasm-fix-y") || !strings.Contains(ex.commands[0], "http://localhost:9200/") {
		t.Errorf("want one curl to localhost naming the index, got %q", ex.commands)
	}
}

func TestNewIndexOps_AFailedCommandIsAnErrorWithItsOutputAndTheHost(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusFailed, stdout: "curl: (7) Failed to connect"}
	err := newIndexOps(ex, 0, 0).DeleteIndex(context.Background(), "clasm-fix-y")
	if err == nil || !strings.Contains(err.Error(), "Failed to connect") || !strings.Contains(err.Error(), "vm-7") {
		t.Errorf("the output and the host belong in the error: %v", err)
	}
	ex = &fakeExecutor{name: "vm-7", err: errors.New("no route")}
	err = newIndexOps(ex, 0, 0).DeleteIndex(context.Background(), "clasm-fix-y")
	if exitCodeOf(err) != ExitUnavailable {
		t.Errorf("an unreachable host is 69: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "vm-7") {
		t.Errorf("the unreachable host should be named in the error: %v", err)
	}
}

func TestNewPostRestoreSteps_RunThroughAnyExecutor(t *testing.T) {
	ex := &fakeExecutor{name: "vm-7", status: StatusSuccess, stdout: indexHealthStdout(healthySections())}
	steps := newPostRestoreSteps(ex, "caltechauthors-rsdoiel-v14")
	r, err := steps.health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.InstanceID != "vm-7" || r.Instance != "caltechauthors-rsdoiel-v14" {
		t.Errorf("report = %+v", r)
	}
	if len(ex.commands) != 1 {
		t.Errorf("the health step should run one script through the executor, got %d", len(ex.commands))
	}
}

func (f *fakeExecutor) Put(ctx context.Context, local, remote string) error { return nil }
func (f *fakeExecutor) Get(ctx context.Context, remote, local string) error { return nil }
