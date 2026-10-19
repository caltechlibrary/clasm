package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// environmentTag is the live tag the stop form reads to decide whether DR-0195's
// gate applies. The inventory's copy of Environment is deliberately set to
// something else in the tests that matter: only AWS's current answer counts.
func environmentTag(value string) []types.Tag {
	return []types.Tag{{Key: aws.String("Environment"), Value: aws.String(value)}}
}

type stopArgs struct {
	inventoryState string
	confirms       []string
	interactive    bool
	input          io.Reader
	format         ui.Format
}

func runStop(f *fakeEC2Client, arg string, a stopArgs) (out, eout string, err error) {
	var w, e bytes.Buffer
	var output io.Writer = &w // prompt text lands beside the plan, as the gate tests read it
	if a.format == "" {
		a.format = ui.FormatText
	}
	err = runStopInstanceCLI(context.Background(), &w, &e, powerClients(f), powerFixture(a.inventoryState), arg, a.format,
		a.confirms, a.interactive, a.input, output, 50*time.Millisecond, time.Millisecond)
	return w.String(), e.String(), err
}

// running is the common starting point: a live-running instance that stops on
// the second DescribeInstances call.
func runningFake(tags []types.Tag) *fakeEC2Client {
	return &fakeEC2Client{initialState: types.InstanceStateNameRunning, stoppedAfterCall: 3, instanceTags: tags}
}

func TestParseStopInstanceArgs(t *testing.T) {
	inst, format, confirms, err := ParseStopInstanceArgs([]string{"box"})
	if err != nil || inst != "box" || format != ui.FormatText || len(confirms) != 0 {
		t.Errorf("plain: got %q %v %v %v", inst, format, confirms, err)
	}
	inst, format, confirms, err = ParseStopInstanceArgs([]string{"-json", "--confirm", "box", "i-1"})
	if err != nil || inst != "i-1" || format != ui.FormatJSON || len(confirms) != 1 || confirms[0] != "box" {
		t.Errorf("-json --confirm: got %q %v %v %v", inst, format, confirms, err)
	}
	for _, args := range [][]string{nil, {"a", "b"}, {""}, {"--confirm"}, {"--confirm", "", "box"}, {"-bogus", "box"}, {"-text", "-json", "box"}} {
		_, _, _, err := ParseStopInstanceArgs(args)
		var u *UsageError
		if !errors.As(err, &u) || CLIExitCode(err) != 2 {
			t.Errorf("%q: want a usage error (exit 2), got %v", args, err)
		}
	}
	_, _, _, err = ParseStopInstanceArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || CLIExitCode(err) != 0 {
		t.Fatalf("--help: want HelpRequested, got %v", err)
	}
	for _, want := range []string{StopInstanceCLISlug, "--confirm", "production"} {
		if !strings.Contains(h.Usage, want) {
			t.Errorf("help lacks %q:\n%s", want, h.Usage)
		}
	}
}

// Start is never gated, so it does not take --confirm at all.
func TestParseStartInstanceArgs_RejectsConfirm(t *testing.T) {
	_, _, err := ParseStartInstanceArgs([]string{"--confirm", "box", "box"})
	if CLIExitCode(err) != 2 {
		t.Errorf("start --confirm: want exit 2, got %d (%v)", CLIExitCode(err), err)
	}
}

