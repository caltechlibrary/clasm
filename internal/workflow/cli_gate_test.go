package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// DR-0180 (design brief destructive_cli_gate.md): one gate for every destructive
// CLI form. A dry run by default that prints the plan; a terminal gets the TUI's
// type-to-confirm prompt; no terminal prints the plan and stops without waiting;
// --confirm <instance-id-or-name> runs without a prompt, and a mismatch is a
// usage error raised before anything changes. A confirmed run prints the plan as
// one line. Exit codes stay 0, 1 and 2.

var gateTarget = inventory.Instance{InstanceID: "i-0abc123", Name: "caltechauthors-test-v13"}

func testGate(confirm string, interactive bool) Gate {
	return Gate{
		Target:      gateTarget,
		Confirm:     confirm,
		Interactive: interactive,
		Summary:     "restore snapshot rdm-1 onto caltechauthors-test-v13",
		Plan: []string{
			"delete 62 indices matching caltechauthors-*",
			"restore snapshot rdm-1 from s3://bucket/caltechauthors-v13/",
		},
	}
}

// A reader that fails the test if anything reads from it: the no-terminal path
// must never wait for input.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("the gate read from its input although no terminal is attached or --confirm was given")
	return 0, errors.New("unexpected read")
}

func TestGate_ConfirmMatchingTheInstanceIDProceeds(t *testing.T) {
	var out bytes.Buffer
	got, err := testGate("i-0abc123", false).Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
}

func TestGate_ConfirmMatchingTheNameProceeds(t *testing.T) {
	var out bytes.Buffer
	got, err := testGate("caltechauthors-test-v13", false).Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
}

// A confirmed run prints the plan first, as one line, so a log shows what ran.
func TestGate_ConfirmedRunPrintsOneSummaryLine(t *testing.T) {
	var out bytes.Buffer
	if _, err := testGate("i-0abc123", false).Decide(&out, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one line, got %d:\n%s", len(lines), out.String())
	}
	for _, want := range []string{"restore snapshot rdm-1 onto caltechauthors-test-v13", "i-0abc123"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the line should mention %q, got: %s", want, lines[0])
		}
	}
	if strings.Contains(out.String(), "delete 62 indices") {
		t.Errorf("a confirmed run prints the one-line summary, not the whole plan:\n%s", out.String())
	}
}

func TestGate_ConfirmMismatchIsAUsageErrorBeforeAnythingIsPrinted(t *testing.T) {
	var out bytes.Buffer
	got, err := testGate("i-0other", false).Decide(&out, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a *UsageError, got: %v", err)
	}
	if got != GateDeclined {
		t.Errorf("a mismatch must not proceed, got %v", got)
	}
	for _, want := range []string{"i-0other", "i-0abc123", "caltechauthors-test-v13", "--confirm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("nothing may be printed before the mismatch is reported, got:\n%s", out.String())
	}
	if CLIExitCode(err) != 2 {
		t.Errorf("a mismatch exits 2, got %d", CLIExitCode(err))
	}
}

// An instance with no Name tag must not be matched by an empty --confirm, nor
// may a partial value match.
func TestGate_ConfirmMustEqualNotMerelyContain(t *testing.T) {
	for _, bad := range []string{"i-0abc", "caltechauthors", "i-0abc123 ", "I-0ABC123"} {
		var out bytes.Buffer
		_, err := testGate(bad, false).Decide(&out, failingReader{t}, &out)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("--confirm %q must not match; got: %v", bad, err)
		}
	}
}

func TestGate_NoTerminalPrintsThePlanAndStopsWithoutWaiting(t *testing.T) {
	var out bytes.Buffer
	got, err := testGate("", false).Decide(&out, failingReader{t}, &out)
	if err != nil {
		t.Fatalf("a dry run is not an error, got: %v", err)
	}
	if got != GateDeclined {
		t.Errorf("got %v, want GateDeclined", got)
	}
	s := out.String()
	for _, want := range []string{"delete 62 indices", "restore snapshot rdm-1 from", "Nothing was changed", "--confirm caltechauthors-test-v13"} {
		if !strings.Contains(s, want) {
			t.Errorf("the dry run should print %q, got:\n%s", want, s)
		}
	}
	if CLIExitCode(err) != 0 {
		t.Errorf("a dry run exits 0, got %d", CLIExitCode(err))
	}
}

func TestGate_DryRunHintFallsBackToTheInstanceIDWithoutAName(t *testing.T) {
	g := testGate("", false)
	g.Target = inventory.Instance{InstanceID: "i-0abc123"}
	var out bytes.Buffer
	if _, err := g.Decide(&out, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--confirm i-0abc123") {
		t.Errorf("the hint should name the instance ID when there is no Name tag, got:\n%s", out.String())
	}
}

func TestGate_TerminalPrintsThePlanThenPromptsAndProceedsOnTheExactID(t *testing.T) {
	w, in, buf := newPipeEditor("i-0abc123\n")
	got, err := testGate("", true).Decide(w, in, buf)
	if err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil\n%s", got, err, buf.String())
	}
	s := buf.String()
	plan := strings.Index(s, "delete 62 indices")
	prompt := strings.Index(s, "Enter identifier") // the prompt's title; accessible mode does not print its description
	if plan < 0 || prompt < 0 || plan > prompt {
		t.Errorf("the plan must be printed before the prompt (plan at %d, prompt at %d):\n%s", plan, prompt, s)
	}
}

