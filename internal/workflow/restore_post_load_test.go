package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// R1 (DR-0196, DR-0199): after a restore is loaded and verified, Restore installs the templates, converts
// the log indices, and checks health. Its verdict is the restore's verdict. The steps' own behaviour is
// tested where they live; these tests are about their order, what stops what, and what the restore reports.

type stepLog struct{ calls []string }

func (l *stepLog) steps() postRestoreSteps {
	return postRestoreSteps{
		install: func(ctx context.Context) (IndexTemplateInstallReport, error) {
			l.calls = append(l.calls, "install")
			return IndexTemplateInstallReport{Installed: []string{"job-logs-v1.0.0"}, AlreadyPresent: []string{"auditlogs-datastream-auditlog-v1.0.0"}}, nil
		},
		discover: func(ctx context.Context) (conversionPlan, error) {
			l.calls = append(l.calls, "discover")
			return conversionPlan{Prefix: cvPrefix, Logs: []logConversion{auditConversion(), jobsConversion()}}, nil
		},
		convert: func(ctx context.Context, w io.Writer, plan conversionPlan) ([]logResult, error) {
			l.calls = append(l.calls, "convert")
			return []logResult{{Label: "audit log", Stream: cvStream, StreamDocs: 8, Copies: []string{"clasm-fix-auditlog-orphan"}}}, nil
		},
		health: func(ctx context.Context) (IndexHealthReport, error) {
			l.calls = append(l.calls, "health")
			return mustHealthyReport(), nil
		},
	}
}

func mustHealthyReport() IndexHealthReport {
	r, _ := buildIndexHealthReport(healthySections())
	r.Instance, r.InstanceID = "box", "i-1"
	return r
}

func boxInstance() inventory.Instance {
	return inventory.Instance{InstanceID: "i-1", Name: "box", Region: "us-west-2"}
}

func TestPostRestore_RunsInstallThenConversionThenHealth(t *testing.T) {
	var l stepLog
	var out bytes.Buffer
	if err := runPostRestoreRepair(context.Background(), &out, boxInstance(), l.steps()); err != nil {
		t.Fatalf("a healthy repair is success: %v", err)
	}
	if got := strings.Join(l.calls, ","); got != "install,discover,convert,health" {
		t.Errorf("order = %s, want install,discover,convert,health", got)
	}
	for _, want := range []string{"job-logs-v1.0.0", "audit log", "HEALTHY"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the restore reports what each step did; missing %q:\n%s", want, out.String())
		}
	}
}

func TestPostRestore_NothingToConvertSkipsTheConversionNotTheCheck(t *testing.T) {
	var l stepLog
	steps := l.steps()
	steps.discover = func(ctx context.Context) (conversionPlan, error) {
		l.calls = append(l.calls, "discover")
		return conversionPlan{Prefix: cvPrefix, Notes: []string{"audit log: already a data stream"}}, nil
	}
	var out bytes.Buffer
	if err := runPostRestoreRepair(context.Background(), &out, boxInstance(), steps); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.calls, ","); got != "install,discover,health" {
		t.Errorf("order = %s, want install,discover,health (no conversion)", got)
	}
}

func TestPostRestore_AnUnhealthyCheckIsTheRestoresVerdict(t *testing.T) {
	var l stepLog
	steps := l.steps()
	steps.health = func(ctx context.Context) (IndexHealthReport, error) {
		l.calls = append(l.calls, "health")
		r, _ := buildIndexHealthReport(with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))
		return r, nil
	}
	var out bytes.Buffer
	err := runPostRestoreRepair(context.Background(), &out, boxInstance(), steps)
	if err == nil || CLIExitCode(err) != 1 {
		t.Fatalf("err = %v, want exit 1: a restore whose health check fails is not a success", err)
	}
	if !strings.Contains(out.String(), "UNHEALTHY") || !strings.Contains(err.Error(), "rdm-index-health") {
		t.Errorf("the report is printed and the error says how to re-check:\n%s\n%v", out.String(), err)
	}
}

