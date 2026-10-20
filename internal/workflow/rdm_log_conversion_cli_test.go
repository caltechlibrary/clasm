package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The form: DR-0180's gate applies on an instance whose live Environment tag is production and nowhere
// else (DR-0198, the author's decision of 2026-10-19). What is converted is the algorithm's business
// (rdm_log_conversion_test.go); these tests are about when it may run and what it says.

func cvPlan() conversionPlan {
	return conversionPlan{Prefix: cvPrefix, Logs: []logConversion{auditConversion(), jobsConversion()}}
}

func cvBoth() *fakeOS {
	f := auditFake()
	f.put(cvJobs, 4, "j")
	return f
}

type cvRun struct {
	plan        conversionPlan
	production  bool
	confirms    []string
	interactive bool
	input       string
	format      ui.Format
}

func runCV(f *fakeOS, r cvRun) (out, eout string, err error) {
	var w, e bytes.Buffer
	if r.format == "" {
		r.format = ui.FormatText
	}
	if r.plan.Prefix == "" {
		r.plan = cvPlan()
	}
	var in *strings.Reader
	if r.input != "" {
		in = strings.NewReader(r.input)
	}
	inst := inventory.Instance{InstanceID: "i-1", Name: "box", Region: "us-west-2"}
	if in != nil {
		err = convertWithGate(context.Background(), &w, &e, f, r.plan, inst, r.production, r.confirms, r.interactive, in, &w, r.format)
	} else {
		err = convertWithGate(context.Background(), &w, &e, f, r.plan, inst, r.production, r.confirms, r.interactive, nil, nil, r.format)
	}
	return w.String(), e.String(), err
}

func TestConvertForm_ProductionWithoutConfirmIsADryRun(t *testing.T) {
	f := cvBoth()
	out, _, err := runCV(f, cvRun{production: true})
	if err != nil || len(f.calls) != 0 {
		t.Fatalf("a dry run changes nothing and exits 0: err=%v calls=%v", err, f.calls)
	}
	for _, want := range []string{"Plan for", "audit log", "job logs", cvOrphan, "clasm-fix-auditlog-orphan", "clasm-fix-job-logs-plain", "never deletes", "Nothing was changed", "--confirm box"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan must contain %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "5 documents") || !strings.Contains(out, "3 documents") {
		t.Errorf("the plan says how many documents each index holds:\n%s", out)
	}
}

func TestConvertForm_ProductionWithConfirmConverts(t *testing.T) {
	f := cvBoth()
	out, _, err := runCV(f, cvRun{production: true, confirms: []string{"box"}})
	if err != nil {
		t.Fatal(err)
	}
	if !f.streams[cvStream] || !f.streams[cvJobs] {
		t.Errorf("both logs are streams now")
	}
	if !strings.Contains(out, "Confirmed (--confirm box)") {
		t.Errorf("a confirmed run logs what confirmed it:\n%s", out)
	}
}

func TestConvertForm_ConfirmByInstanceIDWorksToo(t *testing.T) {
	f := cvBoth()
	if _, _, err := runCV(f, cvRun{production: true, confirms: []string{"i-1"}}); err != nil || !f.streams[cvStream] {
		t.Errorf("the instance ID confirms as the Name tag does: %v", err)
	}
}

func TestConvertForm_AWrongConfirmIsAUsageErrorBeforeAnyCall(t *testing.T) {
	for name, production := range map[string]bool{"production": true, "test": false} {
		f := cvBoth()
		_, _, err := runCV(f, cvRun{production: production, confirms: []string{"other-box"}})
		var usage *UsageError
		if !errors.As(err, &usage) || len(f.calls) != 0 {
			t.Errorf("%s: err=%v calls=%v, want a *UsageError and no calls", name, err, f.calls)
		}
	}
}

