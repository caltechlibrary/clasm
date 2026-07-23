package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// instanceProfileChoice is one entry in promptIAMInstanceProfileOrCreate's
// pick list: either an existing instance profile or "create new". No
// "(none)" entry -- an instance profile is mandatory (DESIGN.md,
// "SSM-Capable Instance Profile Enforcement + Retrofit"). Every existing
// profile shown is already known SSM-capable -- buildInstanceProfileChoices
// filters non-capable ones out rather than showing and rejecting them
// (DECISIONS.md, "Filter non-SSM-capable profiles/roles from the picker,
// don't just annotate them").
type instanceProfileChoice struct {
	label     string
	name      string
	createNew bool
}

// roleChoice is one entry in createInstanceProfileInteractive's role
// pick list -- same filtering as instanceProfileChoice above.
type roleChoice struct {
	label string
	role  RoleInfo
}

// buildInstanceProfileChoices resolves SSM-capability for each of
// profiles and returns promptIAMInstanceProfileOrCreate's pick list,
// filtering out any that aren't SSM-capable -- independent of the
// Picker-tier UI so it's directly unit-testable (DESIGN.md, "SSM-Capable
// Instance Profile Enforcement + Retrofit"; DECISIONS.md, "Filter
// non-SSM-capable profiles/roles from the picker, don't just annotate
// them").
func buildInstanceProfileChoices(ctx context.Context, client awsclient.IAMAPI, profiles []InstanceProfileInfo) ([]instanceProfileChoice, error) {
	choices := make([]instanceProfileChoice, 0, len(profiles)+1)
	for _, p := range profiles {
		capable, err := instanceProfileIsSSMCapable(ctx, client, p)
		if err != nil {
			return nil, err
		}
		if !capable {
			continue
		}
		choices = append(choices, instanceProfileChoice{label: instanceProfileLabel(p), name: p.Name})
	}
	choices = append(choices, instanceProfileChoice{label: "Create new instance profile (attach an existing role)", createNew: true})
	return choices, nil
}

// buildRoleChoices resolves SSM-capability for each of roles and
// returns createInstanceProfileInteractive's role pick list, filtering
// out any that aren't SSM-capable -- same shape as
// buildInstanceProfileChoices above.
func buildRoleChoices(ctx context.Context, client awsclient.IAMAPI, roles []RoleInfo) ([]roleChoice, error) {
	choices := make([]roleChoice, 0, len(roles))
	for _, r := range roles {
		capable, err := roleHasSSMPermissions(ctx, client, r.Name)
		if err != nil {
			return nil, err
		}
		if !capable {
			continue
		}
		choices = append(choices, roleChoice{label: roleLabel(r), role: r})
	}
	return choices, nil
}

// pickInstanceProfileChoice runs a Picker-tier tui.RunPicker (DESIGN.md's
// full conversion punch list) over choices and returns the chosen one.
// Like pickInstance/pickImage/pickSubnet, this drives a real bubbletea
// Program that can't be pipe-tested.
func pickInstanceProfileChoice(ctx context.Context, title string, choices []instanceProfileChoice) (instanceProfileChoice, error) {
	rows := make([]string, len(choices))
	for i, c := range choices {
		rows[i] = c.label
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Description:  "The IAM instance profile controls what AWS APIs this instance can call (e.g. reading its own tags via SSM).",
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return instanceProfileChoice{}, err
	}
	return choices[idx], nil
}

// pickRole runs a Picker-tier tui.RunPicker over choices and returns the
// chosen one -- same limitation as pickInstanceProfileChoice above.
func pickRole(ctx context.Context, title string, choices []roleChoice) (roleChoice, error) {
	rows := make([]string, len(choices))
	for i, c := range choices {
		rows[i] = c.label
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Description:  "The IAM role this new instance profile will attach -- it must already have a trust policy allowing EC2 to assume it.",
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return roleChoice{}, err
	}
	return choices[idx], nil
}

func instanceProfileLabel(p InstanceProfileInfo) string {
	if len(p.Roles) == 0 {
		return p.Name
	}
	return fmt.Sprintf("%s (role: %s)", p.Name, strings.Join(p.Roles, ", "))
}

func roleLabel(r RoleInfo) string {
	if r.Description == "" {
		return r.Name
	}
	return fmt.Sprintf("%s - %s", r.Name, r.Description)
}

