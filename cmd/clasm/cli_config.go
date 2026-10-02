package main

import (
	"io"

	"github.com/caltechlibrary/clasm/internal/workflow"
)

const showCurrentConfigUsage = "usage: clasm configuration show-current-config [-text|-json]"

// runConfigurationLeaf runs the Configuration domain's one CLI form; handled is
// false for any other slug. It prints the configuration main already loaded.
func runConfigurationLeaf(out, eout io.Writer, leafSlug string, leafArgs []string, env cliEnv) (code int, handled bool) {
	if leafSlug != workflow.ShowCurrentConfigCLISlug {
		return 0, false
	}
	opts, _, err := parseLeafWords(leafSlug, showCurrentConfigUsage, workflow.ComputeAllowNone, leafArgs, 0, 0)
	if err != nil {
		return reportCLIError(out, eout, err), true
	}
	return reportCLIError(out, eout, workflow.RunShowConfigCLI(out, env.config, opts.Format)), true
}
