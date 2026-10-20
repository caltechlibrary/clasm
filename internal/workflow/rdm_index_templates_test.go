package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// The install is the app's own routine (DR-0198): clasm carries no template JSON. These tests
// pin what clasm itself decides -- which templates are missing is the app's script's job, but
// what the script may do, how its output is judged, and the exit classes are clasm's.

const (
	installBefore1 = `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"}]}`
	installBefore2 = `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"},{"name":"caltechauthors-job-logs-v1.0.0"}]}`
	installBefore0 = `{}`
)

// installSections is a run that found job-logs-v1.0.0 missing and installed it.
func installSections() map[string]string {
	return map[string]string{
		"prefix":   "cfg=/Sites/caltechauthors/invenio.cfg\nprefix=caltechauthors-\n",
		"endpoint": "status=200\n",
		"before":   installBefore1,
		"app":      "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nINSTALL job-logs-v1.0.0\nDONE\n",
		"after":    installBefore2,
	}
}

func mustInstallReport(t *testing.T, s map[string]string) IndexTemplateInstallReport {
	t.Helper()
	r, err := buildInstallReport(s)
	if err != nil {
		t.Fatalf("buildInstallReport: %v", err)
	}
	return r
}

func exitCodeOf(err error) int {
	var coded *ExitCodeError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return -1
}

func TestInstallReport_InstallsTheMissingOne(t *testing.T) {
	r := mustInstallReport(t, installSections())
	if !slices.Equal(r.Installed, []string{"job-logs-v1.0.0"}) {
		t.Errorf("Installed = %v, want [job-logs-v1.0.0]", r.Installed)
	}
	if !slices.Equal(r.AlreadyPresent, []string{"auditlogs-datastream-auditlog-v1.0.0"}) {
		t.Errorf("AlreadyPresent = %v, want the audit-log template", r.AlreadyPresent)
	}
	if len(r.Expected) != 2 || len(r.Before) != 1 || len(r.After) != 2 || len(r.Failed) != 0 {
		t.Errorf("report = %+v", r)
	}
	if r.Prefix != "caltechauthors-" {
		t.Errorf("Prefix = %q", r.Prefix)
	}
}