// promptIAMInstanceProfileOrCreate lists existing IAM instance profiles
// (DESIGN.md, Feature 2: "IAM instance profile") and offers creating a
// new one attached to an existing IAM role, for operators who don't
// have a profile yet (see DECISIONS.md, "Support picking or creating an
// IAM instance profile from within awsops"). This replaces a free-text
// prompt that pointed operators at "IAM console > Roles" for a field
// that actually needs the *instance profile* name -- the two are only
// the same by convention, not by requirement, and real-AWS testing hit
// exactly that mismatch as AWS's own "Invalid IAM Instance Profile
// name" error. Falls back to the original free-text prompt only if the
// list itself can't be fetched (e.g. missing iam:ListInstanceProfiles
// permission) -- an empty-but-successful list still offers "create
// new". An instance profile is now mandatory, no "(none)" choice
// (DESIGN.md, "SSM-Capable Instance Profile Enforcement + Retrofit"):
// every profile shown, existing or newly created, must have an
// SSM-capable role attached. buildInstanceProfileChoices filters out
// non-capable profiles rather than showing and rejecting them
// (DECISIONS.md, "Filter non-SSM-capable profiles/roles from the
// picker, don't just annotate them") -- live testing found that
// annotating a long list of mostly-irrelevant entries was harder to use
// than filtering. The free-text fallback path (list fetch itself
// failed) can't verify SSM-capability at all and is left unchanged --
// enforcing it there isn't possible without the ability to query IAM in
// the first place.
func promptIAMInstanceProfileOrCreate(ctx context.Context, w io.Writer, client awsclient.IAMAPI, input io.Reader, output io.Writer) (string, error) {
	profiles, err := listInstanceProfiles(ctx, client)
	if err != nil {
		return ui.Prompt("IAM instance profile (the instance profile name, not the IAM role name -- see IAM console > Roles > <role> > Instance profile ARNs)", ui.WithIO(input, output))
	}

	for {
		choices, err := buildInstanceProfileChoices(ctx, client, profiles)
		if err != nil {
			return "", err
		}

		picked, err := pickInstanceProfileChoice(ctx, "Select an IAM instance profile", choices)
		if err != nil {
			return "", err
		}
		if !picked.createNew {
			return picked.name, nil
		}

		name, created, err := createInstanceProfileInteractive(ctx, w, client, input, output)
		if err != nil {
			return "", err
		}
		if created {
			return name, nil
		}
		// No SSM-capable roles were available to attach -- redisplay
		// the picker instead of failing the whole launch over it.
	}
}

// createInstanceProfileInteractive picks an existing SSM-capable IAM
// role and creates a new instance profile attached to it. Returns
// created=false (not an error) if there are no roles at all, or none
// that are SSM-capable (DESIGN.md, "SSM-Capable Instance Profile
// Enforcement + Retrofit") -- either way the caller redisplays the
// instance-profile picker rather than failing the whole launch.
func createInstanceProfileInteractive(ctx context.Context, w io.Writer, client awsclient.IAMAPI, input io.Reader, output io.Writer) (name string, created bool, err error) {
	roles, err := listRoles(ctx, client)
	if err != nil {
		return "", false, err
	}
	if len(roles) == 0 {
		fmt.Fprintln(w, "No IAM roles found in this account -- cannot create an instance profile without one to attach.")
		return "", false, nil
	}

	choices, err := buildRoleChoices(ctx, client, roles)
	if err != nil {
		return "", false, err
	}
	if len(choices) == 0 {
		fmt.Fprintf(w, "No SSM-capable IAM roles found in this account -- attach %s to a role in the IAM console, then try again.\n", ssmManagedInstanceCorePolicyArn)
		return "", false, nil
	}

	picked, err := pickRole(ctx, "Select a role to attach", choices)
	if err != nil {
		return "", false, err
	}
	return createInstanceProfileForRole(ctx, w, client, picked.role, input, output)
}

// createInstanceProfileForRole is createInstanceProfileInteractive's
// testable core, once a role is resolved -- role selection runs a real
// bubbletea Program (tui.RunPicker, DESIGN.md's full conversion punch
// list) that can't be driven by a test's pipe input, same limitation as
// every other Picker-tier conversion this session.
func createInstanceProfileForRole(ctx context.Context, w io.Writer, client awsclient.IAMAPI, role RoleInfo, input io.Reader, output io.Writer) (name string, created bool, err error) {
	for {
		profileName, err := ui.Prompt("New instance profile name", ui.WithDefault(role.Name), ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
		if err != nil {
			return "", false, err
		}

		if err := createInstanceProfileFromRole(ctx, client, profileName, role.Name); err != nil {
			if isDuplicateInstanceProfileError(err) {
				fmt.Fprintf(w, "invalid input: an instance profile named %q already exists -- choose a different name\n", profileName)
				continue
			}
			return "", false, err
		}

		fmt.Fprintf(w, "Created instance profile %q attached to role %q. Note: newly created instance profiles can take a few seconds to propagate -- if launching the instance fails with an instance-profile-not-found error, wait a moment and retry.\n", profileName, role.Name)
		return profileName, true, nil
	}
}

// createInstanceProfileFromRole calls iam:CreateInstanceProfile, then
// iam:AddRoleToInstanceProfile to attach roleName to the newly created
// profileName (see DECISIONS.md, "Support picking or creating an IAM
// instance profile from within awsops" -- scoped to attaching an
// existing role, not also creating one, since a new role would need its
// own trust-policy/permissions decisions).
func createInstanceProfileFromRole(ctx context.Context, client awsclient.IAMAPI, profileName, roleName string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	}); err != nil {
		return err
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	}); err != nil {
		return err
	}
	return nil
}

func isDuplicateInstanceProfileError(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == "EntityAlreadyExists"
}
