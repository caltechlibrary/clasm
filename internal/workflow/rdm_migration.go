package workflow

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/recipe"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// RDMMigrationDomainCLISlug is the RDM Migration domain's cliSlug (DR-0202,
// DR-0204): the steps of moving an RDM instance to a new version.
const RDMMigrationDomainCLISlug = "rdm-migration"

// RunRecipeCLISlug is the run-recipe leaf's cliSlug.
const RunRecipeCLISlug = "run-recipe"

// runRecipeUsage is the form's usage line.
const runRecipeUsage = "usage: clasm " + RDMMigrationDomainCLISlug + " " + RunRecipeCLISlug +
	" [-text|-json] [--apply] [--from <step>] [--confirm <instance-id-or-name>]... <instance-name-or-id> <recipe-dir> [NAME=value]..."

const runRecipeHelp = runRecipeUsage + `

Runs an upgrade recipe (DR-0202): the NN_name.sh scripts in <recipe-dir>, in order, on the
instance over SSM. Each script starts with header lines:

  # step: NAME                          required; the name --from and the log use
  # changes: nothing|database|indices|host   required; a comma list is allowed
  # timeout: SECONDS                    default 600
  # detached: yes                       run in the background and poll it
  # user: root|ubuntu                   default root

Without --apply nothing is run: the plan is printed, with what the production gate would
stop. With it, each step is run and leaves one JSON line (-json puts those lines on stdout
and the progress on stderr); the run stops at the first failure and names the step to
resume from with --from. A step prints "COUNT name=value" for a count the log should
carry; nothing else of its output is copied into the log.

Parameters are NAME=value (capitals and underscores; the value letters, digits and . _ : / @ = + , -).

On an instance whose live Environment tag is production or staged, a step that changes
the database or indices needs --confirm <instance-id-or-name>.

Exit status: 0 ok; 1 a refused gate, or a step that exited 1; 2 usage; 65 a bad recipe, or a
step that exited 65; 66 the recipe directory is missing; 69 the instance cannot be reached,
or a step timed out; 70 a step exited with a code outside the table; other codes are the
step's own, per the workspace table.
`

// RunRecipeArgs is a parsed run-recipe command line.
type RunRecipeArgs struct {
	Instance, Dir string
	Params        []recipe.Param
	Apply         bool
	From          string
	Confirms      []string
	Format        ui.Format
}

// ParseRunRecipeArgs parses the run-recipe form's arguments. Any problem is a
// *UsageError, raised before AWS is touched; --help is a *HelpRequested.
//
// @param args {[]string} the words after the leaf name
// @returns {RunRecipeArgs} the parsed command line
// @returns {error} a *UsageError, or a *HelpRequested for --help
// @example
//
//	a, err := ParseRunRecipeArgs([]string{"--apply", "box", "upgrade/v13-to-v14", "SNAP=rdm-1"})
func ParseRunRecipeArgs(args []string) (RunRecipeArgs, error) {
	usage := func(format string, a ...any) error {
		return &UsageError{Msg: fmt.Sprintf("%s: %s\n%s", RunRecipeCLISlug, fmt.Sprintf(format, a...), runRecipeUsage)}
	}
	var a RunRecipeArgs
	fs := flag.NewFlagSet(RunRecipeCLISlug, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var asText, asJSON bool
	fs.BoolVar(&asText, "text", false, "")
	fs.BoolVar(&asJSON, "json", false, "")
	fs.BoolVar(&a.Apply, "apply", false, "")
	fs.StringVar(&a.From, "from", "", "")
	fs.Func("confirm", "", func(v string) error { a.Confirms = append(a.Confirms, v); return nil })
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return RunRecipeArgs{}, &HelpRequested{Usage: runRecipeHelp}
		}
		return RunRecipeArgs{}, usage("%v", err)
	}
	if asText && asJSON {
		return RunRecipeArgs{}, usage("-text and -json are exclusive")
	}
	a.Format = ui.FormatText
	if asJSON {
		a.Format = ui.FormatJSON
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return RunRecipeArgs{}, usage("an instance and a recipe directory are required")
	}
	a.Instance, a.Dir = rest[0], rest[1]
	params, err := recipe.ParseParams(rest[2:])
	if err != nil {
		return RunRecipeArgs{}, usage("%v", err)
	}
	a.Params = params
	return a, nil
}

