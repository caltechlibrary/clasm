package main

import (
	"context"
	"io"

	"github.com/caltechlibrary/clasm/internal/ui"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// Usage lines of the read-only Key Management and IAM forms.
const (
	showKeyPairsUsage = "usage: clasm key-management show-key-pairs [-text|-json|-jsonl]"

	showRolesUsage                 = "usage: clasm iam show-roles [-text|-json|-jsonl]"
	showInstanceProfilesUsage      = "usage: clasm iam show-instance-profiles [-text|-json|-jsonl]"
	showPoliciesUsage              = "usage: clasm iam show-policies [-text|-json|-jsonl]"
	showRoleDetailUsage            = "usage: clasm iam show-role-detail [-text|-json] <role-name>"
	showInstanceProfileDetailUsage = "usage: clasm iam show-instance-profile-detail [-text|-json] <instance-profile-name>"
)

// runKeyMgmtIAMLeaf runs the read-only Key Management and IAM forms (DR-0177);
// handled is false for any other slug. Key pairs are the snapshot main loaded
// for the domain; the IAM forms fetch what they show and send only List*/Get*.
func runKeyMgmtIAMLeaf(ctx context.Context, out, eout io.Writer, leafSlug string, leafArgs []string, env cliEnv) (code int, handled bool) {
	switch leafSlug {
	case workflow.ShowKeyPairsCLISlug:
		return runListing(out, eout, leafSlug, showKeyPairsUsage, leafArgs, func(f ui.Format) error {
			return ui.WriteKeyPairs(out, env.keyPairs, f)
		}), true
	case workflow.ShowRolesCLISlug:
		return runListing(out, eout, leafSlug, showRolesUsage, leafArgs, func(f ui.Format) error {
			return workflow.RunShowIAMRolesCLI(ctx, out, env.iamClient, env.originTag, f)
		}), true
	case workflow.ShowInstanceProfilesCLISlug:
		return runListing(out, eout, leafSlug, showInstanceProfilesUsage, leafArgs, func(f ui.Format) error {
			return workflow.RunShowIAMInstanceProfilesCLI(ctx, out, env.iamClient, env.originTag, f)
		}), true
	case workflow.ShowPoliciesCLISlug:
		return runListing(out, eout, leafSlug, showPoliciesUsage, leafArgs, func(f ui.Format) error {
			return workflow.RunShowIAMPoliciesCLI(ctx, out, env.iamClient, env.originTag, f)
		}), true
	case workflow.ShowRoleDetailCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showRoleDetailUsage, workflow.ComputeAllowNone, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunShowIAMRoleDetailCLI(ctx, out, env.iamClient, words[0], opts.Format)), true
	case workflow.ShowInstanceProfileDetailCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showInstanceProfileDetailUsage, workflow.ComputeAllowNone, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunShowIAMInstanceProfileDetailCLI(ctx, out, env.iamClient, words[0], opts.Format)), true
	}
	return 0, false
}
