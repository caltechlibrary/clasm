package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// create-instance-profile and remove-role-from-instance-profile (DR-0188). The
// role template makes a role, the profile swap needs an instance profile, and a role
// cannot be deleted while it is in one: without these two the create and teardown
// cycle cannot be scripted.

// The two leaves' cliSlug values (DR-0177's mechanical rule).
const (
	CreateInstanceProfileCLISlug         = "create-instance-profile"
	RemoveRoleFromInstanceProfileCLISlug = "remove-role-from-instance-profile"
)

// Usage lines, repeated in every usage error.
const (
	CreateInstanceProfileUsage         = "usage: clasm iam " + CreateInstanceProfileCLISlug + " [-text|-json] <instance-profile-name> <role-name>"
	RemoveRoleFromInstanceProfileUsage = "usage: clasm iam " + RemoveRoleFromInstanceProfileCLISlug + " [--confirm <profile-name>] <role-name> <profile-name>"
)

const createInstanceProfileHelp = `

Creates an IAM instance profile and adds the named role to it, so the role can be
put on an instance (clasm compute associate-replace-iam-instance-profile). The role
must already exist. The profile is tagged DLD-owned with the configured Origin tag,
so clasm can delete it later (clasm iam delete-instance-profile). There is no
confirmation: it creates and destroys nothing. A profile name already in use is an
error.
`

// createTaggedInstanceProfile creates profileName tagged DLD-owned (when an Origin
// value is configured) and adds roleName to it. It is the one creation path, shared
// by the menu flow and the CLI form. The interactive pick-or-create flow that
// launches use creates an untagged profile; this one is tagged so that
// delete-instance-profile, which only deletes DLD-owned profiles, can remove it.
func createTaggedInstanceProfile(ctx context.Context, client awsclient.IAMAPI, originTag config.OriginTagConfig, profileName, roleName string) (arn string, err error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()

	var tags []iamtypes.Tag
	if originTag.DLDValue != "" {
		tags = []iamtypes.Tag{{Key: aws.String(originTag.Key), Value: aws.String(originTag.DLDValue)}}
	}
	out, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{InstanceProfileName: aws.String(profileName), Tags: tags})
	if err != nil {
		return "", fmt.Errorf("creating instance profile %q: %w", profileName, err)
	}
	if out.InstanceProfile != nil {
		arn = aws.ToString(out.InstanceProfile.Arn)
	}
	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName), RoleName: aws.String(roleName),
	}); err != nil {
		return arn, fmt.Errorf("instance profile %q was created but adding role %q to it failed: %w (the empty profile is still there; remove it with: clasm iam %s %s)",
			profileName, roleName, err, DeleteInstanceProfileCLISlug, profileName)
	}
	return arn, nil
}

// ParseCreateInstanceProfileArgs parses `iam create-instance-profile [-text|-json]
// <instance-profile-name> <role-name>`. Any problem is a *UsageError (exit 2) raised
// before AWS is touched; --help is a *HelpRequested.
func ParseCreateInstanceProfileArgs(args []string) (profile, role string, format ui.Format, err error) {
	opts, words, err := ParseComputeArgs(CreateInstanceProfileCLISlug, CreateInstanceProfileUsage, ComputeAllowNone, args)
	if err != nil {
		if h, ok := err.(*HelpRequested); ok {
			return "", "", ui.FormatText, &HelpRequested{Usage: h.Usage + createInstanceProfileHelp}
		}
		return "", "", ui.FormatText, err
	}
	if len(words) != 2 || strings.TrimSpace(words[0]) == "" || strings.TrimSpace(words[1]) == "" {
		return "", "", ui.FormatText, &UsageError{Msg: fmt.Sprintf("%s: want an instance profile name and a role name, got %d arguments\n%s",
			CreateInstanceProfileCLISlug, len(words), CreateInstanceProfileUsage)}
	}
	return strings.TrimSpace(words[0]), strings.TrimSpace(words[1]), opts.Format, nil
}

type createdInstanceProfileJSON struct {
	InstanceProfileName string `json:"instance_profile_name"`
	InstanceProfileARN  string `json:"instance_profile_arn"`
	RoleName            string `json:"role_name"`
}

// RunCreateInstanceProfileCLI is `clasm iam create-instance-profile`. An unknown role
// is a usage error before anything is created.
func RunCreateInstanceProfileCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, profileName, roleName string, format ui.Format) error {
	if _, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)}); err != nil {
		return iamLookupError("role", roleName, err)
	}
	arn, err := createTaggedInstanceProfile(ctx, client, originTag, profileName, roleName)
	if err != nil {
		return err
	}
	if format == ui.FormatText {
		fmt.Fprintf(w, "Created instance profile %s (%s) with role %s.\n", profileName, arn, roleName)
		return nil
	}
	return ui.WriteJSONValue(w, createdInstanceProfileJSON{profileName, arn, roleName}, format)
}

