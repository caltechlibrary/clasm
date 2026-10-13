package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Delete Role and Delete Instance Profile (DR-0186) on
// DR-0180's gate. The target is identified by its name, so --confirm takes the
// role or profile name. Refusals the action itself has -- not DLD-owned, still
// referenced -- are errors even in a dry run: confirming would not help, so the
// plan must not look like it would succeed.

// The two leaves' cliSlug values (DR-0177's mechanical rule).
const (
	DeleteRoleCLISlug            = "delete-role"
	DeleteInstanceProfileCLISlug = "delete-instance-profile"
)

// Usage lines, repeated in every usage error.
const (
	DeleteRoleUsage            = "usage: clasm iam " + DeleteRoleCLISlug + " [--confirm <role-name>] <role-name>"
	DeleteInstanceProfileUsage = "usage: clasm iam " + DeleteInstanceProfileCLISlug + " [--confirm <profile-name>] <profile-name>"
)

func parseOneName(leaf, usage, what string, args []string) (string, DestructiveOptions, error) {
	opts, words, err := ParseDestructiveNamedArgs(leaf, usage, what, args)
	if err != nil {
		return "", DestructiveOptions{}, err
	}
	if len(words) != 1 || strings.TrimSpace(words[0]) == "" {
		return "", DestructiveOptions{}, &UsageError{Msg: fmt.Sprintf("%s: want one %s, got %d arguments\n%s", leaf, what, len(words), usage)}
	}
	return words[0], opts, nil
}

// ParseDeleteIAMRoleArgs parses `iam delete-role [--confirm <role-name>] <role-name>`.
func ParseDeleteIAMRoleArgs(args []string) (string, DestructiveOptions, error) {
	return parseOneName(DeleteRoleCLISlug, DeleteRoleUsage, "role-name", args)
}

// ParseDeleteIAMInstanceProfileArgs parses
// `iam delete-instance-profile [--confirm <profile-name>] <profile-name>`.
func ParseDeleteIAMInstanceProfileArgs(args []string) (string, DestructiveOptions, error) {
	return parseOneName(DeleteInstanceProfileCLISlug, DeleteInstanceProfileUsage, "profile-name", args)
}

// RunDeleteIAMRoleCLI is `clasm iam delete-role`. A --confirm that does not
// match is a usage error before any AWS call; everything before the gate is
// read-only. interactive says a terminal is attached (input and output drive its
// prompt in tests and are nil in production).
func RunDeleteIAMRoleCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, name, confirm string, interactive bool, input io.Reader, output io.Writer) error {
	gate := Gate{Name: name, Kind: "IAM role", Confirm: confirm, Interactive: interactive, Summary: "delete role " + name}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}
	detail, err := fetchIAMRoleDetail(ctx, client, name)
	if err != nil {
		return iamLookupError("role", name, err)
	}
	if err := inventory.RequireDLDOwned(detail.Tags, originTag, "role", name); err != nil {
		return err
	}
	if len(detail.ReferencedByProfiles) > 0 {
		return fmt.Errorf("role %q is a member of instance profile(s) %s -- remove it from each first (IAM domain, \"Remove role from instance profile\")", name, strings.Join(detail.ReferencedByProfiles, ", "))
	}

	for _, p := range detail.InlinePolicyNames {
		gate.Plan = append(gate.Plan, "delete inline policy "+p)
	}
	dedicated := false
	for _, p := range detail.AttachedPolicies {
		gate.Plan = append(gate.Plan, "detach policy "+p.Name)
		if p.Name == name+"-policy" && !isAWSManagedPolicyArn(p.ARN) {
			dedicated = true
		}
	}
	gate.Plan = append(gate.Plan, "delete role "+name)
	if dedicated {
		gate.Plan = append(gate.Plan, fmt.Sprintf("delete policy %s-policy if nothing else uses it (checked when deleting)", name))
	}

	decision, err := gate.Decide(w, input, output)
	if err != nil || decision != GateProceed {
		return err
	}
	if err := deleteIAMRole(ctx, client, detail); err != nil {
		return err
	}
	fmt.Fprintf(w, "Deleted role %s.\n", name)
	return nil
}

// RunDeleteIAMInstanceProfileCLI is `clasm iam delete-instance-profile`, with
// the same gate and the same order as RunDeleteIAMRoleCLI. A profile that still
// holds a role is refused here, in the dry run too, instead of leaving AWS to
// fail the delete.
func RunDeleteIAMInstanceProfileCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, name, confirm string, interactive bool, input io.Reader, output io.Writer) error {
	gate := Gate{Name: name, Kind: "IAM instance profile", Confirm: confirm, Interactive: interactive, Summary: "delete instance profile " + name}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}
	detail, err := fetchIAMInstanceProfileDetail(ctx, client, name)
	if err != nil {
		return iamLookupError("instance profile", name, err)
	}
	if err := inventory.RequireDLDOwned(detail.Tags, originTag, "instance profile", name); err != nil {
		return err
	}
	if len(detail.Roles) > 0 {
		names := make([]string, len(detail.Roles))
		for i, r := range detail.Roles {
			names[i] = r.Name
		}
		return fmt.Errorf("instance profile %q still contains role(s) %s -- remove each first (IAM domain, \"Remove role from instance profile\")", name, strings.Join(names, ", "))
	}

	gate.Plan = []string{"delete instance profile " + name}
	decision, err := gate.Decide(w, input, output)
	if err != nil || decision != GateProceed {
		return err
	}
	if err := deleteInstanceProfile(ctx, client, name); err != nil {
		return err
	}
	fmt.Fprintf(w, "Deleted instance profile %s.\n", name)
	return nil
}
