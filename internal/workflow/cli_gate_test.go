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

// confirmsOf turns the single --confirm value most tests give into the gate's
// list; "" means the flag was not given.
func confirmsOf(confirm string) []string {
	if confirm == "" {
		return nil
	}
	return []string{confirm}
}

func testGate(confirm string, interactive bool) Gate {
	return Gate{
		Target:      gateTarget,
		Confirms:    confirmsOf(confirm),
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
		if fmt.Sprint(opts.Confirms) != "[i-1]" || fmt.Sprint(pos) != "[a b]" {
			t.Errorf("%v: got confirm=%v positional=%v; want [i-1], [a b]", args, opts.Confirms, pos)
		}
	}
}

func TestParseDestructiveArgs_NoOptionsLeavesEveryWordPositional(t *testing.T) {
	opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"a", "b", "c"})
	if err != nil || len(opts.Confirms) != 0 || fmt.Sprint(pos) != "[a b c]" {
		t.Fatalf("got %+v %v %v", opts, pos, err)
	}
}

// A leaf given only options still has a run to do: options with no positional
// words must parse, leaving arity to the leaf.
func TestParseDestructiveArgs_OptionsOnlyIsAValidParse(t *testing.T) {
	opts, pos, err := ParseDestructiveArgs("restore-x", gateTestUsage, []string{"--confirm", "i-1"})
	if err != nil || fmt.Sprint(opts.Confirms) != "[i-1]" || len(pos) != 0 {
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
	if len(opts.Confirms) != 0 || fmt.Sprint(pos) != "[a --confirm i-1]" {
		t.Errorf("got confirm=%v positional=%v", opts.Confirms, pos)
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

// The gate also guards targets that are not instances (DR-0186): an IAM role or
// instance profile is identified by its name alone, and --confirm, the prompt
// and the "re-run with" hint all use that name.
func namedGate(confirm string, interactive bool) Gate {
	return Gate{
		Name: "ec2-test-restore-role", Kind: "IAM role", Confirms: confirmsOf(confirm), Interactive: interactive,
		Summary: "delete role ec2-test-restore-role",
		Plan:    []string{"detach policy ec2-test-restore-role-policy", "delete role ec2-test-restore-role"},
	}
}

func TestGate_NamedTargetConfirmProceedsOnTheNameOnly(t *testing.T) {
	var out bytes.Buffer
	got, err := namedGate("ec2-test-restore-role", false).Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
	if !strings.Contains(out.String(), "delete role ec2-test-restore-role") {
		t.Errorf("a confirmed run prints the summary line, got %q", out.String())
	}
}

func TestGate_NamedTargetMismatchIsAUsageErrorNamingTheKind(t *testing.T) {
	var out bytes.Buffer
	got, err := namedGate("another-role", false).Decide(&out, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) || got != GateDeclined {
		t.Fatalf("got %v, %v; want GateDeclined and a *UsageError", got, err)
	}
	if !strings.Contains(ue.Msg, "IAM role") || !strings.Contains(ue.Msg, "ec2-test-restore-role") || strings.Contains(ue.Msg, "instance") {
		t.Errorf("the message should name the kind and the name, not an instance: %q", ue.Msg)
	}
	if out.Len() != 0 {
		t.Errorf("nothing may be printed before a mismatch is refused, got %q", out.String())
	}
}

func TestGate_NamedTargetNoTerminalPrintsThePlanAndTheNameHint(t *testing.T) {
	var out bytes.Buffer
	got, err := namedGate("", false).Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateDeclined {
		t.Fatalf("got %v, %v; want GateDeclined, nil", got, err)
	}
	for _, want := range []string{"Plan for IAM role ec2-test-restore-role", "detach policy", "Nothing was changed", "--confirm ec2-test-restore-role"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestGate_NamedTargetTerminalPromptAcceptsTheName(t *testing.T) {
	w, in, buf := newPipeEditor("ec2-test-restore-role\n")
	if got, err := namedGate("", true).Decide(w, in, buf); err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil\n%s", got, err, buf.String())
	}
	w, in, buf = newPipeEditor("wrong\n")
	if got, err := namedGate("", true).Decide(w, in, buf); err != nil || got != GateDeclined || !strings.Contains(buf.String(), "Cancelled") {
		t.Fatalf("a wrong answer must decline: got %v, %v\n%s", got, err, buf.String())
	}
}

func TestParseDestructiveNamedArgs_HelpDescribesTheNameNotAnInstance(t *testing.T) {
	_, _, err := ParseDestructiveNamedArgs("delete-role", "usage: x", "role name", []string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) {
		t.Fatalf("want *HelpRequested, got %v", err)
	}
	if !strings.Contains(h.Usage, "--confirm <role name>") || strings.Contains(h.Usage, "instance") {
		t.Errorf("help = %q", h.Usage)
	}
	opts, words, err := ParseDestructiveNamedArgs("delete-role", "usage: x", "role name", []string{"--confirm", "r", "r"})
	if err != nil || fmt.Sprint(opts.Confirms) != "[r]" || len(words) != 1 {
		t.Errorf("got %+v %v %v", opts, words, err)
	}
}

// --confirm is repeatable (DR-0187). A form with a second kind of thing to confirm
// -- the writable buckets of Restore SQL's location guard -- sets AllowExtras and
// Required: the first value names the target as always, and each Required name
// needs a --confirm of its own. Without AllowExtras any value but the target is
// the old mismatch usage error.

func bucketGate(confirms []string, interactive bool, required ...string) Gate {
	g := testGate("", interactive)
	g.Confirms, g.AllowExtras, g.Required = confirms, true, required
	return g
}

func TestGate_WithoutAllowExtrasAnExtraValueIsAMismatch(t *testing.T) {
	g := testGate("", false)
	g.Confirms = []string{"i-0abc123", "some-bucket"}
	var out bytes.Buffer
	got, err := g.Decide(&out, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) || got != GateDeclined || !strings.Contains(ue.Msg, `"some-bucket"`) {
		t.Fatalf("got %v, %v; want a mismatch usage error naming the extra value", got, err)
	}
	if g.CheckConfirm() == nil {
		t.Errorf("CheckConfirm must refuse an extra value too, so a form fails before any AWS call")
	}
}

func TestGate_RepeatedConfirmNamingTheTargetTwiceIsFine(t *testing.T) {
	g := testGate("", false)
	g.Confirms = []string{"i-0abc123", "caltechauthors-test-v13"} // the ID and the name: both name the target
	var out bytes.Buffer
	if got, err := g.Decide(&out, failingReader{t}, &out); err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
}

func TestGate_ConfirmsWithoutTheTargetAreAUsageErrorBeforeAnyCall(t *testing.T) {
	g := bucketGate([]string{"bucket-a"}, false, "bucket-a")
	err := g.CheckConfirm()
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "i-0abc123") {
		t.Fatalf("want a usage error naming the target's ID, got %v", err)
	}
}

func TestGate_AllWritableBucketsConfirmedProceeds(t *testing.T) {
	var out bytes.Buffer
	g := bucketGate([]string{"i-0abc123", "bucket-a", "bucket-b"}, false, "bucket-a", "bucket-b")
	if got, err := g.Decide(&out, failingReader{t}, &out); err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil", got, err)
	}
	for _, want := range []string{"--confirm i-0abc123", "--confirm bucket-a", "--confirm bucket-b"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("a confirmed run's log line should show every confirm, missing %q:\n%s", want, out.String())
		}
	}
}

// The refusal: the target is confirmed but a writable bucket is not. Exit 1 (the
// state forbids it, not a mistyped command), the plan is printed first, nothing
// is changed, and the message names what to add.
func TestGate_UnconfirmedWritableBucketRefusesWithExitOne(t *testing.T) {
	var out bytes.Buffer
	g := bucketGate([]string{"i-0abc123", "bucket-a"}, false, "bucket-a", "bucket-b")
	got, err := g.Decide(&out, failingReader{t}, &out)
	if err == nil || got != GateDeclined || CLIExitCode(err) != 1 {
		t.Fatalf("got %v, %v (exit %d); want GateDeclined and an exit-1 error", got, err, CLIExitCode(err))
	}
	if !strings.Contains(err.Error(), "bucket-b") || !strings.Contains(err.Error(), "--confirm bucket-b") || strings.Contains(err.Error(), "--confirm bucket-a") {
		t.Errorf("the error should name only the unconfirmed bucket and the flag to add: %v", err)
	}
	if !strings.Contains(out.String(), "Plan for") {
		t.Errorf("the plan is printed before the refusal:\n%s", out.String())
	}
}

func TestGate_ValueThatIsNeitherTheTargetNorAWritableBucketIsAUsageError(t *testing.T) {
	var out bytes.Buffer
	g := bucketGate([]string{"i-0abc123", "typo-bucket"}, false, "bucket-a")
	_, err := g.Decide(&out, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) || CLIExitCode(err) != 2 || !strings.Contains(ue.Msg, `"typo-bucket"`) || !strings.Contains(ue.Msg, "bucket-a") {
		t.Fatalf("want a usage error naming the stray value and the writable buckets, got %v", err)
	}
	// The command line is wrong, so that wins over a missing confirm.
	g = bucketGate([]string{"i-0abc123", "typo-bucket"}, false, "bucket-a", "bucket-b")
	if _, err = g.Decide(&out, failingReader{t}, &out); CLIExitCode(err) != 2 {
		t.Errorf("a stray value is exit 2 even when a bucket is also unconfirmed, got %d", CLIExitCode(err))
	}
}

func TestGate_AnExtraValueWhenNothingIsWritableIsAUsageError(t *testing.T) {
	var out bytes.Buffer
	g := bucketGate([]string{"i-0abc123", "bucket-a"}, false) // nothing flagged
	_, err := g.Decide(&out, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "no bucket") {
		t.Fatalf("want a usage error saying no bucket needs confirming, got %v", err)
	}
}

func TestGate_NoConfirmDryRunHintListsTheTargetAndEveryWritableBucket(t *testing.T) {
	var out bytes.Buffer
	g := bucketGate(nil, false, "bucket-a", "bucket-b")
	got, err := g.Decide(&out, failingReader{t}, &out)
	if err != nil || got != GateDeclined || CLIExitCode(err) != 0 {
		t.Fatalf("a dry run is exit 0, got %v, %v", got, err)
	}
	if !strings.Contains(out.String(), "--confirm caltechauthors-test-v13 --confirm bucket-a --confirm bucket-b") {
		t.Errorf("the hint should give the whole command to apply:\n%s", out.String())
	}
}

func TestGate_TerminalAsksForTheTargetThenEachWritableBucket(t *testing.T) {
	w, in, buf := newPipeEditor("i-0abc123\nbucket-a\n")
	if got, err := bucketGate(nil, true, "bucket-a").Decide(w, in, buf); err != nil || got != GateProceed {
		t.Fatalf("got %v, %v; want GateProceed, nil\n%s", got, err, buf.String())
	}
	w, in, buf = newPipeEditor("i-0abc123\nwrong\n")
	if got, err := bucketGate(nil, true, "bucket-a").Decide(w, in, buf); err != nil || got != GateDeclined || !strings.Contains(buf.String(), "Cancelled") {
		t.Fatalf("a wrong bucket answer must decline: got %v, %v\n%s", got, err, buf.String())
	}
}

// With --confirm given the terminal is not asked, so a missing bucket confirm is a
// refusal, not a prompt: a command that names its confirmations is a script.
func TestGate_ConfirmGivenNeverPromptsForABucket(t *testing.T) {
	var out bytes.Buffer
	_, err := bucketGate([]string{"i-0abc123"}, true, "bucket-a").Decide(&out, failingReader{t}, &out)
	if err == nil || CLIExitCode(err) != 1 {
		t.Fatalf("want an exit-1 refusal, got %v", err)
	}
}

func TestParseDestructiveArgs_ConfirmIsRepeatable(t *testing.T) {
	opts, words, err := ParseDestructiveArgs("restore-sql", "usage: x", []string{"--confirm", "a", "--confirm", "b", "w"})
	if err != nil || strings.Join(opts.Confirms, ",") != "a,b" || len(words) != 1 {
		t.Fatalf("got %+v %v %v", opts, words, err)
	}
	opts, _, err = ParseDestructiveArgs("restore-sql", "usage: x", []string{"w"})
	if err != nil || len(opts.Confirms) != 0 {
		t.Fatalf("no flag, no confirms: got %+v %v", opts, err)
	}
	if _, _, err = ParseDestructiveArgs("restore-sql", "usage: x", []string{"--confirm"}); err == nil {
		t.Errorf("--confirm needs a value")
	}
}

func TestParseDestructiveBucketArgs_HelpExplainsTheBucketConfirm(t *testing.T) {
	_, _, err := ParseDestructiveBucketArgs("restore-sql", "usage: x", []string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "once per bucket") || !strings.Contains(h.Usage, "write") {
		t.Fatalf("help should explain the bucket confirm: %v", err)
	}
	_, _, err = ParseDestructiveArgs("restore-os", "usage: x", []string{"--help"})
	if !errors.As(err, &h) || strings.Contains(h.Usage, "once per bucket") {
		t.Errorf("the plain help must not mention buckets: %v", err)
	}
}

// An empty --confirm value (an unset shell variable, say) is a usage error, not a
// silent dry run: a script that meant to confirm must not quietly not.
func TestParseDestructiveArgs_AnEmptyConfirmValueIsAUsageError(t *testing.T) {
	_, _, err := ParseDestructiveArgs("restore-x", "usage: x", []string{"--confirm", "", "a"})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "needs a value") {
		t.Fatalf("want a usage error, got %v", err)
	}
}