// ParseRemoveRoleFromInstanceProfileArgs parses `iam remove-role-from-instance-profile
// [--confirm <profile-name>] <role-name> <profile-name>`.
func ParseRemoveRoleFromInstanceProfileArgs(args []string) (role, profile string, opts DestructiveOptions, err error) {
	opts, words, err := ParseDestructiveNamedArgs(RemoveRoleFromInstanceProfileCLISlug, RemoveRoleFromInstanceProfileUsage, "profile-name", args)
	if err != nil {
		return "", "", DestructiveOptions{}, err
	}
	if len(words) != 2 || strings.TrimSpace(words[0]) == "" || strings.TrimSpace(words[1]) == "" {
		return "", "", DestructiveOptions{}, &UsageError{Msg: fmt.Sprintf("%s: want a role name and an instance profile name, got %d arguments\n%s",
			RemoveRoleFromInstanceProfileCLISlug, len(words), RemoveRoleFromInstanceProfileUsage)}
	}
	return strings.TrimSpace(words[0]), strings.TrimSpace(words[1]), opts, nil
}

// RunRemoveRoleFromInstanceProfileCLI is `clasm iam remove-role-from-instance-profile`,
// on DR-0180's gate: --confirm names the profile. Removing a role from a profile an
// instance uses cuts that instance's permissions at once, which is why it is gated
// though it can be undone by adding the role back. Everything before the gate is
// read-only, and a refusal the action itself has (not DLD-owned, not a member) is an
// error even in a dry run.
func RunRemoveRoleFromInstanceProfileCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, roleName, profileName string, confirms []string, interactive bool, input io.Reader, output io.Writer) error {
	gate := Gate{Name: profileName, Kind: "IAM instance profile", Confirms: confirms, Interactive: interactive,
		Summary: fmt.Sprintf("remove role %s from instance profile %s", roleName, profileName)}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}
	profile, err := fetchIAMInstanceProfileDetail(ctx, client, profileName)
	if err != nil {
		return iamLookupError("instance profile", profileName, err)
	}
	if err := inventory.RequireDLDOwned(profile.Tags, originTag, "instance profile", profileName); err != nil {
		return err
	}
	roleOut, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return iamLookupError("role", roleName, err)
	}
	if err := inventory.RequireDLDOwned(iamTagsToMap(roleOut.Role.Tags), originTag, "role", roleName); err != nil {
		return err
	}
	members := make([]string, len(profile.Roles))
	isMember := false
	for i, r := range profile.Roles {
		members[i] = r.Name
		if r.Name == roleName {
			isMember = true
		}
	}
	if !isMember {
		in := "no roles"
		if len(members) > 0 {
			in = "role(s) " + strings.Join(members, ", ")
		}
		return fmt.Errorf("role %q is not a member of instance profile %q, which holds %s", roleName, profileName, in)
	}

	gate.Plan = []string{
		fmt.Sprintf("remove role %s from instance profile %s", roleName, profileName),
		fmt.Sprintf("any instance that uses profile %s loses %s's permissions immediately, SSM management included if that role grants it; add the role back to undo", profileName, roleName),
	}
	decision, err := gate.Decide(w, input, output)
	if err != nil || decision != GateProceed {
		return err
	}
	if err := removeRoleFromInstanceProfile(ctx, client, profileName, roleName); err != nil {
		return err
	}
	fmt.Fprintf(w, "Removed role %s from instance profile %s.\n", roleName, profileName)
	return nil
}

// CreateInstanceProfile runs the IAM domain's "Create Instance Profile" action
// (DR-0188): pick a role, name the profile (the role's name by default), confirm,
// then create it tagged DLD-owned with the role in it.
func CreateInstanceProfile(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	roles, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		fmt.Fprintln(w, "No IAM roles found.")
		return nil
	}
	role, err := pickIAMRole(ctx, "Select the role to put in a new instance profile", "Any role may be chosen; the role itself is not changed.", roles)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return createIAMInstanceProfileConfirmed(ctx, w, client, originTag, role.Name, nil, nil)
}

// createIAMInstanceProfileConfirmed is CreateInstanceProfile's testable core once a
// role is picked (the picker is a real bubbletea program and cannot be pipe-tested).
func createIAMInstanceProfileConfirmed(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, roleName string, input io.Reader, output io.Writer) error {
	profileName, err := ui.Prompt("New instance profile name", ui.WithDefault(roleName), ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return cancelledIsNil(w, err)
	}
	ok, err := Confirm(fmt.Sprintf("Create instance profile %q with role %q?", profileName, roleName), WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}
	arn, err := createTaggedInstanceProfile(ctx, client, originTag, profileName, roleName)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Created instance profile %s (%s) with role %s. Newly created instance profiles can take a few seconds to propagate.\n", profileName, arn, roleName)
	return nil
}
