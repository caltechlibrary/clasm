package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// powerFixture is one instance, i-1 "box", in us-west-2, as the inventory saw
// it. The inventory's State is deliberately not what the fake reports: the
// forms must act on the instance's live state, since the inventory is a
// snapshot taken when clasm started.
func powerFixture(inventoryState string) []inventory.Instance {
	return []inventory.Instance{{InstanceID: "i-1", Name: "box", State: inventoryState, Region: "us-west-2"}}
}

func powerClients(f *fakeEC2Client) map[string]awsclient.EC2API {
	return map[string]awsclient.EC2API{"us-west-2": f}
}

func runStart(f *fakeEC2Client, inventoryState, arg string, format ui.Format) (out, eout string, err error) {
	var w, e bytes.Buffer
	err = runStartInstanceCLI(context.Background(), &w, &e, powerClients(f), powerFixture(inventoryState), arg, format, false,
		50*time.Millisecond, time.Millisecond)
	return w.String(), e.String(), err
}

func TestParseStartInstanceArgs(t *testing.T) {
	inst, format, _, err := ParseStartInstanceArgs([]string{"box"})
	if err != nil || inst != "box" || format != ui.FormatText {
		t.Errorf("plain: got %q %v %v", inst, format, err)
	}
	inst, format, _, err = ParseStartInstanceArgs([]string{"-json", "i-1"})
	if err != nil || inst != "i-1" || format != ui.FormatJSON {
		t.Errorf("-json: got %q %v %v", inst, format, err)
	}
	for _, args := range [][]string{nil, {"a", "b"}, {""}, {"  "}, {"-bogus", "box"}, {"-text", "-json", "box"}} {
		_, _, _, err := ParseStartInstanceArgs(args)
		var u *UsageError
		if !errors.As(err, &u) || CLIExitCode(err) != 2 {
			t.Errorf("%q: want a usage error (exit 2), got %v", args, err)
		}
	}
	_, _, _, err = ParseStartInstanceArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || CLIExitCode(err) != 0 {
		t.Fatalf("--help: want HelpRequested, got %v", err)
	}
	if !strings.Contains(h.Usage, StartInstanceCLISlug) {
		t.Errorf("help does not name the leaf:\n%s", h.Usage)
	}
}

func TestStartInstanceCLI_StartsAStoppedInstanceAndWaits(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, runningAfterCall: 3, publicIP: "203.0.113.9", instanceDetailPrivateIP: "10.0.0.5"}
	out, eout, err := runStart(f, "stopped", "box", ui.FormatText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput == nil || len(f.lastStartInstancesInput.InstanceIds) != 1 || f.lastStartInstancesInput.InstanceIds[0] != "i-1" {
		t.Fatalf("want one StartInstances for i-1, got %+v", f.lastStartInstancesInput)
	}
	if f.describeCalls < 3 {
		t.Errorf("did not wait for running: %d describe calls", f.describeCalls)
	}
	for _, want := range []string{"203.0.113.9", "10.0.0.5", "aws ssm start-session --target i-1", "public IP may have changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(eout, "Starting i-1") || strings.Contains(out, "Starting i-1") {
		t.Errorf("the progress line must go to stderr only:\nstdout: %s\nstderr: %s", out, eout)
	}
}

// The inventory said running, AWS says stopped (somebody stopped it since clasm
// loaded): the live state wins.
func TestStartInstanceCLI_ActsOnLiveStateNotTheInventory(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, runningAfterCall: 2}
	if _, _, err := runStart(f, "running", "i-1", ui.FormatText); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput == nil {
		t.Error("an instance that is live-stopped must be started even though the inventory says running")
	}

	g := &fakeEC2Client{initialState: types.InstanceStateNameRunning}
	if _, _, err := runStart(g, "stopped", "i-1", ui.FormatText); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.lastStartInstancesInput != nil {
		t.Error("an instance that is live-running must not be sent a start even though the inventory says stopped")
	}
}

