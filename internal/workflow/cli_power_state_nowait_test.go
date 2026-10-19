package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// --no-wait (DR-0195): send the call and return once AWS has accepted it. The
// state check, the idempotence rule and the production gate are unchanged; only
// the polling goes. A fake that would never reach the end state proves nothing
// polls: the wait would time out.

func runStartNoWait(f *fakeEC2Client, arg string, format ui.Format) (out, eout string, err error) {
	var w, e bytes.Buffer
	err = runStartInstanceCLI(context.Background(), &w, &e, powerClients(f), powerFixture("stopped"), arg, format, true,
		50*time.Millisecond, time.Millisecond)
	return w.String(), e.String(), err
}

func runStopNoWait(f *fakeEC2Client, arg string, format ui.Format, confirms []string) (out, eout string, err error) {
	var w, e bytes.Buffer
	err = runStopInstanceCLI(context.Background(), &w, &e, powerClients(f), powerFixture("running"), arg, format, true,
		confirms, false, failingReader{}, &w, 50*time.Millisecond, time.Millisecond)
	return w.String(), e.String(), err
}

func TestParseInstanceArgs_NoWait(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{{[]string{"box"}, false}, {[]string{"-no-wait", "box"}, true}, {[]string{"--no-wait", "box"}, true}} {
		_, _, got, err := ParseStartInstanceArgs(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("start %v: got noWait=%v err=%v, want %v", tc.args, got, err, tc.want)
		}
		_, _, got, _, err = ParseStopInstanceArgs(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("stop %v: got noWait=%v err=%v, want %v", tc.args, got, err, tc.want)
		}
	}
	_, _, _, err := ParseStartInstanceArgs([]string{"--help"})
	if h, ok := err.(*HelpRequested); !ok || !strings.Contains(h.Usage, "--no-wait") || !strings.Contains(h.Usage, "-no-wait") {
		t.Errorf("start help must describe --no-wait, got %v", err)
	}
	_, _, _, _, err = ParseStopInstanceArgs([]string{"--help"})
	if h, ok := err.(*HelpRequested); !ok || !strings.Contains(h.Usage, "--no-wait") {
		t.Errorf("stop help must describe --no-wait, got %v", err)
	}
	if !strings.Contains(StartInstanceUsage, "[-no-wait]") || !strings.Contains(StopInstanceUsage, "[-no-wait]") {
		t.Errorf("both usage lines must show -no-wait:\n%s\n%s", StartInstanceUsage, StopInstanceUsage)
	}
}

func TestStartInstanceCLI_NoWaitSendsAndReturnsAtOnce(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped} // would never reach running
	out, eout, err := runStartNoWait(f, "box", ui.FormatText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput == nil {
		t.Fatal("StartInstances was not sent")
	}
	if strings.Contains(eout, "waiting for it") {
		t.Errorf("the progress line must not claim to wait:\n%s", eout)
	}
	if f.describeCalls != 1 {
		t.Errorf("want only the state check (1 describe), got %d: something polled", f.describeCalls)
	}
	for _, want := range []string{"Start requested", "pending", "clasm compute show-instance-detail i-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Public IP", "ssh -i", "aws ssm start-session", "public IP may have changed"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("no connection details exist yet, but stdout has %q:\n%s", unwanted, out)
		}
	}
}

func TestStartInstanceCLI_NoWaitJSON(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, publicIP: "203.0.113.9", instanceDetailPrivateIP: "10.0.0.5"}
	out, _, err := runStartNoWait(f, "box", ui.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	if got["previous_state"] != "stopped" || got["state"] != "pending" || got["changed"] != true {
		t.Errorf("want stopped -> pending, changed true, got %v", got)
	}
	for _, k := range []string{"public_ip", "private_ip", "ssm_start_session"} {
		if got[k] != "" {
			t.Errorf("%s = %v: no connection details exist before running", k, got[k])
		}
	}
}

func TestStartInstanceCLI_NoWaitOnPendingSendsNothingAndIsNotChanged(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNamePending}
	out, _, err := runStartNoWait(f, "box", ui.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput != nil || f.describeCalls != 1 {
		t.Errorf("want nothing sent and no polling: start=%v describes=%d", f.lastStartInstancesInput, f.describeCalls)
	}
	got := decodeJSONMap(t, out)
	if got["previous_state"] != "pending" || got["state"] != "pending" || got["changed"] != false {
		t.Errorf("want pending -> pending, changed false, got %v", got)
	}
	text, peout, _ := runStartNoWait(&fakeEC2Client{initialState: types.InstanceStateNamePending}, "box", ui.FormatText)
	if strings.Contains(peout, "waiting for it") {
		t.Errorf("the progress line must not claim to wait:\n%s", peout)
	}
	if !strings.Contains(text, "already starting") || !strings.Contains(text, "nothing was sent") {
		t.Errorf("text must say it was already starting and nothing was sent:\n%s", text)
	}
}

// A running instance is the same no-op as without the option, connection
// details included: they are known.
func TestStartInstanceCLI_NoWaitOnRunningIsTheUsualNoOp(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning, publicIP: "203.0.113.9"}
	out, _, err := runStartNoWait(f, "box", ui.FormatText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput != nil || !strings.Contains(out, "already running") || !strings.Contains(out, "203.0.113.9") {
		t.Errorf("want the usual no-op with connection details, got start=%v:\n%s", f.lastStartInstancesInput, out)
	}
}