func TestInstallReport_NothingMissingInstallsNothing(t *testing.T) {
	s := with(installSections(), map[string]string{
		"before": installBefore2, "after": installBefore2,
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nDONE\n"})
	r := mustInstallReport(t, s)
	if len(r.Installed) != 0 || len(r.AlreadyPresent) != 2 || len(r.Failed) != 0 {
		t.Errorf("a run with nothing missing installs nothing: %+v", r)
	}
}

func TestInstallReport_EveryTemplateGone(t *testing.T) {
	s := with(installSections(), map[string]string{
		"before": installBefore0,
		"app":    "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nINSTALL auditlogs-datastream-auditlog-v1.0.0\nINSTALL job-logs-v1.0.0\nDONE\n"})
	r := mustInstallReport(t, s)
	if len(r.Installed) != 2 || len(r.AlreadyPresent) != 0 {
		t.Errorf("after a restore with include_global_state:false every template is installed: %+v", r)
	}
}

func TestInstallReport_NeverOverwrites(t *testing.T) {
	// The routine must install only what is missing. If its output says it installed a template
	// the instance already had, that is an overwrite and an internal error (70), not a success.
	s := with(installSections(), map[string]string{
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nINSTALL auditlogs-datastream-auditlog-v1.0.0\nDONE\n"})
	_, err := buildInstallReport(s)
	if exitCodeOf(err) != ExitInternal || !strings.Contains(err.Error(), "already") {
		t.Errorf("err = %v, want an ExitCodeError 70 saying the template was already present", err)
	}
}

func TestInstallReport_AnInstallThatDidNotTakeFails(t *testing.T) {
	s := with(installSections(), map[string]string{"after": installBefore1})
	_, err := buildInstallReport(s)
	if err == nil || exitCodeOf(err) != -1 || !strings.Contains(err.Error(), "job-logs-v1.0.0") {
		t.Errorf("err = %v, want a plain error (exit 1) naming the template the install did not leave behind", err)
	}
}

func TestInstallReport_FailuresAreReportedWithWhatWorked(t *testing.T) {
	s := with(installSections(), map[string]string{
		"before": installBefore0, "after": `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"}]}`,
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nINSTALL auditlogs-datastream-auditlog-v1.0.0\nFAILED job-logs-v1.0.0 RequestError(400, 'illegal_argument_exception')\nDONE\n"})
	r := mustInstallReport(t, s)
	if !slices.Equal(r.Installed, []string{"auditlogs-datastream-auditlog-v1.0.0"}) {
		t.Errorf("Installed = %v, want the one that worked", r.Installed)
	}
	if len(r.Failed) != 1 || r.Failed[0].Name != "job-logs-v1.0.0" || !strings.Contains(r.Failed[0].Error, "illegal_argument_exception") {
		t.Errorf("Failed = %+v, want job-logs-v1.0.0 with the app's error", r.Failed)
	}
}

func TestInstallReport_AppProblemsAreClassified(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		want      int
	}{
		{"no uv", map[string]string{"app": "status=no_uv\n"}, ExitNoInput},
		{"no .env", map[string]string{"app": "status=no_env\n"}, ExitNoInput},
		{"routine crashed", map[string]string{"app": "status=failed\nOUT Traceback (most recent call last):\nOUT ImportError: no module\n"}, ExitInternal},
		{"routine listed nothing", map[string]string{"app": "status=ok\nDONE\n"}, ExitInternal},
		{"routine never finished", map[string]string{"app": "status=ok\nEXPECTED job-logs-v1.0.0\n"}, ExitInternal},
		{"no app at all", map[string]string{"prefix": "cfg=\nprefix=\n"}, ExitNoInput},
		{"endpoint down", map[string]string{"endpoint": "status=000\n"}, ExitUnavailable},
		{"wants credentials", map[string]string{"endpoint": "status=401\n"}, ExitUnavailable},
		{"unreadable listing", map[string]string{"before": `{"index_templates":[`}, ExitData},
		{"missing section", map[string]string{"after": ""}, ExitData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildInstallReport(with(installSections(), tc.overrides))
			if exitCodeOf(err) != tc.want {
				t.Errorf("err = %v (code %d), want exit %d", err, exitCodeOf(err), tc.want)
			}
		})
	}
	// A failed routine shows what it printed, so the operator can see why.
	_, err := buildInstallReport(with(installSections(), map[string]string{"app": "status=failed\nOUT ImportError: no module\n"}))
	if err == nil || !strings.Contains(err.Error(), "ImportError") {
		t.Errorf("a crashed routine's own output belongs in the error, got %v", err)
	}
}

// ---- the command ----

func TestInstallCommand_RunsTheAppsOwnRoutineAndCarriesNoTemplate(t *testing.T) {
	cmd := buildInstallIndexTemplatesCommand(0)
	for _, want := range []string{"sudo -u ubuntu -H", "invenio shell", "/home/ubuntu/.local/bin/uv", "_put_template", "current_search.index_templates", "LIVE_TEMPLATES", "SEARCH_INDEX_PREFIX", `cd '$APP'`} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the script must contain %q", want)
		}
	}
	for _, banned := range []string{"index_patterns", "mappings", "composed_of", `"settings"`, `"priority"`, "data_stream\":", "-d @", "--data-binary"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("clasm carries no template JSON (it goes stale and applies the wrong prefix); found %q", banned)
		}
	}
	// Only the app writes. clasm's own curl never changes anything.
	if regexp.MustCompile(`curl[^\n]*-X (PUT|POST|DELETE|PATCH)`).MatchString(cmd) {
		t.Errorf("the script's curl calls must be reads: the app does the install")
	}
}

func TestInstallCommand_InstallsOnlyWhatIsMissing(t *testing.T) {
	cmd := buildInstallIndexTemplatesCommand(0)
	if !strings.Contains(cmd, "n in live or prefix + n in live") {
		t.Errorf("the routine must skip a template the instance already has, with or without the prefix")
	}
	if strings.Count(cmd, "_put_template") != 1 {
		t.Errorf("one install call, guarded by the missing check; got %d", strings.Count(cmd, "_put_template"))
	}
	if strings.Index(cmd, "continue") > strings.Index(cmd, "_put_template") || !strings.Contains(cmd, "continue") {
		t.Errorf("the skip for a present template must come before the install")
	}
}

func TestInstallCommand_ListsBeforeAndAfter(t *testing.T) {
	cmd := buildInstallIndexTemplatesCommand(0)
	b, a, p := strings.Index(cmd, "sec before"), strings.Index(cmd, "sec after"), strings.Index(cmd, "_put_template")
	if b < 0 || a < 0 || !(b < p && p < a) {
		t.Errorf("the template list is read before the install and again after it (before=%d install=%d after=%d)", b, p, a)
	}
}

func TestInstallCommand_StopsWithoutWritingWhenTheAppCannotBeRun(t *testing.T) {
	cmd := buildInstallIndexTemplatesCommand(0)
	uv, env, put := strings.Index(cmd, "status=no_uv"), strings.Index(cmd, "status=no_env"), strings.Index(cmd, "_put_template")
	if uv < 0 || env < 0 || uv > put || env > put {
		t.Errorf("a missing uv or .env must be detected before anything is run (no_uv=%d no_env=%d install=%d)", uv, env, put)
	}
}

func TestInstallCommand_ShellHygiene(t *testing.T) {
	cmd := buildInstallIndexTemplatesCommand(5 * time.Minute)
	if wildcardRE.MatchString(cmd) {
		t.Errorf("no wildcard anywhere: %q", wildcardRE.FindString(cmd))
	}
	for _, bash := range []string{"[[", "<<<", "declare ", "mapfile", "${!"} {
		if strings.Contains(cmd, bash) {
			t.Errorf("SSM runs this under /bin/sh; found %q", bash)
		}
	}
	if regexp.MustCompile(`curl[^\n]* -u `).MatchString(cmd) || strings.Contains(cmd, "PASSWORD") {
		t.Errorf("no credentials")
	}
	if !strings.Contains(cmd, "W=300") || !strings.Contains(cmd, "sleep") {
		t.Errorf("a five-minute wait polls with a 300-second bound")
	}
	if strings.Contains(buildInstallIndexTemplatesCommand(0), "sleep") {
		t.Errorf("a wait of zero does not poll")
	}
}

// ---- running it ----

func installStdout(s map[string]string) string {
	var b strings.Builder
	for _, k := range []string{"prefix", "endpoint", "before", "app", "after"} {
		if v, ok := s[k]; ok {
			b.WriteString("@@ " + k + "\n" + v)
			if !strings.HasSuffix(v, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func installFake(s map[string]string) *fakeSSMClient {
	return &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: installStdout(s)}
}

func TestInstallRDMIndexTemplates_OneCommandAndTheInstanceNamed(t *testing.T) {
	ssm := installFake(installSections())
	r, err := InstallRDMIndexTemplates(context.Background(), ssm, healthInstance(), 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if ssm.sendCommandCallCount != 1 || !strings.Contains(ssm.lastCommandText, "_put_template") {
		t.Errorf("want one SSM call carrying the install script, got %d", ssm.sendCommandCallCount)
	}
	if r.InstanceID != "i-057d8cc298da8f186" || r.Instance != "caltechauthors-rsdoiel-v13b" {
		t.Errorf("report = %+v", r)
	}
}

func TestInstallRDMIndexTemplates_TheTimeoutCoversTheWaitAndTheApp(t *testing.T) {
	ssm := installFake(installSections())
	if _, err := InstallRDMIndexTemplates(context.Background(), ssm, healthInstance(), 5*time.Minute, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if ssm.lastTimeoutSeconds == nil || *ssm.lastTimeoutSeconds <= 300+180 {
		t.Errorf("SSM must be given more than the wait plus the app's 180 seconds, got %v", ssm.lastTimeoutSeconds)
	}
}

func TestInstallRDMIndexTemplates_SSMFailureClasses(t *testing.T) {
	_, err := InstallRDMIndexTemplates(context.Background(), &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}, healthInstance(), 0, time.Millisecond)
	if exitCodeOf(err) != ExitUnavailable {
		t.Errorf("an unreachable instance is 69, got %v", err)
	}
	_, err = InstallRDMIndexTemplates(context.Background(), &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusFailed, stdout: "sh: syntax error"}, healthInstance(), 0, time.Millisecond)
	if exitCodeOf(err) != ExitInternal {
		t.Errorf("a script that failed is 70, got %v", err)
	}
}

// ---- output ----

func TestWriteIndexTemplateInstall_Text(t *testing.T) {
	var did, none bytes.Buffer
	if err := WriteIndexTemplateInstall(&did, mustInstallReport(t, installSections()), ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(did.String(), "Installed 1") || !strings.Contains(did.String(), "job-logs-v1.0.0") {
		t.Errorf("the output must name what it installed:\n%s", did.String())
	}
	s := with(installSections(), map[string]string{"before": installBefore2, "after": installBefore2,
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nDONE\n"})
	if err := WriteIndexTemplateInstall(&none, mustInstallReport(t, s), ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none.String(), "Nothing to install") || strings.Contains(none.String(), "Installed") {
		t.Errorf("with nothing missing it says so:\n%s", none.String())
	}
}

func TestWriteIndexTemplateInstall_JSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteIndexTemplateInstall(&buf, mustInstallReport(t, installSections()), ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON document: %v", err)
	}
	if inst, _ := got["installed"].([]any); len(inst) != 1 {
		t.Errorf("installed = %v", got["installed"])
	}
	if f, ok := got["failed"].([]any); !ok || len(f) != 0 {
		t.Errorf("failed must be an empty array, got %v", got["failed"])
	}
}

// ---- the form ----

func TestParseInstallIndexTemplatesArgs(t *testing.T) {
	inst, format, wait, err := ParseInstallIndexTemplatesArgs([]string{"box"})
	if err != nil || inst != "box" || format != ui.FormatText || wait != DefaultIndexHealthWait {
		t.Errorf("defaults: %q %v %v %v", inst, format, wait, err)
	}
	if _, f, w, err := ParseInstallIndexTemplatesArgs([]string{"-json", "-wait", "0", "box"}); err != nil || f != ui.FormatJSON || w != 0 {
		t.Errorf("-json -wait 0: %v %v %v", f, w, err)
	}
	for name, args := range map[string][]string{
		"no instance": {}, "surplus word": {"box", "other"}, "unknown option": {"-bogus", "box"},
		"two formats": {"-text", "-json", "box"}, "bad wait": {"-wait", "soon", "box"}, "negative wait": {"-wait", "-1s", "box"},
		"blank instance": {" "}, "no gate, so no --confirm": {"--confirm", "box", "box"},
	} {
		_, _, _, err := ParseInstallIndexTemplatesArgs(args)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a *UsageError", name, err)
		}
	}
	_, _, _, err = ParseInstallIndexTemplatesArgs([]string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, InstallIndexTemplatesCLISlug) || !strings.Contains(help.Usage, "Exit status") {
		t.Errorf("--help: %v", err)
	}
}

func TestRunInstallIndexTemplatesCLI_ExitCodePerClass(t *testing.T) {
	good := installSections()
	failed := with(good, map[string]string{
		"before": installBefore0, "after": `{}`,
		"app": "status=ok\nEXPECTED job-logs-v1.0.0\nFAILED job-logs-v1.0.0 RequestError(400, 'x')\nDONE\n"})
	cases := []struct {
		name string
		ssm  *fakeSSMClient
		arg  string
		want int
	}{
		{"installed", installFake(good), "box", 0},
		{"nothing to do", installFake(with(good, map[string]string{"before": installBefore2, "after": installBefore2, "app": "status=ok\nEXPECTED job-logs-v1.0.0\nDONE\n"})), "box", 0},
		{"a template failed", installFake(failed), "box", 1},
		{"did not take", installFake(with(good, map[string]string{"after": installBefore1})), "box", 1},
		{"endpoint down", installFake(with(good, map[string]string{"endpoint": "status=000\n"})), "box", 69},
		{"ssm unreachable", &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}, "box", 69},
		{"no app", installFake(with(good, map[string]string{"prefix": "cfg=\nprefix=\n"})), "box", 66},
		{"no uv", installFake(with(good, map[string]string{"app": "status=no_uv\n"})), "box", 66},
		{"unreadable", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: "garbage"}, "box", 65},
		{"overwrite attempted", installFake(with(good, map[string]string{"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nINSTALL auditlogs-datastream-auditlog-v1.0.0\nDONE\n"})), "box", 70},
		{"script failed", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusFailed}, "box", 70},
		{"unknown instance", &fakeSSMClient{commandID: "c"}, "nope", 2},
	}
	instances := healthInstances()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := RunInstallIndexTemplatesCLI(context.Background(), &bytes.Buffer{}, healthClients(tc.ssm), instances, tc.arg, ui.FormatText, 0, time.Millisecond)
			if got := CLIExitCode(err); got != tc.want {
				t.Errorf("exit code = %d (err %v), want %d", got, err, tc.want)
			}
		})
	}
}