func TestGate_TerminalWrongAnswerDeclinesWithoutError(t *testing.T) {
	w, in, buf := newPipeEditor("wrong-name\n")
	got, err := testGate("", true).Decide(w, in, buf)
	if err != nil {
		t.Fatalf("declining is not an error, got: %v", err)
	}
	if got != GateDeclined {
		t.Errorf("got %v, want GateDeclined", got)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("a declined prompt should say Cancelled, got:\n%s", buf.String())
	}
	if CLIExitCode(err) != 0 {
		t.Errorf("declining exits 0, got %d", CLIExitCode(err))
	}
}

// With --confirm the terminal is never asked, even when one is attached.
func TestGate_ConfirmSkipsThePromptEvenOnATerminal(t *testing.T) {
	var out bytes.Buffer
	got, err := testGate("i-0abc123", true).Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
}

func TestGate_EmptyPlanStillStatesNothingWasChanged(t *testing.T) {
	g := testGate("", false)
	g.Plan = nil
	var out bytes.Buffer
	if _, err := g.Decide(&out, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing was changed") {
		t.Errorf("got:\n%s", out.String())
	}
}

// --- options parsed per verb with a FlagSet ---

const gateTestUsage = "usage: restore-x [--confirm <instance-id-or-name>] <instance> <bucket>"

func TestParseDestructiveArgs_ConfirmSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"--confirm", "i-1", "a", "b"},
		{"-confirm", "i-1", "a", "b"},
		{"--confirm=i-1", "a", "b"},
		{"-confirm=i-1", "a", "b"},
	} {
		opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, args)
		if err != nil {
			t.Errorf("%v: unexpected error: %v", args, err)
			continue
		}
		if opts.Confirm != "i-1" || fmt.Sprint(pos) != "[a b]" {
			t.Errorf("%v: got confirm=%q positional=%v; want i-1, [a b]", args, opts.Confirm, pos)
		}
	}
}

func TestParseDestructiveArgs_NoOptionsLeavesEveryWordPositional(t *testing.T) {
	opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"a", "b", "c"})
	if err != nil || opts.Confirm != "" || fmt.Sprint(pos) != "[a b c]" {
		t.Fatalf("got %+v %v %v", opts, pos, err)
	}
}

// A leaf given only options still has a run to do: options with no positional
// words must parse, leaving arity to the leaf.
func TestParseDestructiveArgs_OptionsOnlyIsAValidParse(t *testing.T) {
	opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"--confirm", "i-1"})
	if err != nil || opts.Confirm != "i-1" || len(pos) != 0 {
		t.Fatalf("got %+v %v %v", opts, pos, err)
	}
}

// Go's flag parsing stops at the first non-flag word, so options come first;
// one after a positional word is just a positional word, and the leaf's arity
// check then refuses it. Documented by this test, not worked around.
func TestParseDestructiveArgs_OptionsAfterAPositionalWordAreNotOptions(t *testing.T) {
	opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"a", "--confirm", "i-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Confirm != "" || fmt.Sprint(pos) != "[a --confirm i-1]" {
		t.Errorf("got confirm=%q positional=%v", opts.Confirm, pos)
	}
}

func TestParseDestructiveArgs_UnknownOptionIsAUsageError(t *testing.T) {
	_, _, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"--force", "a"})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a *UsageError, got: %v", err)
	}
	for _, want := range []string{"restore-x", "force", gateTestUsage} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	if CLIExitCode(err) != 2 {
		t.Errorf("exit %d, want 2", CLIExitCode(err))
	}
}

func TestParseDestructiveArgs_ConfirmWithoutAValueIsAUsageError(t *testing.T) {
	_, _, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"--confirm"})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("want a *UsageError, got: %v", err)
	}
}

func TestParseDestructiveArgs_HelpReturnsTheLeafUsageAndExitsZero(t *testing.T) {
	for _, flagName := range []string{"--help", "-help", "-h"} {
		_, _, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{flagName})
		var h *HelpRequested
		if !errors.As(err, &h) {
			t.Errorf("%s: want a *HelpRequested, got: %v", flagName, err)
			continue
		}
		if !strings.Contains(h.Usage, gateTestUsage) || !strings.Contains(h.Usage, "--confirm") {
			t.Errorf("%s: the help should carry the leaf usage and describe --confirm, got: %q", flagName, h.Usage)
		}
		if CLIExitCode(err) != 0 {
			t.Errorf("%s: help exits 0, got %d", flagName, CLIExitCode(err))
		}
	}
}

func TestCLIExitCode(t *testing.T) {
	if got := CLIExitCode(nil); got != 0 {
		t.Errorf("nil -> %d, want 0", got)
	}
	if got := CLIExitCode(errors.New("the action failed")); got != 1 {
		t.Errorf("an action failure -> %d, want 1", got)
	}
	if got := CLIExitCode(fmt.Errorf("wrapped: %w", &UsageError{Msg: "bad"})); got != 2 {
		t.Errorf("a wrapped usage error -> %d, want 2", got)
	}
	if got := CLIExitCode(fmt.Errorf("wrapped: %w", &HelpRequested{Usage: "u"})); got != 0 {
		t.Errorf("a wrapped help request -> %d, want 0", got)
	}
}
