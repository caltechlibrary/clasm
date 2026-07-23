package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// isAWSManagedPolicyArn reports whether arn is one of AWS's own managed
// policies (account segment literally "aws", e.g.
// "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"), as opposed to
// a customer-managed policy in this account
// ("arn:aws:iam::123456789012:policy/..."). Used to guarantee deleteIAMRole
// never deletes an AWS-owned policy -- only ever detaches it.
func isAWSManagedPolicyArn(arn string) bool {
	return strings.Contains(arn, "iam::aws:policy/")
}

// deleteIAMRole deletes detail.Name and, if it has a dedicated
// customer-managed policy created alongside it by Phase 20.39 (named
// "<role>-policy", per createIAMRoleFromTemplate's own naming
// convention) that isn't attached to anything else once detached,
// deletes that too -- per the 2026-07-23 scoping discussion ("delete
// both together" to avoid leaving an orphaned policy as new clutter).
//
// Refuses upfront if detail.ReferencedByProfiles is non-empty: AWS's
// own DeleteRole documentation warns that deleting a role or instance
// profile still associated with a running instance can break whatever
// depends on it, and detaching a role from an instance profile is
// already Compute's own, carefully-scoped "Associate/replace IAM
// instance profile" action (Phase 20.33) -- automating that
// cross-cutting side effect here would be scope creep into a workflow
// that already exists and is deliberately separate.
//
// Order matches AWS's own documented precondition list for DeleteRole
// (confirmed via the vendored SDK's doc comment, 2026-07-23): delete
// inline policies, detach managed policies, then delete the role.
// Deleting the now-unattached dedicated policy afterward has no bearing
// on whether DeleteRole itself succeeds, so it's done last.
func deleteIAMRole(ctx context.Context, client awsclient.IAMAPI, detail IAMRoleDetail) error {
	if len(detail.ReferencedByProfiles) > 0 {
		return fmt.Errorf("role %q is still referenced by instance profile(s) %s -- detach it first (Compute domain, \"Associate/replace IAM instance profile\")", detail.Name, strings.Join(detail.ReferencedByProfiles, ", "))
	}

	for _, name := range detail.InlinePolicyNames {
		if _, err := client.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{RoleName: aws.String(detail.Name), PolicyName: aws.String(name)}); err != nil {
			return fmt.Errorf("deleting inline policy %s: %w", name, err)
		}
	}

	dedicatedPolicyName := detail.Name + "-policy"
	var dedicatedPolicyArn string
	for _, p := range detail.AttachedPolicies {
		if _, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: aws.String(detail.Name), PolicyArn: aws.String(p.ARN)}); err != nil {
			return fmt.Errorf("detaching policy %s: %w", p.Name, err)
		}
		if p.Name == dedicatedPolicyName && !isAWSManagedPolicyArn(p.ARN) {
			dedicatedPolicyArn = p.ARN
		}
	}

	if _, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(detail.Name)}); err != nil {
		return fmt.Errorf("deleting role: %w", err)
	}

	if dedicatedPolicyArn != "" {
		if err := deleteIAMPolicyIfUnused(ctx, client, dedicatedPolicyArn); err != nil {
			return fmt.Errorf("deleting dedicated policy %s: %w", dedicatedPolicyName, err)
		}
	}

	return nil
}

// deleteIAMPolicyIfUnused deletes policyArn only if, after the caller
// has already detached it from the role being deleted, it isn't
// attached to any other role/user/group -- checked via
// ListEntitiesForPolicy, not assumed from clasm's own naming
// convention, since an operator could always have attached the same
// policy elsewhere by hand. Per AWS's own DeletePolicy precondition
// (confirmed via the vendored SDK's doc comment): every non-default
// version must be deleted first (DeletePolicyVersion), since DeletePolicy
// itself only removes the default version as part of removing the
// policy entirely.
func deleteIAMPolicyIfUnused(ctx context.Context, client awsclient.IAMAPI, policyArn string) error {
	entities, err := client.ListEntitiesForPolicy(ctx, &iam.ListEntitiesForPolicyInput{PolicyArn: aws.String(policyArn)})
	if err != nil {
		return err
	}
	if len(entities.PolicyRoles) > 0 || len(entities.PolicyUsers) > 0 || len(entities.PolicyGroups) > 0 {
		return nil
	}

	versions, err := client.ListPolicyVersions(ctx, &iam.ListPolicyVersionsInput{PolicyArn: aws.String(policyArn)})
	if err != nil {
		return err
	}
	for _, v := range versions.Versions {
		if v.IsDefaultVersion {
			continue
		}
		if _, err := client.DeletePolicyVersion(ctx, &iam.DeletePolicyVersionInput{PolicyArn: aws.String(policyArn), VersionId: v.VersionId}); err != nil {
			return err
		}
	}

	_, err = client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(policyArn)})
	return err
}