func TestPostRestore_AnUnreachableInstanceAfterTheLoadIsExit69(t *testing.T) {
	var l stepLog
	steps := l.steps()
	steps.health = func(ctx context.Context) (IndexHealthReport, error) {
		l.calls = append(l.calls, "health")
		return IndexHealthReport{}, &ExitCodeError{Code: ExitUnavailable, Err: errors.New("OpenSearch did not answer")}
	}
	err := runPostRestoreRepair(context.Background(), &bytes.Buffer{}, boxInstance(), steps)
	if CLIExitCode(err) != 69 {
		t.Errorf("err = %v (exit %d), want 69", err, CLIExitCode(err))
	}
}

func TestPostRestore_AFailedStepStopsTheLaterOnesAndKeepsItsClass(t *testing.T) {
	cases := []struct {
		name     string
		break_   func(s *postRestoreSteps, l *stepLog)
		wantCode int
		wantRan  string
		hint     string
	}{
		{"install cannot run the app", func(s *postRestoreSteps, l *stepLog) {
			s.install = func(ctx context.Context) (IndexTemplateInstallReport, error) {
				l.calls = append(l.calls, "install")
				return IndexTemplateInstallReport{}, &ExitCodeError{Code: ExitNoInput, Err: errors.New("uv was not found")}
			}
		}, 66, "install", "install-index-templates"},
		{"a template failed to install", func(s *postRestoreSteps, l *stepLog) {
			s.install = func(ctx context.Context) (IndexTemplateInstallReport, error) {
				l.calls = append(l.calls, "install")
				return IndexTemplateInstallReport{Failed: []TemplateInstallFailure{{Name: "job-logs-v1.0.0", Error: "boom"}}}, nil
			}
		}, 1, "install", "install-index-templates"},
		{"the conversion is refused", func(s *postRestoreSteps, l *stepLog) {
			s.discover = func(ctx context.Context) (conversionPlan, error) {
				l.calls = append(l.calls, "discover")
				return conversionPlan{}, errors.New("not converting: the copy name is taken")
			}
		}, 1, "install,discover", "convert-log-indices"},
		{"a conversion step fails", func(s *postRestoreSteps, l *stepLog) {
			s.convert = func(ctx context.Context, w io.Writer, plan conversionPlan) ([]logResult, error) {
				l.calls = append(l.calls, "convert")
				return nil, &conversionError{Label: "audit log", Step: "counting", Err: errors.New("x"), Copies: []string{"clasm-fix-auditlog-orphan"}}
			}
		}, 1, "install,discover,convert", "convert-log-indices"},
		{"the instance goes away during discovery", func(s *postRestoreSteps, l *stepLog) {
			s.discover = func(ctx context.Context) (conversionPlan, error) {
				l.calls = append(l.calls, "discover")
				return conversionPlan{}, &ExitCodeError{Code: ExitUnavailable, Err: errors.New("no answer")}
			}
		}, 69, "install,discover", "convert-log-indices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var l stepLog
			steps := l.steps()
			tc.break_(&steps, &l)
			err := runPostRestoreRepair(context.Background(), &bytes.Buffer{}, boxInstance(), steps)
			if err == nil || CLIExitCode(err) != tc.wantCode {
				t.Fatalf("err = %v (exit %d), want exit %d", err, CLIExitCode(err), tc.wantCode)
			}
			if got := strings.Join(l.calls, ","); got != tc.wantRan {
				t.Errorf("steps run = %s, want %s: a failure stops the later steps", got, tc.wantRan)
			}
			if !strings.Contains(err.Error(), "restore itself") || !strings.Contains(err.Error(), tc.hint) {
				t.Errorf("the error says the restore finished and how to repeat the step (%q): %v", tc.hint, err)
			}
		})
	}
}

func TestPostRestore_TheConversionIsNotGatedAgain(t *testing.T) {
	// DR-0199: one confirmation, taken before the restore, covers the conversion it triggers. The
	// repair has no confirmation to ask for: no input, no prompt, so a run with --confirm and no
	// terminal cannot stall on it.
	var l stepLog
	if err := runPostRestoreRepair(context.Background(), &bytes.Buffer{}, boxInstance(), l.steps()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(l.calls, ","), "convert") {
		t.Errorf("the conversion ran without a second confirmation: %v", l.calls)
	}
}