func TestRunInstallIndexTemplatesCLI_AFailedTemplateStillPrintsWhatWorked(t *testing.T) {
	s := with(installSections(), map[string]string{
		"before": installBefore0, "after": `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"}]}`,
		"app": "status=ok\nEXPECTED auditlogs-datastream-auditlog-v1.0.0\nEXPECTED job-logs-v1.0.0\nINSTALL auditlogs-datastream-auditlog-v1.0.0\nFAILED job-logs-v1.0.0 boom\nDONE\n"})
	var out bytes.Buffer
	err := RunInstallIndexTemplatesCLI(context.Background(), &out, healthClients(installFake(s)), healthInstances(), "box", ui.FormatText, 0, time.Millisecond)
	if CLIExitCode(err) != 1 || !strings.Contains(out.String(), "auditlogs-datastream-auditlog-v1.0.0") || !strings.Contains(out.String(), "boom") {
		t.Errorf("the run is processed to the end, then exits 1; err=%v\n%s", err, out.String())
	}
}

func TestRunInstallIndexTemplatesCLI_UsageErrorSendsNothing(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c"}
	err := RunInstallIndexTemplatesCLI(context.Background(), &bytes.Buffer{}, healthClients(ssm), healthInstances(), "nope", ui.FormatText, 0, time.Millisecond)
	var usage *UsageError
	if !errors.As(err, &usage) || ssm.sendCommandCallCount != 0 {
		t.Errorf("err=%v calls=%d", err, ssm.sendCommandCallCount)
	}
}