// filterDLDOwnedRoles narrows roles to those recognized as DLD-owned --
// the Delete Role picker shows only these, following this project's
// established "filter, don't annotate-and-reject" precedent (DECISIONS.md,
// "Filter non-SSM-capable profiles/roles from the picker, don't just
// annotate them"): there's no legitimate reason to offer deleting a
// role clasm doesn't recognize as DLD's.
func filterDLDOwnedRoles(roles []inventory.IAMRoleSummary) []inventory.IAMRoleSummary {
	filtered := make([]inventory.IAMRoleSummary, 0, len(roles))
	for _, r := range roles {
		if r.DLDOwned {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// DeleteIAMRole runs the IAM domain's "Delete Role" action (PLAN.md
// Phase 20.40): pick a DLD-owned role, show its full detail, refuse if
// it's still referenced by an instance profile, gate behind a
// type-to-confirm (the role's own name, matching this project's
// existing destructive-operation tier -- Terminate Instance, Remove
// AMI), then delete it (and its dedicated policy, if unused elsewhere).
func DeleteIAMRole(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return deleteIAMRoleWorkflow(ctx, w, client, originTag, nil, nil)
}

// deleteIAMRoleWorkflow is DeleteIAMRole's testable core for the path
// reachable before pickIAMRole (Picker-tier, not pipe-testable) -- same
// accepted limitation as viewIAMRoleDetail/viewIAMInstanceProfileDetail.
// Once a role is picked, deleteIAMRoleConfirmed takes over and is fully
// pipe-testable (Menu-tier Confirm/ConfirmDestructive only).
func deleteIAMRoleWorkflow(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
	roles, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	dldRoles := filterDLDOwnedRoles(roles)
	if len(dldRoles) == 0 {
		fmt.Fprintln(w, "No DLD-owned IAM roles found to delete.")
		return nil
	}

	role, err := pickIAMRole(ctx, "Select a DLD-owned role to delete", "Only roles recognized as DLD-owned (via the configured Origin tag) are shown.", dldRoles)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	detail, err := fetchIAMRoleDetail(ctx, client, role.Name)
	if err != nil {
		return err
	}
	return deleteIAMRoleConfirmed(ctx, w, client, originTag, detail, menuInput, menuOutput)
}

// deleteIAMRoleConfirmed is deleteIAMRoleWorkflow's testable core, once
// a role's detail is already resolved: displays it, refuses early (before
// asking for any confirmation at all) if the role is still referenced by
// an instance profile, defensively re-checks RequireDLDOwned (the picker
// already filtered to DLD-owned roles, but this is the guard's first
// real caller and belt-and-suspenders costs nothing), then gates behind
// ConfirmDestructive before calling deleteIAMRole.
func deleteIAMRoleConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, detail IAMRoleDetail, menuInput io.Reader, menuOutput io.Writer) error {
	displayIAMRoleDetail(w, detail)

	if err := inventory.RequireDLDOwned(detail.Tags, originTag, "role", detail.Name); err != nil {
		return err
	}

	if len(detail.ReferencedByProfiles) > 0 {
		fmt.Fprintf(w, "\nCannot delete: role %q is still referenced by instance profile(s): %s. Detach it first (Compute domain, \"Associate/replace IAM instance profile\").\n", detail.Name, strings.Join(detail.ReferencedByProfiles, ", "))
		return nil
	}

	ok, err := ConfirmDestructive([]string{detail.Name}, WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := deleteIAMRole(ctx, client, detail); err != nil {
		return err
	}

	fmt.Fprintf(w, "Deleted role %s.\n", detail.Name)
	return nil
}

// AttachPolicyToRole runs the IAM domain's "Attach policy to role"
// action (PLAN.md Phase 20.40): pick a DLD-owned role, pick a
// customer-managed policy, confirm, then attach it. A plain Confirm,
// not ConfirmDestructive -- unlike deleting a role, attaching a policy
// is trivially reversible via the paired Detach action below.
func AttachPolicyToRole(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return attachPolicyToRoleWorkflow(ctx, w, client, originTag, nil, nil)
}

// attachPolicyToRoleWorkflow is AttachPolicyToRole's testable core for
// the path reachable before pickIAMRole/pickIAMPolicy (Picker-tier, not
// pipe-testable). Once both are picked, attachPolicyToRoleConfirmed
// takes over and is fully pipe-testable (Menu-tier Confirm only).
func attachPolicyToRoleWorkflow(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
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

	// Not filtered to DLD-owned policies: attaching an IMSS- or
	// AWS-authored customer-managed policy to a DLD-owned role is a
	// legitimate, expected case (e.g. a shared logging policy), unlike
	// picking which *role* to modify.
	policies, err := inventory.ListIAMPolicySummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		fmt.Fprintln(w, "No customer-managed IAM policies found to attach.")
		return nil
	}

	policy, err := pickIAMPolicy(ctx, "Select a policy to attach", "", policies)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return attachPolicyToRoleConfirmed(ctx, w, client, originTag, role, policy, menuInput, menuOutput)
}

// attachPolicyToRoleConfirmed is attachPolicyToRoleWorkflow's testable
// core, once a role and policy are already resolved: defensively
// re-checks RequireDLDOwned (the picker already filtered to DLD-owned
// roles, but this is belt-and-suspenders, matching
// deleteIAMRoleConfirmed's own re-check), gates behind a plain Confirm,
// then calls AttachRolePolicy.
func attachPolicyToRoleConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, role inventory.IAMRoleSummary, policy inventory.IAMPolicySummary, menuInput io.Reader, menuOutput io.Writer) error {
	if err := inventory.RequireDLDOwned(role.Tags, originTag, "role", role.Name); err != nil {
		return err
	}

	ok, err := Confirm(fmt.Sprintf("Attach policy %s to role %s?", policy.Name, role.Name), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: aws.String(role.Name), PolicyArn: aws.String(policy.ARN)}); err != nil {
		return err
	}

	fmt.Fprintf(w, "Attached policy %s to role %s.\n", policy.Name, role.Name)
	return nil
}