func TestConvertForm_OtherInstancesRunWithoutAGate(t *testing.T) {
	f := cvBoth()
	out, _, err := runCV(f, cvRun{production: false})
	if err != nil || !f.streams[cvStream] || !f.streams[cvJobs] {
		t.Fatalf("a non-production instance converts with no confirmation: err=%v", err)
	}
	if strings.Contains(out, "Nothing was changed") || strings.Contains(out, "Plan for") {
		t.Errorf("no gate means no plan prompt:\n%s", out)
	}
}

func TestConvertForm_ProductionOnATerminalAsksAndTheNameProceeds(t *testing.T) {
	f := cvBoth()
	_, _, err := runCV(f, cvRun{production: true, interactive: true, input: "box\n"})
	if err != nil || !f.streams[cvStream] {
		t.Errorf("typing the instance name at the prompt confirms: %v", err)
	}
	f = cvBoth()
	_, _, err = runCV(f, cvRun{production: true, interactive: true, input: "nope\n"})
	if err != nil || len(f.calls) != 0 {
		t.Errorf("anything else cancels without a change: err=%v calls=%v", err, f.calls)
	}
}

func TestConvertForm_NothingToConvertNeedsNoGateAndSaysSo(t *testing.T) {
	f := newFakeOS()
	out, _, err := runCV(f, cvRun{production: true, plan: conversionPlan{Prefix: cvPrefix, Notes: []string{"audit log: already a data stream", "job logs: already a data stream"}}})
	if err != nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
	if !strings.Contains(out, "Nothing to convert") || !strings.Contains(out, "already a data stream") || strings.Contains(out, "Plan for") {
		t.Errorf("a healthy instance is a plain no-op, even on production:\n%s", out)
	}
}

