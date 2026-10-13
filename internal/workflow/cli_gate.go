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
	// Name and Kind replace Target for a target that is not an instance (an IAM
	// role or instance profile, DR-0186): Name is the one identifier --confirm
	// and the prompt accept, Kind says what it is ("IAM role") in the plan
	// header and the mismatch message. Leave both empty for an instance.
	Name, Kind string
	// Confirms are the values of --confirm, in order; none if the flag was not
	// given. The flag is repeatable (DR-0187): one value names the target, and a
	// form with AllowExtras takes one more per Required name.
	Confirms []string
	// AllowExtras lets Confirms hold values other than the target's, which must
	// then each be in Required. Without it any such value is the usual mismatch
	// usage error. Restore SQL sets it for its writable-bucket guard.
	AllowExtras bool
	// Required are the names, besides the target, that each need a --confirm of
	// their own before the action may run -- the buckets a restored database's
	// file locations name that this instance can write to (DR-0187). Only read
	// when AllowExtras is set.
	Required []string
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
	if g.Name != "" {
		return []string{g.Name}
	}
	var out []string
	for _, v := range []string{g.Target.InstanceID, g.Target.Name} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// targetFlag is how the target is named in a --confirm: the Name when there is
// one, since it reads better in a crontab, otherwise the ID.
func (g Gate) targetFlag() string {
	switch {
	case g.Name != "":
		return g.Name
	case g.Target.Name != "":
		return g.Target.Name
	}
	return g.Target.InstanceID
}

// applyHint is the flags a dry run tells the operator to add: one --confirm for
// the target and, when buckets need confirming, one for each.
func (g Gate) applyHint() string {
	flags := []string{"--confirm " + g.targetFlag()}
	if g.AllowExtras {
		for _, b := range g.Required {
			flags = append(flags, "--confirm "+b)
		}
	}
	return strings.Join(flags, " ")
}

// mismatchError is the usage error for a --confirm value that does not name the
// target.
func (g Gate) mismatchError(value string) error {
	if g.Name != "" {
		return &UsageError{Msg: fmt.Sprintf("--confirm %q does not match the target %s: want its name (%s), exactly", value, g.Kind, g.Name)}
	}
	name := g.Target.Name
	if name == "" {
		name = "none"
	}
	return &UsageError{Msg: fmt.Sprintf(
		"--confirm %q does not match the target instance: want its instance ID (%s) or its Name tag (%s), exactly",
		value, g.Target.InstanceID, name)}
}

// extraConfirms are the --confirm values that are not the target's.
func (g Gate) extraConfirms() []string {
	var extras []string
	for _, c := range g.Confirms {
		if !slices.Contains(g.accepted(), c) {
			extras = append(extras, c)
		}
	}
	return extras
}

// CheckConfirm reports whether the --confirm values, when given, name the target:
// nil if none was given or each equals the instance ID or Name tag exactly -- or,
// with AllowExtras, if at least one does and the rest are left for Decide to match
// against Required -- else a *UsageError. A form calls it as soon as its arguments
// are resolved, so a typo fails before any AWS call, not after the read-only calls
// that build the plan.
func (g Gate) CheckConfirm() error {
	if len(g.Confirms) == 0 {
		return nil
	}
	extras := g.extraConfirms()
	if !g.AllowExtras {
		if len(extras) > 0 {
			return g.mismatchError(extras[0])
		}
		return nil
	}
	if len(extras) == len(g.Confirms) {
		return g.mismatchError(g.Confirms[0])
	}
	return nil
}

// confirmedLine is the one line a confirmed run logs of its confirmations.
func (g Gate) confirmedLine() string {
	flags := make([]string, len(g.Confirms))
	for i, c := range g.Confirms {
		flags[i] = "--confirm " + c
	}
	return strings.Join(flags, " ")
}