// DetachPolicyFromRole runs the IAM domain's "Detach policy from role"
// action (PLAN.md Phase 20.40): pick a DLD-owned role, pick one of its
// currently attached policies, confirm, then detach it. A plain
// Confirm -- same reasoning as AttachPolicyToRole.
func DetachPolicyFromRole(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return detachPolicyFromRoleWorkflow(ctx, w, client, originTag, nil, nil)
}

// detachPolicyFromRoleWorkflow is DetachPolicyFromRole's testable core
// for the path reachable before pickIAMRole (Picker-tier, not
// pipe-testable). Once a role is picked, detachPolicyFromRoleConfirmed
// takes over and is fully pipe-testable (Menu-tier pickComparable +
// Confirm only), since IAMPolicyRef is comparable.
func detachPolicyFromRoleWorkflow(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
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
	return detachPolicyFromRoleConfirmed(ctx, w, client, originTag, detail, menuInput, menuOutput)
}

// detachPolicyFromRoleConfirmed is detachPolicyFromRoleWorkflow's
// testable core, once a role's detail is already resolved: defensively
// re-checks RequireDLDOwned, refuses if the role has no attached
// managed policies to offer, lets the operator pick which attached
// policy to detach (Menu-tier, since IAMPolicyRef is comparable), gates
// behind a plain Confirm, then calls DetachRolePolicy. Deliberately
// does not also offer deleting the now-unattached policy itself -- that
// cascade only applies to Delete Role's own dedicated policy convention
// (deleteIAMRole), not to detaching a policy that may still be in use
// elsewhere or was never clasm's to manage.
func detachPolicyFromRoleConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, detail IAMRoleDetail, menuInput io.Reader, menuOutput io.Writer) error {
	if err := inventory.RequireDLDOwned(detail.Tags, originTag, "role", detail.Name); err != nil {
		return err
	}

	if len(detail.AttachedPolicies) == 0 {
		fmt.Fprintf(w, "Role %s has no attached managed policies to detach.\n", detail.Name)
		return nil
	}

	policy, err := pickComparable(w, "Select a policy to detach", fmt.Sprintf("Managed policies currently attached to role %s.", detail.Name), hintCancel, detail.AttachedPolicies, func(p IAMPolicyRef) string { return p.Name }, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	ok, err := Confirm(fmt.Sprintf("Detach policy %s from role %s?", policy.Name, detail.Name), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if _, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: aws.String(detail.Name), PolicyArn: aws.String(policy.ARN)}); err != nil {
		return err
	}

	fmt.Fprintf(w, "Detached policy %s from role %s.\n", policy.Name, detail.Name)
	return nil
}