func TestRunInstallIndexTemplatesCLI_JSONErrorObjectCarriesTheClass(t *testing.T) {
	var out bytes.Buffer
	err := RunInstallIndexTemplatesCLI(context.Background(), &out, healthClients(installFake(with(installSections(), map[string]string{"prefix": "cfg=\nprefix=\n"}))), healthInstances(), "box", ui.FormatJSON, 0, time.Millisecond)
	if CLIExitCode(err) != 66 {
		t.Fatalf("err = %v", err)
	}
	var got struct {
		Error struct {
			Class string `json:"class"`
			Code  int    `json:"code"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil || got.Error.Class != "no_input" || got.Error.Code != 66 {
		t.Errorf("error object = %+v (%v)\n%s", got, jerr, out.String())
	}
}

func TestInstallIndexTemplatesForInstance_Interactive(t *testing.T) {
	var out bytes.Buffer
	if err := installIndexTemplatesForInstance(context.Background(), &out, healthClients(installFake(installSections())), healthInstance()); err != nil {
		t.Fatalf("a successful install: %v", err)
	}
	if !strings.Contains(out.String(), "job-logs-v1.0.0") {
		t.Errorf("the result is shown:\n%s", out.String())
	}
	failed := with(installSections(), map[string]string{"app": "status=ok\nEXPECTED job-logs-v1.0.0\nFAILED job-logs-v1.0.0 boom\nDONE\n", "before": installBefore0, "after": `{}`})
	out.Reset()
	if err := installIndexTemplatesForInstance(context.Background(), &out, healthClients(installFake(failed)), healthInstance()); err == nil {
		t.Errorf("a template that failed to install must be an error the menu shows")
	}
	if err := installIndexTemplatesForInstance(context.Background(), &bytes.Buffer{}, healthClients(&fakeSSMClient{commandID: "c", sendCommandErr: errors.New("x")}), healthInstance()); err == nil {
		t.Errorf("an unreachable instance is an error")
	}
}

func TestWriteIndexTemplateInstall_AllFailedIsNotNothingToInstall(t *testing.T) {
	s := with(installSections(), map[string]string{
		"before": installBefore0, "after": `{}`,
		"app": "status=ok\nEXPECTED job-logs-v1.0.0\nFAILED job-logs-v1.0.0 boom\nDONE\n"})
	var out bytes.Buffer
	if err := WriteIndexTemplateInstall(&out, mustInstallReport(t, s), ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Nothing to install") || !strings.Contains(out.String(), "FAILED job-logs-v1.0.0: boom") {
		t.Errorf("a run where every template failed must not claim there was nothing to do:\n%s", out.String())
	}
}