func TestStopInstanceCLI_StopsARunningInstanceAndWaits(t *testing.T) {
	f := runningFake(environmentTag("test"))
	out, eout, err := runStop(f, "box", stopArgs{inventoryState: "running"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput == nil || len(f.lastStopInstancesInput.InstanceIds) != 1 || f.lastStopInstancesInput.InstanceIds[0] != "i-1" {
		t.Fatalf("want one StopInstances for i-1, got %+v", f.lastStopInstancesInput)
	}
	if f.describeCalls < 3 {
		t.Errorf("did not wait for stopped: %d describe calls", f.describeCalls)
	}
	if !strings.Contains(out, "is now stopped") {
		t.Errorf("stdout lacks the result:\n%s", out)
	}
	if !strings.Contains(eout, "Stopping i-1") || strings.Contains(out, "Stopping i-1") {
		t.Errorf("the progress line must go to stderr only:\nstdout: %s\nstderr: %s", out, eout)
	}
}

// Only production is gated; an untagged instance and the other two values run
// without --confirm.
func TestStopInstanceCLI_OnlyProductionIsGated(t *testing.T) {
	for name, tags := range map[string][]types.Tag{
		"untagged": nil, "test": environmentTag("test"), "development": environmentTag("development"),
		"Production-case": environmentTag("Production"), // not the value the launch flows write
	} {
		f := runningFake(tags)
		if _, _, err := runStop(f, "box", stopArgs{inventoryState: "running"}); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
		if f.lastStopInstancesInput == nil {
			t.Errorf("%s: an ungated stop must be sent without --confirm", name)
		}
	}
}

func TestStopInstanceCLI_ProductionWithoutConfirmOrTerminalIsADryRun(t *testing.T) {
	f := runningFake(environmentTag("production"))
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", input: failingReader{t}})
	if err != nil {
		t.Fatalf("a dry run is not an error, got: %v", err)
	}
	if CLIExitCode(err) != 0 {
		t.Errorf("a dry run exits 0")
	}
	if f.lastStopInstancesInput != nil {
		t.Fatal("StopInstances was sent to production without --confirm")
	}
	for _, want := range []string{"Plan for", "production", "Nothing was changed", "--confirm box"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run lacks %q:\n%s", want, out)
		}
	}
}

func TestStopInstanceCLI_ProductionConfirmedByNameOrID(t *testing.T) {
	for _, confirm := range []string{"box", "i-1"} {
		f := runningFake(environmentTag("production"))
		out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", confirms: []string{confirm}, input: failingReader{t}})
		if err != nil {
			t.Fatalf("--confirm %s: unexpected error: %v", confirm, err)
		}
		if f.lastStopInstancesInput == nil {
			t.Errorf("--confirm %s: the stop was not sent", confirm)
		}
		if !strings.Contains(out, "is now stopped") {
			t.Errorf("--confirm %s: no result:\n%s", confirm, out)
		}
	}
}

// A --confirm that is not the target is exit 2 before any AWS call, production
// or not: a script that names the wrong instance must find out.
func TestStopInstanceCLI_ConfirmMismatchIsUsageBeforeAnyCall(t *testing.T) {
	for name, tags := range map[string][]types.Tag{"production": environmentTag("production"), "test": environmentTag("test")} {
		f := runningFake(tags)
		_, _, err := runStop(f, "box", stopArgs{inventoryState: "running", confirms: []string{"other-box"}})
		if CLIExitCode(err) != 2 {
			t.Errorf("%s: want exit 2, got %d (%v)", name, CLIExitCode(err), err)
		}
		if f.describeCalls != 0 || f.lastStopInstancesInput != nil {
			t.Errorf("%s: a mismatch must precede every AWS call: %d describes, stop=%v", name, f.describeCalls, f.lastStopInstancesInput)
		}
	}
}

// A correct --confirm on an instance that does not need one is harmless, so a
// script can pass it always.
func TestStopInstanceCLI_ConfirmOnANonProductionInstanceIsAccepted(t *testing.T) {
	f := runningFake(environmentTag("test"))
	if _, _, err := runStop(f, "box", stopArgs{inventoryState: "running", confirms: []string{"box"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput == nil {
		t.Error("the stop was not sent")
	}
}

func TestStopInstanceCLI_ProductionTerminalPromptsAndProceedsOnTheName(t *testing.T) {
	f := runningFake(environmentTag("production"))
	_, in, _ := newPipeEditor("box\n")
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", interactive: true, input: in})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput == nil {
		t.Errorf("the typed name must confirm the stop:\n%s", out)
	}
}

func TestStopInstanceCLI_ProductionTerminalWrongAnswerCancels(t *testing.T) {
	f := runningFake(environmentTag("production"))
	_, in, _ := newPipeEditor("wrong\n")
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", interactive: true, input: in})
	if err != nil {
		t.Fatalf("declining is not an error, got: %v", err)
	}
	if f.lastStopInstancesInput != nil {
		t.Error("a wrong answer must not stop production")
	}
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("want Cancelled:\n%s", out)
	}
}

// The gate reads the live Environment tag, not the inventory's copy, which is as
// old as clasm's start.
func TestStopInstanceCLI_GateReadsTheLiveTagNotTheInventory(t *testing.T) {
	var w, e bytes.Buffer
	inv := []inventory.Instance{{InstanceID: "i-1", Name: "box", State: "running", Region: "us-west-2", Environment: "test"}}
	f := runningFake(environmentTag("production"))
	err := runStopInstanceCLI(context.Background(), &w, &e, powerClients(f), inv, "box", ui.FormatText, nil, false, nil, nil, 50*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput != nil {
		t.Error("the live tag says production: the stop must be gated although the inventory says test")
	}

	inv[0].Environment = "production"
	g := runningFake(environmentTag("test"))
	w.Reset()
	if err := runStopInstanceCLI(context.Background(), &w, &e, powerClients(g), inv, "box", ui.FormatText, nil, false, nil, nil, 50*time.Millisecond, time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.lastStopInstancesInput == nil {
		t.Error("the live tag says test: the stop must not be gated although the inventory says production")
	}
}

func TestStopInstanceCLI_ActsOnLiveStateNotTheInventory(t *testing.T) {
	f := runningFake(nil)
	if _, _, err := runStop(f, "i-1", stopArgs{inventoryState: "stopped"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput == nil {
		t.Error("a live-running instance must be stopped although the inventory says stopped")
	}
	g := &fakeEC2Client{initialState: types.InstanceStateNameStopped}
	if _, _, err := runStop(g, "i-1", stopArgs{inventoryState: "running"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.lastStopInstancesInput != nil {
		t.Error("a live-stopped instance must not be sent a stop although the inventory says running")
	}
}

// Stopped is the wanted state already: nothing to send, and nothing to confirm
// even on production.
func TestStopInstanceCLI_StoppedIsANoOpEvenOnProduction(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopped, instanceTags: environmentTag("production")}
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", input: failingReader{t}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput != nil {
		t.Fatal("StopInstances was sent to a stopped instance")
	}
	if !strings.Contains(out, "already stopped") || !strings.Contains(out, "nothing was changed") || strings.Contains(out, "Plan for") {
		t.Errorf("want 'already stopped; nothing was changed' and no plan, got:\n%s", out)
	}
}

// Stopping is on its way: no second stop, but the form still waits for stopped.
func TestStopInstanceCLI_StoppingIsWaitedOutNotResent(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameStopping, stoppedAfterCall: 3, instanceTags: environmentTag("production")}
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", format: ui.FormatJSON})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.lastStopInstancesInput != nil {
		t.Error("StopInstances was sent to a stopping instance")
	}
	got := decodeJSONMap(t, out)
	if got["previous_state"] != "stopping" || got["state"] != "stopped" || got["changed"] != false {
		t.Errorf("want stopping -> stopped, changed false, got %v", got)
	}
}

func TestStopInstanceCLI_RefusesAStateThatForbidsIt(t *testing.T) {
	for _, state := range []types.InstanceStateName{
		types.InstanceStateNamePending, types.InstanceStateNameShuttingDown, types.InstanceStateNameTerminated,
	} {
		f := &fakeEC2Client{initialState: state}
		_, _, err := runStop(f, "box", stopArgs{inventoryState: "running"})
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
		if f.lastStopInstancesInput != nil {
			t.Errorf("%s: StopInstances was sent", state)
		}
	}
}

func TestStopInstanceCLI_UnknownNameIsUsageBeforeAnyCall(t *testing.T) {
	f := runningFake(nil)
	_, _, err := runStop(f, "nope", stopArgs{inventoryState: "running"})
	if CLIExitCode(err) != 2 {
		t.Errorf("unknown name: want exit 2, got %d (%v)", CLIExitCode(err), err)
	}
	if f.describeCalls != 0 || f.lastStopInstancesInput != nil {
		t.Errorf("a usage error must precede every AWS call: %d describes", f.describeCalls)
	}
}

func TestStopInstanceCLI_JSONShape(t *testing.T) {
	f := runningFake(environmentTag("test"))
	out, _, err := runStop(f, "box", stopArgs{inventoryState: "running", format: ui.FormatJSON})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	want := map[string]any{
		"instance_id": "i-1", "name": "box", "region": "us-west-2",
		"previous_state": "running", "state": "stopped", "changed": true, "declined": false,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// With -json stdout must stay one JSON object even when the gate declines: the
// plan goes to stderr and the object says nothing was done.
func TestStopInstanceCLI_JSONDryRunKeepsStdoutParseable(t *testing.T) {
	f := runningFake(environmentTag("production"))
	out, eout, err := runStop(f, "box", stopArgs{inventoryState: "running", format: ui.FormatJSON, input: failingReader{t}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeJSONMap(t, out)
	if got["changed"] != false || got["declined"] != true || got["state"] != "running" {
		t.Errorf("want changed false, declined true, state running, got %v", got)
	}
	if !strings.Contains(eout, "Plan for") || !strings.Contains(eout, "Nothing was changed") {
		t.Errorf("the plan belongs on stderr:\n%s", eout)
	}
	if f.lastStopInstancesInput != nil {
		t.Error("StopInstances was sent")
	}
}

func TestStopInstanceCLI_WaitTimeoutIsExit1AndSaysHowToCheck(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning} // never reaches stopped
	_, _, err := runStop(f, "box", stopArgs{inventoryState: "running"})
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if f.lastStopInstancesInput == nil {
		t.Error("the stop must have been sent before the wait gave up")
	}
	if CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %d", CLIExitCode(err))
	}
	if !strings.Contains(err.Error(), "clasm compute show-instance-detail i-1") {
		t.Errorf("the message must say how to check, got %q", err)
	}
}

func TestStopInstanceCLI_APIErrorIsNotSuccess(t *testing.T) {
	f := &fakeEC2Client{initialState: types.InstanceStateNameRunning, stopInstancesErr: errors.New("UnauthorizedOperation")}
	_, _, err := runStop(f, "box", stopArgs{inventoryState: "running"})
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "i-1") {
		t.Errorf("want exit 1 naming the instance, got %d %v", CLIExitCode(err), err)
	}
}

func TestStopInstanceCLI_UnreachableAWSIsExit69(t *testing.T) {
	unreachable := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")}

	f := &fakeEC2Client{describeErr: unreachable}
	_, _, err := runStop(f, "box", stopArgs{inventoryState: "running"})
	if CLIExitCode(err) != 69 {
		t.Errorf("state check: want exit 69, got %d (%v)", CLIExitCode(err), err)
	}
	if f.lastStopInstancesInput != nil {
		t.Error("StopInstances was sent after the state check failed")
	}

	g := &fakeEC2Client{initialState: types.InstanceStateNameRunning, stopInstancesErr: unreachable}
	_, _, err = runStop(g, "box", stopArgs{inventoryState: "running"})
	if CLIExitCode(err) != 69 {
		t.Errorf("stop call: want exit 69, got %d (%v)", CLIExitCode(err), err)
	}
}

// The menu path and the CLI path share StopInstance, so the same call goes to
// AWS from either.
func TestStopInstance_MenuAndCLISendTheSameCall(t *testing.T) {
	menu := &fakeEC2Client{stoppedAfterCall: 1}
	var w bytes.Buffer
	err := stopEC2Instance(context.Background(), &w, powerClients(menu), powerFixture("running")[0], strings.NewReader("y\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("menu path: %v", err)
	}
	cli := runningFake(nil)
	if _, _, err := runStop(cli, "box", stopArgs{inventoryState: "running"}); err != nil {
		t.Fatalf("cli path: %v", err)
	}
	if menu.lastStopInstancesInput == nil || cli.lastStopInstancesInput == nil {
		t.Fatalf("both paths must stop the instance: menu=%v cli=%v", menu.lastStopInstancesInput, cli.lastStopInstancesInput)
	}
	if m, c := menu.lastStopInstancesInput.InstanceIds, cli.lastStopInstancesInput.InstanceIds; len(m) != 1 || len(c) != 1 || m[0] != c[0] {
		t.Errorf("menu sent %v, cli sent %v", m, c)
	}
}