// checkExtras matches the extra --confirm values against Required: a value that
// is not a Required name is a *UsageError (the command line is wrong), and a
// Required name with no value is a refusal -- an ordinary error, exit 1, because
// the state forbids the action, not a mistyped command.
func (g Gate) checkExtras() (missing []string, err error) {
	for _, e := range g.extraConfirms() {
		if slices.Contains(g.Required, e) {
			continue
		}
		if len(g.Required) == 0 {
			return nil, &UsageError{Msg: fmt.Sprintf("--confirm %q is not the target, and no bucket needs confirming for this restore", e)}
		}
		return nil, &UsageError{Msg: fmt.Sprintf("--confirm %q names neither the target nor a bucket this instance can write to (%s)", e, strings.Join(g.Required, ", "))}
	}
	for _, r := range g.Required {
		if !slices.Contains(g.Confirms, r) {
			missing = append(missing, r)
		}
	}
	return missing, nil
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
	if len(g.Confirms) > 0 {
		if err := g.CheckConfirm(); err != nil {
			return GateDeclined, err
		}
		var missing []string
		if g.AllowExtras {
			var err error
			if missing, err = g.checkExtras(); err != nil {
				return GateDeclined, err
			}
		}
		if len(missing) > 0 {
			g.printPlan(w)
			flags := make([]string, len(missing))
			for i, m := range missing {
				flags[i] = "--confirm " + m
			}
			return GateDeclined, fmt.Errorf("refused: this instance can write to %s, so each needs its own confirmation; nothing was changed. To go ahead add %s",
				strings.Join(missing, ", "), strings.Join(flags, " "))
		}
		fmt.Fprintf(w, "Confirmed (%s): %s\n", g.confirmedLine(), g.Summary)
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
	if g.AllowExtras {
		for _, bucket := range g.Required {
			fmt.Fprintf(w, "\nThis instance can write to bucket %s. Type its name to confirm.\n", bucket)
			ok, err := ConfirmDestructive([]string{bucket}, WithConfirmIO(input, output))
			if err != nil && !errors.Is(err, huh.ErrUserAborted) {
				return GateDeclined, err
			}
			if err != nil || !ok {
				fmt.Fprintln(w, "Cancelled.")
				return GateDeclined, nil
			}
		}
	}
	return GateProceed, nil
}

func (g Gate) printPlan(w io.Writer) {
	if g.Name != "" {
		fmt.Fprintf(w, "Plan for %s %s:\n", g.Kind, g.Name)
	} else {
		fmt.Fprintf(w, "Plan for %s:\n", strings.Join(g.accepted(), " / "))
	}
	for _, line := range g.Plan {
		fmt.Fprintf(w, "  - %s\n", line)
	}
}

// DestructiveOptions holds the options every destructive leaf shares.
type DestructiveOptions struct {
	// Confirms are --confirm's values, in order: the target's instance ID or Name
	// tag, and for a form with a bucket guard one more per writable bucket.
	Confirms []string
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
	return parseDestructiveArgs(leaf, usage, "instance-id-or-name",
		"The value must equal the target instance's ID or Name tag.", args)
}

// ParseDestructiveNamedArgs is ParseDestructiveArgs for a leaf whose target is
// identified by a name alone (an IAM role or instance profile): what is the
// placeholder in --confirm's help, "role-name" for example.
func ParseDestructiveNamedArgs(leaf, usage, what string, args []string) (DestructiveOptions, []string, error) {
	return parseDestructiveArgs(leaf, usage, what, "The value must equal the target's name, exactly.", args)
}

// ParseDestructiveBucketArgs is ParseDestructiveArgs for a leaf with the
// writable-bucket guard (DR-0187): --confirm is repeated, and the help says what
// each extra value is for.
func ParseDestructiveBucketArgs(leaf, usage string, args []string) (DestructiveOptions, []string, error) {
	return parseDestructiveArgs(leaf, usage, "instance-id-or-name",
		"The value must equal the target instance's ID or Name tag.\n"+
			"      Repeat it once per bucket named by a restored file location that this instance can write to:\n"+
			"      the restore refuses until each such bucket is named.", args)
}

func parseDestructiveArgs(leaf, usage, what, rule string, args []string) (DestructiveOptions, []string, error) {
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	var confirms []string
	fs.Func("confirm", "", func(v string) error {
		if v == "" {
			return errors.New("--confirm needs a value")
		}
		confirms = append(confirms, v)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return DestructiveOptions{}, nil, &HelpRequested{Usage: destructiveHelp(usage, what, rule)}
		}
		return DestructiveOptions{}, nil, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, usage)}
	}
	return DestructiveOptions{Confirms: confirms}, fs.Args(), nil
}

func destructiveHelp(usage, what, rule string) string {
	var b bytes.Buffer
	b.WriteString(usage)
	b.WriteString("\n\nOptions:\n")
	b.WriteString("  --confirm <" + what + ">\n")
	b.WriteString("      Run without prompting. " + rule + "\n")
	b.WriteString("      Without it, a terminal is asked to confirm; with no terminal the plan is printed and\n")
	b.WriteString("      nothing is changed (exit 0).\n")
	b.WriteString("  -h, --help\n      Show this help.\n")
	return b.String()
}