func TestStartInstanceCLI_RunningIsANoOp(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning, publicIP: "203.0.113.9"}
	out, _, err := runStart(f, "running", "box", ui.FormatText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput != nil {
		t.Fatal("StartInstances was sent to a running instance")
	}
	if !strings.Contains(out, "already running") || !strings.Contains(out, "nothing was changed") {
		t.Errorf("want 'already running; nothing was changed', got:\n%s", out)
	}
	if strings.Contains(out, "public IP may have changed") {
		t.Errorf("the changed-address note belongs to a start that happened, got:\n%s", out)
	}
}

// Pending is on its way: no second start, but the form still waits for running.
func TestStartInstanceCLI_PendingIsWaitedOutNotResent(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNamePending, runningAfterCall: 3}
	out, _, err := runStart(f, "pending", "box", ui.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStartInstancesInput != nil {
		t.Error("StartInstances was sent to a pending instance")
	}
	got := decodeJSONMap(t, out)
	if got["previous_state"] != "pending" || got["state"] != "running" || got["changed"] != false {
		t.Errorf("want pending -> running, changed false, got %v", got)
	}
}

func TestStartInstanceCLI_RefusesAStateThatForbidsIt(t *testing.T) {
	for _, state := range []types.InstanceStateName{
		types.InstanceStateNameTerminated, types.InstanceStateNameShuttingDown, types.InstanceStateNameStopping,
	} {
		f := &fakeEC2Client{initialState: state}
		_, _, err := runStart(f, "stopped", "box", ui.FormatText)
		if err == nil {
			t.Errorf("%s: want an error", state)
			continue
		}
		if CLIExitCode(err) != 1 {
			t.Errorf("%s: want exit 1, got %d (%v)", state, CLIExitCode(err), err)
		}
		if !strings.Contains(err.Error(), string(state)) {
			t.Errorf("%s: the message must name the state, got %q", state, err)
		}
		if f.lastStartInstancesInput != nil {
			t.Errorf("%s: StartInstances was sent", state)
		}
	}
}

func TestStartInstanceCLI_UnknownOrAmbiguousNameIsUsageBeforeAnyCall(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped}
	_, _, err := runStart(f, "stopped", "nope", ui.FormatText)
	if CLIExitCode(err) != 2 {
		t.Errorf("unknown name: want exit 2, got %d (%v)", CLIExitCode(err), err)
	}
	two := append(powerFixture("stopped"), inventory.Instance{InstanceID: "i-2", Name: "box", State: "stopped", Region: "us-west-2"})
	var w, e bytes.Buffer
	err = runStartInstanceCLI(context.Background(), &w, &e, powerClients(f), two, "box", ui.FormatText, false, time.Millisecond, time.Millisecond)
	if CLIExitCode(err) != 2 {
		t.Errorf("ambiguous name: want exit 2, got %d (%v)", CLIExitCode(err), err)
	}
	if f.describeCalls != 0 || f.lastStartInstancesInput != nil {
		t.Errorf("a usage error must precede every AWS call: %d describes, start=%v", f.describeCalls, f.lastStartInstancesInput)
	}
}

func TestStartInstanceCLI_JSONShape(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, runningAfterCall: 2, publicIP: "203.0.113.9", instanceDetailPrivateIP: "10.0.0.5"}
	out, _, err := runStart(f, "stopped", "box", ui.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	want := map[string]any{
		"instance_id": "i-1", "name": "box", "region": "us-west-2",
		"previous_state": "stopped", "state": "running", "changed": true,
		"public_ip": "203.0.113.9", "private_ip": "10.0.0.5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if s, _ := got["ssm_start_session"].(string); !strings.Contains(s, "aws ssm start-session --target i-1") {
		t.Errorf("ssm_start_session = %v", got["ssm_start_session"])
	}
}

