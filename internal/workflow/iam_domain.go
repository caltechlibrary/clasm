package workflow

import (
	"context"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// iamRoleRows resolves each summary's SSM-capability (reusing
// roleHasSSMPermissions, Phase 20.33) and converts to ui.IAMRoleRow --
// the testable core of ShowIAMRoles, since ui.DisplayIAMRoles's
// subsequent call to tui.RunListView is a real bubbletea Program that
// can't be pipe-tested (same accepted limitation as showAllTags's own
// doc comment).
func iamRoleRows(ctx context.Context, client awsclient.IAMAPI, summaries []inventory.IAMRoleSummary) ([]ui.IAMRoleRow, error) {
	rows := make([]ui.IAMRoleRow, len(summaries))
	for i, s := range summaries {
		capable, err := roleHasSSMPermissions(ctx, client, s.Name)
		if err != nil {
			return nil, err
		}
		rows[i] = ui.IAMRoleRow{
			Name:       s.Name,
			CreateDate: s.CreateDate,
			Origin:     s.Origin,
			DLDOwned:   s.DLDOwned,
			SSMCapable: capable,
		}
	}
	return rows, nil
}

// ShowIAMRoles fetches every IAM role in the account, resolves Origin/
// DLD-ownership/SSM-capability, and displays them in the shared
// List-tier component, sorted by CreateDate descending (DESIGN.md, "IAM
// Profile & Role Management Domain").
func ShowIAMRoles(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	summaries, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	rows, err := iamRoleRows(ctx, client, summaries)
	if err != nil {
		return err
	}
	return ui.DisplayIAMRoles(ctx, rows)
}

// ShowIAMInstanceProfiles fetches every IAM instance profile in the
// account and displays them in the shared List-tier component, same
// shape as ShowIAMRoles.
func ShowIAMInstanceProfiles(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	summaries, err := inventory.ListIAMInstanceProfileSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	return ui.DisplayIAMInstanceProfiles(ctx, summaries)
}

// ShowIAMPolicies fetches every customer-managed IAM policy in the
// account and displays them in the shared List-tier component, same
// shape as ShowIAMRoles.
func ShowIAMPolicies(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	summaries, err := inventory.ListIAMPolicySummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	return ui.DisplayIAMPolicies(ctx, summaries)
}
