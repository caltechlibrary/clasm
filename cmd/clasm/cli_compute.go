package main

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/ui"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// Usage lines of the read-only Compute forms, repeated in every usage error.
const (
	showInstancesUsage       = "usage: clasm compute show-instances [-text|-json|-jsonl]"
	showAMIsUsage            = "usage: clasm compute show-amis [-text|-json|-jsonl]"
	showLaunchTemplatesUsage = "usage: clasm compute show-launch-templates [-text|-json|-jsonl]"

	showInstanceDetailUsage       = "usage: clasm compute show-instance-detail [-text|-json] <instance-name-or-id>"
	showAMIDetailUsage            = "usage: clasm compute show-ami-detail [-text|-json] <ami-name-or-id>"
	showLaunchTemplateDetailUsage = "usage: clasm compute show-launch-template-detail [-text|-json] [-versions] <template-name-or-id> [version]"
	showExportCloudInitUsage      = "usage: clasm compute show-export-cloud-init-for-an-instance-or-ami [-text|-json] [-launch-temporary-instance] <instance-or-ami-name-or-id> [file]"
)

// runComputeLeaf runs one of Compute's read-only CLI forms (DR-0177). handled is
// false when leafSlug is not one of them. Every form here is read-only: the
// listings are the snapshot refresh() just loaded and send no further call.
func runComputeLeaf(ctx context.Context, out, eout io.Writer, leafSlug string, leafArgs []string, env cliEnv) (code int, handled bool) {
	switch leafSlug {
	case workflow.ShowInstancesCLISlug:
		return runListing(out, eout, leafSlug, showInstancesUsage, leafArgs, func(f ui.Format) error {
			return ui.WriteInstances(out, env.instances, f)
		}), true
	case workflow.ShowAMIsCLISlug:
		return runListing(out, eout, leafSlug, showAMIsUsage, leafArgs, func(f ui.Format) error {
			return ui.WriteImages(out, env.images, f)
		}), true
	case workflow.ShowLaunchTemplatesCLISlug:
		return runListing(out, eout, leafSlug, showLaunchTemplatesUsage, leafArgs, func(f ui.Format) error {
			return ui.WriteLaunchTemplates(out, env.launchTemplates, f)
		}), true
	case workflow.ShowInstanceDetailCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showInstanceDetailUsage, workflow.ComputeAllowNone, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunShowInstanceDetailCLI(ctx, out, env.ec2Clients, env.instances, words[0], opts.Format)), true
	case workflow.ShowAMIDetailCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showAMIDetailUsage, workflow.ComputeAllowNone, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunShowAMIDetailCLI(ctx, out, env.ec2Clients, env.images, words[0], opts.Format)), true
	case workflow.ShowLaunchTemplateDetailCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showLaunchTemplateDetailUsage, workflow.ComputeAllowVersions, leafArgs, 1, 2)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		version := ""
		if len(words) == 2 {
			version = words[1]
		}
		return reportCLIError(out, eout, workflow.RunShowLaunchTemplateDetailCLI(ctx, out, env.ec2Clients, env.launchTemplates, words[0], version, opts.Versions, opts.Format)), true
	case workflow.ShowExportCloudInitCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showExportCloudInitUsage, workflow.ComputeAllowLaunch, leafArgs, 1, 2)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		file := ""
		if len(words) == 2 {
			file = words[1]
		}
		return reportCLIError(out, eout, workflow.RunExportCloudInitCLI(ctx, out, eout, env.ec2Clients, env.ssmClients, env.instances, env.images, words[0], file, opts)), true
	}
	return 0, false
}

// parseLeafWords parses a leaf's options and checks it got between min and max
// positional words, reporting a miscount as a usage error.
func parseLeafWords(leaf, usage string, allow workflow.ComputeAllow, args []string, min, max int) (workflow.ComputeOptions, []string, error) {
	opts, words, err := workflow.ParseComputeArgs(leaf, usage, allow, args)
	if err != nil {
		return opts, nil, err
	}
	if len(words) < min || len(words) > max {
		return opts, nil, &workflow.UsageError{Msg: fmt.Sprintf("%s: got %d arguments\n%s", leaf, len(words), usage)}
	}
	return opts, words, nil
}

// runListing parses a listing leaf's options, refuses stray words (a listing
// takes none) and writes the listing in the chosen format.
func runListing(out, eout io.Writer, leaf, usage string, args []string, write func(ui.Format) error) int {
	opts, words, err := workflow.ParseComputeArgs(leaf, usage, workflow.ComputeAllowJSONL, args)
	if err == nil && len(words) > 0 {
		err = &workflow.UsageError{Msg: fmt.Sprintf("%s: takes no arguments, got %q\n%s", leaf, words[0], usage)}
	}
	if err != nil {
		return reportCLIError(out, eout, err)
	}
	if err := write(opts.Format); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return 1
	}
	return 0
}