// A no-op reports changed false, so a script can tell whether it did anything.
func TestStartInstanceCLI_JSONNoOpIsNotChanged(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning}
	out, _, err := runStart(f, "running", "box", ui.FormatJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	if got["changed"] != false || got["previous_state"] != "running" || got["state"] != "running" {
		t.Errorf("want running -> running, changed false, got %v", got)
	}
}

func TestStartInstanceCLI_WaitTimeoutIsExit1AndSaysHowToCheck(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped} // never reaches running
	_, _, err := runStart(f, "stopped", "box", ui.FormatText)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if f.lastStartInstancesInput == nil {
		t.Error("the start must have been sent before the wait gave up")
	}
	if CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %d", CLIExitCode(err))
	}
	if !strings.Contains(err.Error(), "clasm compute show-instance-detail i-1") {
		t.Errorf("the message must say how to check, got %q", err)
	}
}

func TestStartInstanceCLI_APIErrorIsNotSuccess(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, startInstancesErr: errors.New("UnauthorizedOperation")}
	_, _, err := runStart(f, "stopped", "box", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "i-1") {
		t.Errorf("want exit 1 naming the instance, got %d %v", CLIExitCode(err), err)
	}
}

// DR-0014: an unreachable service is 69, whether the call that failed was the
// state check or the start itself, and nothing is sent after a failed check.
func TestStartInstanceCLI_UnreachableAWSIsExit69(t *testing.T) {
	unreachable := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")}

	f := &fakeEC2Client{describeErr: unreachable}
	_, _, err := runStart(f, "stopped", "box", ui.FormatText)
	if CLIExitCode(err) != 69 {
		t.Errorf("state check: want exit 69, got %d (%v)", CLIExitCode(err), err)
	}
	if f.lastStartInstancesInput != nil {
		t.Error("StartInstances was sent after the state check failed")
	}

	g := &fakeEC2Client{initialState: types.InstanceStateNameStopped, startInstancesErr: unreachable}
	_, _, err = runStart(g, "stopped", "box", ui.FormatText)
	if CLIExitCode(err) != 69 {
		t.Errorf("start call: want exit 69, got %d (%v)", CLIExitCode(err), err)
	}
}

func TestCLIExitCode_ExitCodeError(t *testing.T) {
	if got := CLIExitCode(&ExitCodeError{Code: 69, Err: errors.New("x")}); got != 69 {
		t.Errorf("got %d, want 69", got)
	}
	wrapped := errors.Join(errors.New("context"), &ExitCodeError{Code: 66, Err: errors.New("x")})
	if got := CLIExitCode(wrapped); got != 66 {
		t.Errorf("a wrapped ExitCodeError: got %d, want 66", got)
	}
	if got := CLIExitCode(errors.New("plain")); got != 1 {
		t.Errorf("a plain error stays 1, got %d", got)
	}
}

// The menu path and the CLI path share StartInstance, so the same call goes to
// AWS from either.
func TestStartInstance_MenuAndCLISendTheSameCall(t *testing.T) {
	menu := &fakeEC2Client{runningAfterCall: 1}
	var w bytes.Buffer
	err := startEC2Instance(context.Background(), &w, powerClients(menu), powerFixture("stopped")[0], strings.NewReader("y\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("menu path: %v", err)
	}
	cli := &fakeEC2Client{initialState: types.InstanceStateNameStopped, runningAfterCall: 2}
	if _, _, err := runStart(cli, "stopped", "box", ui.FormatText); err != nil {
		t.Fatalf("cli path: %v", err)
	}
	if menu.lastStartInstancesInput == nil || cli.lastStartInstancesInput == nil {
		t.Fatalf("both paths must start the instance: menu=%v cli=%v", menu.lastStartInstancesInput, cli.lastStartInstancesInput)
	}
	if m, c := menu.lastStartInstancesInput.InstanceIds, cli.lastStartInstancesInput.InstanceIds; len(m) != 1 || len(c) != 1 || m[0] != c[0] {
		t.Errorf("menu sent %v, cli sent %v", m, c)
	}
}

func decodeJSONMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(s), &got); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, s)
	}
	return got
}
