package workflow

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The shared pieces every destructive CLI form is built from (DR-0180, design
// brief destructive_cli_gate.md): one confirmation gate, per-verb option
// parsing, and the exit-code mapping. A destructive leaf's own design reduces
// to its arguments and what its plan prints; the dry-run default, the
// confirmation rule and the exit codes live here once, with their own tests, so
// no destructive form can be written without them.

// UsageError is an error in how a command was invoked -- an unknown option, the
// wrong arguments, a --confirm that does not match the target -- raised before
// any call that changes anything. The CLI exits 2 for it.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// HelpRequested is returned when a leaf was given --help. It is not a failure:
// the CLI prints Usage and exits 0.
type HelpRequested struct{ Usage string }

func (e *HelpRequested) Error() string { return e.Usage }

// CLIExitCode maps an error from a CLI form to the process exit code, keeping
// the existing convention: 0 success (a dry run and a help request included),
// 1 the action failed, 2 a usage error.
func CLIExitCode(err error) int {
	var help *HelpRequested
	var usage *UsageError
	switch {
	case err == nil, errors.As(err, &help):
		return 0
	case errors.As(err, &usage):
		return 2
	default:
		return 1
	}
}

// GateDecision is what the gate decided: proceed with the destructive action,
// or stop with nothing changed.
type GateDecision int

const (
	// GateProceed: the action is confirmed; run it.
	GateProceed GateDecision = iota
	// GateDeclined: do not run it. Either a dry run (no terminal and no
	// --confirm), a prompt that was not answered with the target, or a
	// --confirm mismatch (which is also returned as a *UsageError).
	GateDeclined
)

// Gate is the one confirmation gate every destructive CLI form shares.
type Gate struct {
	// Target is the instance the action would change. --confirm and the
	// prompt both accept its exact instance ID or its exact Name tag.
	Target inventory.Instance
	// Confirm is the value of --confirm, "" if it was not given.
	Confirm string
	// Interactive reports whether a terminal is attached. False means a
	// script: the gate never waits for input.
	Interactive bool
	// Summary is one line describing what a confirmed run does. It is what a
	// log shows of a confirmed run.
	Summary string
	// Plan is what a dry run and the terminal prompt print: one line per thing
	// that would change. Built from read-only calls.
	Plan []string
}

// accepted is what --confirm and the prompt must equal: the instance ID or the
// Name tag, exactly. An empty Name is not accepted.
func (g Gate) accepted() []string {
	var out []string
	for _, v := range []string{g.Target.InstanceID, g.Target.Name} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// applyHint is the flag a dry run tells the operator to add: the Name when
// there is one, since it reads better in a crontab, otherwise the ID.
func (g Gate) applyHint() string {
	if g.Target.Name != "" {
		return "--confirm " + g.Target.Name
	}
	return "--confirm " + g.Target.InstanceID
}

// Decide applies the gate (DR-0180):
//
//   - --confirm given: its value must equal the target's instance ID or Name
//     tag, else a *UsageError is returned before anything is printed or
//     changed. On a match it prints the Summary as one line and proceeds, with
//     no prompt even if a terminal is attached.
//   - no --confirm, terminal attached: print the plan, then ask the same
//     type-to-confirm as the TUI. The exact identifier proceeds; anything else,
//     or aborting the prompt, prints Cancelled and declines without an error.
//   - no --confirm, no terminal: print the plan and a line saying nothing was
//     changed and how to apply it, and decline -- never wait for input, so a
//     cron job cannot hang. Exit 0.
//
// input and output drive the prompt through huh's accessible-mode pipe path in
// tests and are nil in production.
func (g Gate) Decide(w io.Writer, input io.Reader, output io.Writer) (GateDecision, error) {
	if g.Confirm != "" {
		if !slices.Contains(g.accepted(), g.Confirm) {
			name := g.Target.Name
			if name == "" {
				name = "none"
			}
			return GateDeclined, &UsageError{Msg: fmt.Sprintf(
				"--confirm %q does not match the target instance: want its instance ID (%s) or its Name tag (%s), exactly",
				g.Confirm, g.Target.InstanceID, name)}
		}
		fmt.Fprintf(w, "Confirmed (--confirm %s): %s\n", g.Confirm, g.Summary)
		return GateProceed, nil
	}

	g.printPlan(w)
	if !g.Interactive {
		fmt.Fprintf(w, "\nNothing was changed. Re-run with %s to apply.\n", g.applyHint())
		return GateDeclined, nil
	}

	ok, err := ConfirmDestructive(g.accepted(), WithConfirmIO(input, output))
	if err != nil && !errors.Is(err, huh.ErrUserAborted) {
		return GateDeclined, err
	}
	if err != nil || !ok {
		fmt.Fprintln(w, "Cancelled.")
		return GateDeclined, nil
	}
	return GateProceed, nil
}

func (g Gate) printPlan(w io.Writer) {
	fmt.Fprintf(w, "Plan for %s:\n", strings.Join(g.accepted(), " / "))
	for _, line := range g.Plan {
		fmt.Fprintf(w, "  - %s\n", line)
	}
}

// DestructiveOptions holds the options every destructive leaf shares.
type DestructiveOptions struct {
	// Confirm is --confirm's value: the target's instance ID or Name tag, or "".
	Confirm string
}

// ParseDestructiveArgs parses a destructive leaf's options with a flag.FlagSet
// of its own -- options belong to the verb, as in dataset, so --help shows
// exactly that leaf's options -- and returns the positional words that follow.
// Go's flag parsing stops at the first non-flag word, so options come before
// the positional arguments (`<leaf> [options] <args...>`); an option after a
// positional word is just a positional word, and the leaf's arity check then
// refuses it.
//
// usage is the leaf's own one-line usage, repeated in every error. An unknown
// option, or --confirm without a value, is a *UsageError; --help is a
// *HelpRequested carrying the usage and a description of --confirm. Arity is
// the leaf's to check: options with no positional words parse fine, so a leaf
// given only options is still a run.
func ParseDestructiveArgs(leaf, usage string, args []string) (DestructiveOptions, []string, error) {
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	confirm := fs.String("confirm", "", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return DestructiveOptions{}, nil, &HelpRequested{Usage: destructiveHelp(usage)}
		}
		return DestructiveOptions{}, nil, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, usage)}
	}
	return DestructiveOptions{Confirm: *confirm}, fs.Args(), nil
}

func destructiveHelp(usage string) string {
	var b bytes.Buffer
	b.WriteString(usage)
	b.WriteString("\n\nOptions:\n")
	b.WriteString("  --confirm <instance-id-or-name>\n")
	b.WriteString("      Run without prompting. The value must equal the target instance's ID or Name tag.\n")
	b.WriteString("      Without it, a terminal is asked to confirm; with no terminal the plan is printed and\n")
	b.WriteString("      nothing is changed (exit 0).\n")
	b.WriteString("  -h, --help\n      Show this help.\n")
	return b.String()
}