// recipeCLIError turns an error from the recipe package into the error the CLI
// reports: a usage problem stays a *UsageError, any other recognised class
// carries its DR-0014 exit code, and an error the package does not own passes
// through.
func recipeCLIError(err error) error {
	if err == nil {
		return nil
	}
	code, _, ok := recipe.Classify(err)
	switch {
	case !ok:
		return err
	case code == 2:
		return &UsageError{Msg: err.Error()}
	}
	return &ExitCodeError{Code: code, Err: err}
}

// RunRecipeCLI runs an upgrade recipe on an EC2 instance over SSM (DR-0202).
// It loads and validates the recipe before any AWS call, reads the instance's
// live Environment tag (production and staged are protected, DR-0203), and
// hands the rest to recipe.Run. In -json mode stdout carries one JSON line per
// step and the progress goes to eout; otherwise the progress goes to out.
//
// @param ctx {context.Context} cancels the run
// @param out {io.Writer} stdout
// @param eout {io.Writer} stderr
// @param ssmClients {map[string]awsclient.SSMAPI} SSM clients by region
// @param ec2Clients {map[string]awsclient.EC2API} EC2 clients by region
// @param instances {[]inventory.Instance} the inventory the instance is resolved against
// @param a {RunRecipeArgs} the parsed command line
// @param poll {time.Duration} how often a detached step is polled
// @returns {error} nil, a *UsageError, or an *ExitCodeError carrying the class's code
// @example
//
//	err := RunRecipeCLI(ctx, os.Stdout, os.Stderr, ssmClients, ec2Clients, instances, args, 5*time.Second)
func RunRecipeCLI(ctx context.Context, out, eout io.Writer, ssmClients map[string]awsclient.SSMAPI, ec2Clients map[string]awsclient.EC2API, instances []inventory.Instance, a RunRecipeArgs, poll time.Duration) error {
	steps, ignored, err := recipe.Load(a.Dir)
	if err != nil {
		return recipeCLIError(err)
	}
	inst, err := resolveInstanceForCLI(a.Instance, instances)
	if err != nil {
		return err
	}
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	ec2Client, err := resolveEC2(ec2Clients, inst.Region)
	if err != nil {
		return err
	}
	live, err := describeLiveInstance(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return classifyUnavailable(fmt.Errorf("reading the Environment tag of %s: %w", inst.InstanceID, err))
	}
	opts := recipe.Options{
		Params: a.Params, Apply: a.Apply, From: a.From, Confirm: a.Confirms,
		Protected: isProtectedInstance(live), HostNames: []string{inst.InstanceID, inst.Name},
		Poll: poll, Text: out,
	}
	if a.Format == ui.FormatJSON {
		opts.Text, opts.JSONL = eout, out
	}
	if len(ignored) > 0 && !a.Apply {
		fmt.Fprintf(opts.Text, "Ignored files (not NN_name.sh): %v\n", ignored)
	}
	_, err = recipe.Run(ctx, NewSSMExecutor(ssmClient, inst.InstanceID), steps, opts)
	return recipeCLIError(err)
}

// RunRDMMigrationMenu is the interactive entry into the RDM Migration domain. Its
// forms are CLI-only until a TUI shell over them is built, so it says how to run
// one and returns to the picker.
//
// @param ctx {context.Context} unused
// @param w {io.Writer} receives the hint
// @returns {error} ErrBackToDomainPicker
// @example
//
//	err := RunRDMMigrationMenu(ctx, os.Stdout)
func RunRDMMigrationMenu(ctx context.Context, w io.Writer) error {
	fmt.Fprintln(w, "RDM Migration has command-line forms only for now:")
	fmt.Fprintln(w, "  clasm rdm-migration run-recipe [--apply] [--from STEP] [--confirm INSTANCE] INSTANCE RECIPE-DIR [NAME=value]...")
	fmt.Fprintln(w, "Run it with --help for the recipe format. Without --apply it prints the plan and runs nothing.")
	return ErrBackToDomainPicker
}