func TestConvertForm_ProgressGoesToStderrAndTheResultToStdout(t *testing.T) {
	f := cvBoth()
	out, eout, err := runCV(f, cvRun{production: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(eout, "copying "+cvOrphan) {
		t.Errorf("progress lines belong on stderr:\n%s", eout)
	}
	if strings.Contains(out, "copying ") || !strings.Contains(out, "Converted") || !strings.Contains(out, "clasm-fix-auditlog-plain") {
		t.Errorf("stdout is the result alone, and names the copies kept:\n%s", out)
	}
}

func TestConvertForm_JSONResult(t *testing.T) {
	f := cvBoth()
	out, _, err := runCV(f, cvRun{production: false, format: ui.FormatJSON})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		InstanceID string      `json:"instance_id"`
		Declined   bool        `json:"declined"`
		Converted  []logResult `json:"converted"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("stdout must be one JSON object: %v\n%s", jerr, out)
	}
	if got.InstanceID != "i-1" || got.Declined || len(got.Converted) != 2 || got.Converted[0].StreamDocs != 8 {
		t.Errorf("result = %+v", got)
	}
}

func TestConvertForm_JSONDryRunPutsThePlanOnStderrAndSaysDeclined(t *testing.T) {
	f := cvBoth()
	out, eout, err := runCV(f, cvRun{production: true, format: ui.FormatJSON})
	if err != nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
	var got struct {
		Declined  bool        `json:"declined"`
		Converted []logResult `json:"converted"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil || !got.Declined || len(got.Converted) != 0 {
		t.Errorf("a declined run says so in JSON: %v\n%s", jerr, out)
	}
	if !strings.Contains(eout, "Plan for") {
		t.Errorf("with -json the plan goes to stderr so stdout stays one object:\n%s", eout)
	}
}

func TestConvertForm_AFailureIsExit1NamingTheCopiesAndJSONCarriesTheClass(t *testing.T) {
	f := cvBoth()
	f.failAt = 8
	out, _, err := runCV(f, cvRun{production: false, format: ui.FormatJSON})
	if CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "clasm-fix-auditlog") {
		t.Fatalf("err = %v, want exit 1 naming the copies", err)
	}
	var got struct {
		Error struct {
			Class string `json:"class"`
			Code  int    `json:"code"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil || got.Error.Class != "negative" || got.Error.Code != 1 {
		t.Errorf("error object = %+v (%v)\n%s", got, jerr, out)
	}
}

// ---- arguments ----

func TestParseConvertLogIndicesArgs(t *testing.T) {
	inst, format, wait, opts, err := ParseConvertLogIndicesArgs([]string{"box"})
	if err != nil || inst != "box" || format != ui.FormatText || wait != DefaultIndexHealthWait || len(opts.Confirms) != 0 {
		t.Errorf("defaults: %q %v %v %v %v", inst, format, wait, opts, err)
	}
	inst, format, wait, opts, err = ParseConvertLogIndicesArgs([]string{"-json", "-wait", "0", "--confirm", "box", "box"})
	if err != nil || format != ui.FormatJSON || wait != 0 || len(opts.Confirms) != 1 || opts.Confirms[0] != "box" {
		t.Errorf("all options: %v %v %v %v", format, wait, opts, err)
	}
	for name, args := range map[string][]string{
		"no instance": {}, "surplus": {"box", "x"}, "unknown option": {"-bogus", "box"}, "two formats": {"-text", "-json", "box"},
		"bad wait": {"-wait", "soon", "box"}, "negative wait": {"-wait", "-1s", "box"}, "blank": {" "}, "empty confirm": {"--confirm", "", "box"},
	} {
		_, _, _, _, err := ParseConvertLogIndicesArgs(args)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a *UsageError", name, err)
		}
	}
	_, _, _, _, err = ParseConvertLogIndicesArgs([]string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, ConvertLogIndicesCLISlug) || !strings.Contains(help.Usage, "Exit status") || !strings.Contains(help.Usage, "--confirm") {
		t.Errorf("--help: %v", err)
	}
}

// ---- the public form: what runs when ----

func cvDiscoveryStdout(overrides map[string]string) string {
	var b strings.Builder
	s := with(cvSections(), overrides)
	for _, k := range []string{"prefix", "endpoint", "data_streams", "indices", "aliases", "templates_ds"} {
		b.WriteString("@@ " + k + "\n" + s[k])
		if !strings.HasSuffix(s[k], "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func cvFakes(overrides map[string]string, tags []types.Tag) (*fakeSSMClient, *fakeEC2Client) {
	ssm := &fakeSSMClient{commandID: "c", finalStatus: ssmtypes.CommandInvocationStatusSuccess, stdout: cvDiscoveryStdout(overrides)}
	return ssm, &fakeEC2Client{initialState: types.InstanceStateNameRunning, instanceTags: tags}
}

func runCVPublic(ssm *fakeSSMClient, ec2 *fakeEC2Client, arg string, confirms []string) (string, error) {
	var w bytes.Buffer
	err := RunConvertLogIndicesCLI(context.Background(), &w, &bytes.Buffer{}, healthClients(ssm), powerClients(ec2), healthInstances(), arg, confirms, ui.FormatText, 0, time.Millisecond, false, nil, nil)
	return w.String(), err
}

var cvHealthy = map[string]string{
	"data_streams": `{"data_streams":[{"name":"` + cvStream + `","indices":[{"index_name":"` + cvOrphan + `"}]},{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	"indices":      `[{"index":"` + cvOrphan + `","docs.count":"5"},{"index":".ds-` + cvJobs + `-000001","docs.count":"4"}]`,
	"aliases":      `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"}]`,
}

func TestRunConvertLogIndicesCLI_HealthyNeverReadsEC2OrWrites(t *testing.T) {
	ssm, ec2 := cvFakes(cvHealthy, environmentTag("production"))
	out, err := runCVPublic(ssm, ec2, "box", nil)
	if err != nil || ssm.sendCommandCallCount != 1 || ec2.describeCalls != 0 {
		t.Fatalf("a healthy instance is one read and nothing else: err=%v ssm calls=%d ec2 calls=%d", err, ssm.sendCommandCallCount, ec2.describeCalls)
	}
	if !strings.Contains(out, "Nothing to convert") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunConvertLogIndicesCLI_ProductionIsReadFromTheLiveTag(t *testing.T) {
	ssm, ec2 := cvFakes(nil, environmentTag("production"))
	out, err := runCVPublic(ssm, ec2, "box", nil)
	if err != nil || ssm.sendCommandCallCount != 1 {
		t.Fatalf("production without --confirm is a dry run, so only discovery was sent: err=%v ssm calls=%d", err, ssm.sendCommandCallCount)
	}
	if !strings.Contains(out, "Nothing was changed") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunConvertLogIndicesCLI_AnotherEnvironmentGoesStraightToWork(t *testing.T) {
	for name, tags := range map[string][]types.Tag{"test": environmentTag("test"), "untagged": nil} {
		ssm, ec2 := cvFakes(nil, tags)
		_, _ = runCVPublic(ssm, ec2, "box", nil)
		if ssm.sendCommandCallCount < 2 || !strings.Contains(ssm.sentCommands[1], "clasm-fix-auditlog-orphan") || !strings.Contains(ssm.sentCommands[1], "-X PUT") {
			t.Errorf("%s: with no gate the first write follows discovery (copy index created); commands %d: %v", name, ssm.sendCommandCallCount, ssm.sentCommands)
		}
	}
}

func TestRunConvertLogIndicesCLI_ExitCodePerClass(t *testing.T) {
	cases := []struct {
		name string
		ssm  *fakeSSMClient
		ec2  *fakeEC2Client
		arg  string
		want int
	}{
		{"healthy", func() *fakeSSMClient { s, _ := cvFakes(cvHealthy, nil); return s }(), &fakeEC2Client{}, "box", 0},
		{"refusal: no template", func() *fakeSSMClient {
			s, _ := cvFakes(map[string]string{"templates_ds": `{}`}, nil)
			return s
		}(), &fakeEC2Client{}, "box", 1},
		{"no app", func() *fakeSSMClient { s, _ := cvFakes(map[string]string{"prefix": "cfg=\nprefix=\n"}, nil); return s }(), &fakeEC2Client{}, "box", 66},
		{"endpoint down", func() *fakeSSMClient { s, _ := cvFakes(map[string]string{"endpoint": "status=000\n"}, nil); return s }(), &fakeEC2Client{}, "box", 69},
		{"ssm unreachable", &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}, &fakeEC2Client{}, "box", 69},
		{"unreadable", &fakeSSMClient{commandID: "c", finalStatus: ssmtypes.CommandInvocationStatusSuccess, stdout: "garbage"}, &fakeEC2Client{}, "box", 65},
		{"script failed", &fakeSSMClient{commandID: "c", finalStatus: ssmtypes.CommandInvocationStatusFailed}, &fakeEC2Client{}, "box", 70},
		{"unknown instance", &fakeSSMClient{commandID: "c"}, &fakeEC2Client{}, "nope", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runCVPublic(tc.ssm, tc.ec2, tc.arg, nil)
			if got := CLIExitCode(err); got != tc.want {
				t.Errorf("exit code = %d (err %v), want %d", got, err, tc.want)
			}
		})
	}
}

func TestRunConvertLogIndicesCLI_ProductionLookupFailureIs69AndWritesNothing(t *testing.T) {
	ssm, _ := cvFakes(nil, nil)
	ec2 := &fakeEC2Client{describeErr: errors.New("dial tcp: i/o timeout")}
	_, err := runCVPublic(ssm, ec2, "box", nil)
	if err == nil || ssm.sendCommandCallCount != 1 {
		t.Errorf("when the environment cannot be read the form must not guess it is safe: err=%v ssm calls=%d", err, ssm.sendCommandCallCount)
	}
}

func TestRunConvertLogIndicesCLI_AMismatchedConfirmSendsNothing(t *testing.T) {
	ssm, ec2 := cvFakes(nil, environmentTag("production"))
	_, err := runCVPublic(ssm, ec2, "box", []string{"other"})
	var usage *UsageError
	if !errors.As(err, &usage) || ssm.sendCommandCallCount != 0 || ec2.describeCalls != 0 {
		t.Errorf("a typo fails before any AWS call: err=%v ssm=%d ec2=%d", err, ssm.sendCommandCallCount, ec2.describeCalls)
	}
}
