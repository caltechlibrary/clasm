package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// filterDLDOwnedInstanceProfiles narrows profiles to those recognized as
// DLD-owned -- same "filter, don't annotate-and-reject" precedent as
// filterDLDOwnedRoles, applied to Delete Instance Profile's own picker.
func filterDLDOwnedInstanceProfiles(profiles []inventory.IAMInstanceProfileSummary) []inventory.IAMInstanceProfileSummary {
	filtered := make([]inventory.IAMInstanceProfileSummary, 0, len(profiles))
	for _, p := range profiles {
		if p.DLDOwned {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// removeRoleFromInstanceProfile wraps iam:RemoveRoleFromInstanceProfile --
// the actual remedy for Delete Role's "still referenced by instance
// profile(s)" refusal (DECISIONS.md, "Delete Role: correct the
// wrong-remedy message, add the missing instance-profile-membership
// actions"). Associate/replace IAM instance profile (Phase 20.33) only
// ever changes an EC2 instance's *association* to a profile; it has no
// effect on this profile-to-role *membership* relationship.
func removeRoleFromInstanceProfile(ctx context.Context, client awsclient.IAMAPI, profileName, roleName string) error {
	_, err := client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	return err
}

// deleteInstanceProfile wraps iam:DeleteInstanceProfile. AWS itself
// refuses (DeleteConflict) if a role is still attached -- deliberately
// not pre-checked client-side here, matching this project's "fail loud,
// don't guess" convention.
func deleteInstanceProfile(ctx context.Context, client awsclient.IAMAPI, profileName string) error {
	_, err := client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
	return err
}

// RemoveRoleFromInstanceProfile runs the IAM domain's "Remove role from
// instance profile" action (PLAN.md Phase 20.55): pick a DLD-owned role,
// pick which instance profile it's currently a member of (if more than
// one), confirm, then remove it.
func RemoveRoleFromInstanceProfile(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return removeRoleFromInstanceProfileWorkflow(ctx, w, client, originTag, nil, nil)
}

// removeRoleFromInstanceProfileWorkflow is RemoveRoleFromInstanceProfile's
// testable core for the path reachable before pickIAMRole (Picker-tier,
// not pipe-testable). Once a role is picked, removeRoleFromInstanceProfileConfirmed
// takes over and is fully pipe-testable (Menu-tier pickComparable +
// Confirm only).
func removeRoleFromInstanceProfileWorkflow(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
	roles, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	dldRoles := filterDLDOwnedRoles(roles)
	if len(dldRoles) == 0 {
		fmt.Fprintln(w, "No DLD-owned IAM roles found.")
		return nil
	}

	role, err := pickIAMRole(ctx, "Select a DLD-owned role", "Only roles recognized as DLD-owned (via the configured Origin tag) are shown.", dldRoles)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	detail, err := fetchIAMRoleDetail(ctx, client, role.Name)
	if err != nil {
		return err
	}
	return removeRoleFromInstanceProfileConfirmed(ctx, w, client, originTag, detail, menuInput, menuOutput)
}

// removeRoleFromInstanceProfileConfirmed is
// removeRoleFromInstanceProfileWorkflow's testable core, once a role's
// detail is already resolved: defensively re-checks RequireDLDOwned,
// refuses if the role isn't a member of any instance profile (nothing to
// remove), lets the operator pick which profile if it's a member of more
// than one (Menu-tier, since a profile name is a plain, comparable
// string), gates behind a plain Confirm -- reversible, the role can
// always be re-added -- then calls removeRoleFromInstanceProfile.
func removeRoleFromInstanceProfileConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, detail IAMRoleDetail, menuInput io.Reader, menuOutput io.Writer) error {
	if err := inventory.RequireDLDOwned(detail.Tags, originTag, "role", detail.Name); err != nil {
		return err
	}

	if len(detail.ReferencedByProfiles) == 0 {
		fmt.Fprintf(w, "Role %s is not a member of any instance profile.\n", detail.Name)
		return nil
	}

	profileName, err := pickComparable(w, "Select an instance profile", fmt.Sprintf("Instance profiles role %s is currently a member of.", detail.Name), hintCancel, detail.ReferencedByProfiles, func(s string) string { return s }, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	ok, err := Confirm(fmt.Sprintf("Remove role %s from instance profile %s?", detail.Name, profileName), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := removeRoleFromInstanceProfile(ctx, client, profileName, detail.Name); err != nil {
		return err
	}

	fmt.Fprintf(w, "Removed role %s from instance profile %s.\n", detail.Name, profileName)
	return nil
}

// DeleteInstanceProfile runs the IAM domain's "Delete instance profile"
// action (PLAN.md Phase 20.55): pick a DLD-owned instance profile,
// confirm, then delete it.
func DeleteInstanceProfile(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return deleteInstanceProfileWorkflow(ctx, w, client, originTag, nil, nil)
}

// deleteInstanceProfileWorkflow is DeleteInstanceProfile's testable core
// for the path reachable before pickIAMInstanceProfile (Picker-tier, not
// pipe-testable). Once a profile is picked, deleteInstanceProfileConfirmed
// takes over and is fully pipe-testable (Menu-tier Confirm only).
func deleteInstanceProfileWorkflow(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
	profiles, err := inventory.ListIAMInstanceProfileSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	dldProfiles := filterDLDOwnedInstanceProfiles(profiles)
	if len(dldProfiles) == 0 {
		fmt.Fprintln(w, "No DLD-owned IAM instance profiles found to delete.")
		return nil
	}

	profile, err := pickIAMInstanceProfile(ctx, "Select a DLD-owned instance profile to delete", "Only instance profiles recognized as DLD-owned (via the configured Origin tag) are shown.", dldProfiles)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return deleteInstanceProfileConfirmed(ctx, w, client, originTag, profile, menuInput, menuOutput)
}

// deleteInstanceProfileConfirmed is deleteInstanceProfileWorkflow's
// testable core, once a profile is already resolved: defensively
// re-checks RequireDLDOwned, gates behind a plain Confirm -- not
// ConfirmDestructive, since deleting an unused instance profile is
// low-stakes and AWS itself hard-refuses (DeleteConflict) if a role is
// still attached, rather than clasm silently allowing a dangerous case
// through -- then calls deleteInstanceProfile.
func deleteInstanceProfileConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, profile inventory.IAMInstanceProfileSummary, menuInput io.Reader, menuOutput io.Writer) error {
	if err := inventory.RequireDLDOwned(profile.Tags, originTag, "instance profile", profile.Name); err != nil {
		return err
	}

	ok, err := Confirm(fmt.Sprintf("Delete instance profile %s?", profile.Name), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := deleteInstanceProfile(ctx, client, profile.Name); err != nil {
		return err
	}

	fmt.Fprintf(w, "Deleted instance profile %s.\n", profile.Name)
	return nil
}