func TestStartInstanceCLI_NoWaitStillRefusesAForbiddenState(t *testing.T) {
	for _, state := range []types.InstanceStateName{types.InstanceStateNameStopping, types.InstanceStateNameTerminated, types.InstanceStateNameShuttingDown} {
		f := &fakeEC2Client{initialState: state}
		_, _, err := runStartNoWait(f, "box", ui.FormatText)
		if CLIExitCode(err) != 1 || f.lastStartInstancesInput != nil {
			t.Errorf("%s: want exit 1 and nothing sent, got %d start=%v", state, CLIExitCode(err), f.lastStartInstancesInput)
		}
	}
}

func TestStopInstanceCLI_NoWaitSendsAndReturnsAtOnce(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning, instanceTags: environmentTag("test")} // would never reach stopped
	out, eout, err := runStopNoWait(f, "box", ui.FormatText, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput == nil {
		t.Fatal("StopInstances was not sent")
	}
	if strings.Contains(eout, "waiting for it") {
		t.Errorf("the progress line must not claim to wait:\n%s", eout)
	}
	if f.describeCalls != 1 {
		t.Errorf("want only the state check (1 describe), got %d: something polled", f.describeCalls)
	}
	for _, want := range []string{"Stop requested", "stopping", "clasm compute show-instance-detail i-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "is now stopped") {
		t.Errorf("it has not stopped yet, but stdout says so:\n%s", out)
	}
}

func TestStopInstanceCLI_NoWaitJSON(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning}
	out, _, err := runStopNoWait(f, "box", ui.FormatJSON, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	if got["previous_state"] != "running" || got["state"] != "stopping" || got["changed"] != true || got["declined"] != false {
		t.Errorf("want running -> stopping, changed true, declined false, got %v", got)
	}
}

func TestStopInstanceCLI_NoWaitOnStoppingAndStoppedSendNothing(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopping}
	out, _, err := runStopNoWait(f, "box", ui.FormatJSON, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput != nil || f.describeCalls != 1 {
		t.Errorf("stopping: want nothing sent and no polling: stop=%v describes=%d", f.lastStopInstancesInput, f.describeCalls)
	}
	if got := decodeJSONMap(t, out); got["state"] != "stopping" || got["changed"] != false {
		t.Errorf("stopping: want state stopping, changed false, got %v", got)
	}
	text, seout, _ := runStopNoWait(&fakeEC2Client{initialState: types.InstanceStateNameStopping}, "box", ui.FormatText, nil)
	if strings.Contains(seout, "waiting for it") {
		t.Errorf("the progress line must not claim to wait:\n%s", seout)
	}
	if !strings.Contains(text, "already stopping") || !strings.Contains(text, "nothing was sent") {
		t.Errorf("text must say it was already stopping and nothing was sent:\n%s", text)
	}

	g := &fakeEC2Client{initialState: types.InstanceStateNameStopped}
	out, _, err = runStopNoWait(g, "box", ui.FormatJSON, nil)
	if err != nil || g.lastStopInstancesInput != nil {
		t.Fatalf("stopped: err=%v stop=%v", err, g.lastStopInstancesInput)
	}
	if got := decodeJSONMap(t, out); got["state"] != "stopped" || got["changed"] != false {
		t.Errorf("stopped: want state stopped, changed false, got %v", got)
	}
}

// The gate is about whether to stop, not about waiting: --no-wait cannot be a
// way round it.
func TestStopInstanceCLI_NoWaitDoesNotSkipTheProductionGate(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning, instanceTags: environmentTag("production")}
	out, _, err := runStopNoWait(f, "box", ui.FormatJSON, nil)
	if err != nil {
		t.Fatalf("a dry run is not an error, got: %v", err)
	}
	if f.lastStopInstancesInput != nil {
		t.Fatal("StopInstances was sent to production without --confirm")
	}
	if got := decodeJSONMap(t, out); got["declined"] != true || got["changed"] != false || got["state"] != "running" {
		t.Errorf("want declined true, changed false, state running, got %v", got)
	}

	g := &fakeEC2Client{initialState: types.InstanceStateNameRunning, instanceTags: environmentTag("production")}
	out, _, err = runStopNoWait(g, "box", ui.FormatJSON, []string{"box"})
	if err != nil || g.lastStopInstancesInput == nil {
		t.Fatalf("a confirmed stop must be sent: err=%v stop=%v", err, g.lastStopInstancesInput)
	}
	if got := decodeJSONMap(t, out); got["state"] != "stopping" || got["changed"] != true || got["declined"] != false {
		t.Errorf("want running -> stopping, changed true, got %v", got)
	}
}

func TestStopInstanceCLI_NoWaitStillRefusesAForbiddenState(t *testing.T) {
	for _, state := range []types.InstanceStateName{types.InstanceStateNamePending, types.InstanceStateNameTerminated, types.InstanceStateNameShuttingDown} {
		f := &fakeEC2Client{initialState: state}
		_, _, err := runStopNoWait(f, "box", ui.FormatText, nil)
		if CLIExitCode(err) != 1 || f.lastStopInstancesInput != nil {
			t.Errorf("%s: want exit 1 and nothing sent, got %d stop=%v", state, CLIExitCode(err), f.lastStopInstancesInput)
		}
	}
}