// ---- wired to the real steps ----

func TestNewPostRestoreSteps_AreTheRealOnes(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{
			{substring: "LIVE_TEMPLATES", stdout: installStdout(installSections()), status: types.CommandInvocationStatusSuccess},
			{substring: "sec templates_ds", stdout: cvDiscoveryStdout(cvHealthy), status: types.CommandInvocationStatusSuccess},
			{substring: "sec agg_audit", stdout: indexHealthStdout(healthySections()), status: types.CommandInvocationStatusSuccess},
		}}
	var out bytes.Buffer
	if err := runPostRestoreRepair(context.Background(), &out, boxInstance(), newPostRestoreSteps(ssm, boxInstance())); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	order := []string{"LIVE_TEMPLATES", "sec templates_ds", "sec agg_audit"}
	last := -1
	for _, frag := range order {
		idx := -1
		for i, c := range ssm.sentCommands {
			if strings.Contains(c, frag) {
				idx = i
				break
			}
		}
		if idx < 0 || idx <= last {
			t.Fatalf("want the install, then the discovery, then the health check, got %d for %q after %d; commands: %d", idx, frag, last, len(ssm.sentCommands))
		}
		last = idx
	}
	if len(ssm.sentCommands) != 3 {
		t.Errorf("a healthy restore needs exactly three more commands, got %d", len(ssm.sentCommands))
	}
	// Each step waits a bounded minute for OpenSearch, not zero (which fails on a cluster that has only just
	// finished a restore) and not a boot's five minutes (which would hide an outage).
	for i, c := range ssm.sentCommands {
		if !strings.Contains(c, "W=60\n") {
			t.Errorf("command %d must wait up to postRestoreWait (W=60):\n%s", i, c)
		}
	}
}

func TestNewPostRestoreSteps_WaitOnlyAShortWhile(t *testing.T) {
	// OpenSearch has just restored a snapshot, so it is up; a long wait would hide a real outage.
	if postRestoreWait > 5*60*1_000_000_000 {
		t.Errorf("postRestoreWait = %v is longer than a boot's wait", postRestoreWait)
	}
	if postRestoreWait <= 0 {
		t.Errorf("a wait of zero would fail on a restore that has only just finished")
	}
}

