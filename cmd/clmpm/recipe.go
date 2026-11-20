package main

import (
	"flag"
	"fmt"

	"github.com/caltechlibrary/clasm/internal/recipe"
)

// recipeFlags registers run-recipe's options: -apply runs the recipe (without
// it the verb prints the plan and runs nothing), -from resumes at a step, and
// -confirm permits the steps that change the database or indices.
func recipeFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.apply, "apply", false, "run the steps; without this the command prints the plan")
	fs.StringVar(&o.from, "from", "", "resume at this step")
	fs.BoolVar(&o.confirm, "confirm", false, "permit steps that change the database or indices")
}

// doRunRecipe runs an upgrade recipe (DR-0202) on a Multipass VM. A VM has no
// Environment tag, so every VM is protected for database and index steps:
// those need -confirm, as the restore verbs do (DR-0207).
//
// @param d {deps} the command's dependencies
// @param o {*options} the parsed flags
// @param args {[]string} VM, the recipe directory, then NAME=value parameters
// @param out {cmdOut} where results go
// @returns {error} a *recipe error, which classify maps to an exit code, or nil
func doRunRecipe(d deps, o *options, args []string, out cmdOut) error {
	vm, dir := args[0], args[1]
	params, err := recipe.ParseParams(args[2:])
	if err != nil {
		return err
	}
	steps, ignored, err := recipe.Load(dir)
	if err != nil {
		return err
	}
	opts := recipe.Options{
		Params: params, Apply: o.apply, From: o.from, Protected: true, HostNames: []string{vm},
		RemoteDir: d.recipeRemoteDir, Text: out.w,
	}
	if o.confirm {
		opts.Confirm = []string{vm}
	}
	if out.json {
		opts.Text, opts.JSONL = out.errw, out.w
	}
	if len(ignored) > 0 && !o.apply {
		fmt.Fprintf(opts.Text, "Ignored files (not NN_name.sh): %v\n", ignored)
	}
	_, err = recipe.Run(d.ctx, d.newExecutor(vm), steps, opts)
	return err
}

// recipeClassified reports whether the recipe package owns err.
func recipeClassified(err error) bool {
	_, _, ok := recipe.Classify(err)
	return ok
}

// recipeCode returns the exit code and class of an error the recipe package owns.
func recipeCode(err error) (int, string) {
	code, class, _ := recipe.Classify(err)
	return code, class
}
