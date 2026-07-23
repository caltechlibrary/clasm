package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
)

// yesNo renders a bool as the same yes/no shape this package's other
// boolean-ish columns already use (see staticWebsiteLabel).
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatIAMTime renders an IAM resource's CreateDate for display --
// IAM's CreateDate is a real time.Time (unlike inventory.Image's
// pre-formatted CreationDate string), so formatting happens here.
func formatIAMTime(t time.Time) string {
	return t.Format("2006-01-02 15:04")
}

// IAMRoleRow is one row in the IAM domain's Roles list -- an
// inventory.IAMRoleSummary plus SSM-capability, which is a
// workflow-layer concern (roleHasSSMPermissions) layered on top rather
// than fetched as part of the summary itself (see inventory.IAMRoleSummary's
// doc comment).
type IAMRoleRow struct {
	Name       string
	CreateDate time.Time
	Origin     string
	DLDOwned   bool
	SSMCapable bool
}

// iamRoleListViewConfig builds a tui.ListViewConfig from rows -- see
// imageListViewConfig's doc comment for the extraction rationale.
func iamRoleListViewConfig(rows []IAMRoleRow) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s",
		padRight("ROLE NAME", 32),
		padRight("ORIGIN", 16),
		padRight("CREATED", 17),
		"SSM-CAPABLE")

	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s %s %s %s",
			padRight(truncate(r.Name, 32), 32),
			padRight(truncate(r.Origin, 16), 16),
			padRight(formatIAMTime(r.CreateDate), 17),
			yesNo(r.SSMCapable))
	}

	return tui.ListViewConfig{
		Title:        "IAM Roles",
		Header:       header,
		Rows:         out,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayIAMRoles shows IAM roles in the shared List-tier component
// (DESIGN.md, "IAM Profile & Role Management Domain") -- same
// reachability convention as DisplayInstances.
func DisplayIAMRoles(ctx context.Context, rows []IAMRoleRow) error {
	return tui.RunListView(ctx, iamRoleListViewConfig(rows))
}

// iamInstanceProfileListViewConfig builds a tui.ListViewConfig from
// summaries -- see imageListViewConfig's doc comment for the extraction
// rationale.
func iamInstanceProfileListViewConfig(summaries []inventory.IAMInstanceProfileSummary) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s",
		padRight("PROFILE NAME", 32),
		padRight("ORIGIN", 16),
		"CREATED")

	rows := make([]string, len(summaries))
	for i, p := range summaries {
		rows[i] = fmt.Sprintf("%s %s %s",
			padRight(truncate(p.Name, 32), 32),
			padRight(truncate(p.Origin, 16), 16),
			formatIAMTime(p.CreateDate))
	}

	return tui.ListViewConfig{
		Title:        "IAM Instance Profiles",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayIAMInstanceProfiles shows IAM instance profiles in the shared
// List-tier component -- same reachability convention as
// DisplayInstances.
func DisplayIAMInstanceProfiles(ctx context.Context, summaries []inventory.IAMInstanceProfileSummary) error {
	return tui.RunListView(ctx, iamInstanceProfileListViewConfig(summaries))
}

// iamPolicyListViewConfig builds a tui.ListViewConfig from summaries --
// see imageListViewConfig's doc comment for the extraction rationale.
func iamPolicyListViewConfig(summaries []inventory.IAMPolicySummary) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s",
		padRight("POLICY NAME", 28),
		padRight("ORIGIN", 16),
		padRight("CREATED", 17),
		"ARN")

	rows := make([]string, len(summaries))
	for i, p := range summaries {
		rows[i] = fmt.Sprintf("%s %s %s %s",
			padRight(truncate(p.Name, 28), 28),
			padRight(truncate(p.Origin, 16), 16),
			padRight(formatIAMTime(p.CreateDate), 17),
			p.ARN)
	}

	return tui.ListViewConfig{
		Title:        "IAM Policies (customer-managed)",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayIAMPolicies shows customer-managed IAM policies in the shared
// List-tier component -- same reachability convention as
// DisplayInstances.
func DisplayIAMPolicies(ctx context.Context, summaries []inventory.IAMPolicySummary) error {
	return tui.RunListView(ctx, iamPolicyListViewConfig(summaries))
}