// noopInstallStdout is an install run that found both templates already present.
func noopInstallStdout() string {
	return installStdout(with(installSections(), map[string]string{
		"before": installBefore2, "after": installBefore2,
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nDONE\n"}))
}

// ---- through the real restore ----

func restoreWith(t *testing.T, ssm *fakeSSMClient, confirm string) (out string, err error) {
	t.Helper()
	buf := &bytes.Buffer{}
	s3 := &fakeS3Client{allObjects: snapshotObjects("caltechdata", 10, "rdm-20260819-160031")}
	err = RunRestoreOpenSearchSnapshotAuto(context.Background(), buf, ssm, s3, restoreCLIInst, restoreCLIParams(), confirmsOf(confirm), false, failingReader{t}, buf)
	return buf.String(), err
}

func firstCommandWithAll(sent []string, contains ...string) int {
outer:
	for i, c := range sent {
		for _, frag := range contains {
			if !strings.Contains(c, frag) {
				continue outer
			}
		}
		return i
	}
	return -1
}

func TestRestoreCLI_PostLoadStepsRunAfterTheLoadAndTheCleanupInOrder(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	out, err := restoreWith(t, ssm, "caltechdata")
	if err != nil {
		t.Fatalf("a restore onto a healthy instance succeeds: %v\n%s", err, out)
	}
	sent := ssm.sentCommands
	restore := firstCommandWithAll(sent, "/_restore")
	cleanup := firstCommandWithAll(sent, "DELETE", "_snapshot/rdm_backup_repo/rdm-20260819-160031")
	install := firstCommandWithAll(sent, "LIVE_TEMPLATES")
	discover := firstCommandWithAll(sent, "sec templates_ds")
	health := firstCommandWithAll(sent, "sec agg_audit")
	if restore < 0 || cleanup < 0 || !(restore < cleanup && cleanup < install && install < discover && discover < health) {
		t.Errorf("want restore < cleanup < install < discover < health, got %d %d %d %d %d", restore, cleanup, install, discover, health)
	}
	if !strings.Contains(out, "Restored OpenSearch snapshot") || !strings.Contains(out, "HEALTHY") {
		t.Errorf("the restore's own report and the health verdict are both shown:\n%s", out)
	}
}

func TestRestoreCLI_AnUnhealthyInstanceAfterTheLoadIsExit1ButTheRestoreIsStillReported(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	bad := indexHealthStdout(with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))
	ssm.responses = append([]ssmCommandResponse{{substring: "sec agg_audit", stdout: bad, status: types.CommandInvocationStatusSuccess}}, ssm.responses...)
	out, err := restoreWith(t, ssm, "caltechdata")
	if err == nil || CLIExitCode(err) != 1 {
		t.Fatalf("err = %v (exit %d), want exit 1: this restore used to exit 0 with the page broken", err, CLIExitCode(err))
	}
	if !strings.Contains(out, "Restored OpenSearch snapshot") || !strings.Contains(out, "Cleaned up the local snapshot repository") || !strings.Contains(out, "UNHEALTHY") {
		t.Errorf("the load and the cleanup happened and are reported, then the verdict:\n%s", out)
	}
}

func TestRestoreCLI_AnInstanceThatStopsAnsweringAfterTheLoadIsExit69(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	down := indexHealthStdout(with(healthySections(), map[string]string{"endpoint": "status=000\n"}))
	ssm.responses = append([]ssmCommandResponse{{substring: "sec agg_audit", stdout: down, status: types.CommandInvocationStatusSuccess}}, ssm.responses...)
	_, err := restoreWith(t, ssm, "caltechdata")
	if CLIExitCode(err) != 69 {
		t.Errorf("err = %v (exit %d), want 69", err, CLIExitCode(err))
	}
}

func TestRestoreCLI_AMissingTemplateIsInstalledNotLeftForTheOperator(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	ssm.responses = append([]ssmCommandResponse{{substring: "LIVE_TEMPLATES", stdout: installStdout(installSections()), status: types.CommandInvocationStatusSuccess}}, ssm.responses...)
	out, err := restoreWith(t, ssm, "caltechdata")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "job-logs-v1.0.0") {
		t.Errorf("the restore reports the template it installed:\n%s", out)
	}
}

func TestRestoreCLI_ADryRunNamesTheConversionAndStillOnlyReads(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 147456\n")
	out, err := restoreWith(t, ssm, "")
	if err != nil {
		t.Fatalf("a dry run is not an error: %v", err)
	}
	for _, want := range []string{"install any missing index templates", "convert any audit-log or job-logs index", "copy first, copies kept", "exits 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("DR-0199: the plan the one confirmation covers must name the conversion; missing %q:\n%s", want, out)
		}
	}
	for _, c := range ssm.sentCommands {
		if strings.Contains(c, "LIVE_TEMPLATES") || strings.Contains(c, "sec templates_ds") || strings.Contains(c, "sec agg_audit") {
			t.Errorf("a dry run must not run the post-load steps: %q", c)
		}
	}
}

func TestRestoreOpenSearch_TheInteractivePromptNamesTheConversionBeforeItAsksForConfirmation(t *testing.T) {
	ssm := restoreOpenSearchFake("caltechdata-rdmrecords-a\n", "a snapshot done\n", "caltechdata-rdmrecords-a yellow open 1\n")
	s3 := &fakeS3Client{allObjects: oneOpenSearchSnapshotObject("caltechdata", "rdm-20260819-160031")}
	inst := inventory.Instance{InstanceID: "i-1", Name: "caltechdata", Region: "us-east-1"}
	term, le, buf := newPipeEditor(conflictInput)
	err := restoreOpenSearchSnapshot(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssm}, s3, sameS3Client(s3), inst, nil, le, buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	out := buf.String()
	note := strings.Index(out, "convert any audit-log or job-logs index")
	prompt := strings.Index(out, "Enter identifier")
	if note < 0 || prompt < 0 || note > prompt {
		t.Errorf("the note naming the conversion comes before the type-to-confirm prompt (note=%d prompt=%d):\n%s", note, prompt, out)
	}
}
