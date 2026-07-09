package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/smithy-go"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// instanceProfileChoice is one entry in promptIAMInstanceProfileOrCreate's
// pick list: either an existing instance profile, the "(none)" entry
// that leaves the field blank, or "create new".
type instanceProfileChoice struct {
	label     string
	name      string
	createNew bool
}

func instanceProfileChoiceLabel(c instanceProfileChoice) string { return c.label }

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
// (DESIGN.md, Feature 2: "IAM instance profile (optional)") and offers
// creating a new one attached to an existing IAM role, for operators who
// don't have a profile yet (see DECISIONS.md, "Support picking or
// creating an IAM instance profile from within awsops"). This replaces a
// free-text prompt that pointed operators at "IAM console > Roles" for a
// field that actually needs the *instance profile* name -- the two are
// only the same by convention, not by requirement, and real-AWS testing
// hit exactly that mismatch as AWS's own "Invalid IAM Instance Profile
// name" error. Falls back to the original free-text prompt only if the
// list itself can't be fetched (e.g. missing iam:ListInstanceProfiles
// permission) -- an empty-but-successful list still offers "create new",
// unlike promptSecurityGroupIDs/promptSubnetID, since this field's whole
// point is to also cover the "I don't have one yet" case.
func promptIAMInstanceProfileOrCreate(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.IAMAPI) (string, error) {
	profiles, err := listInstanceProfiles(ctx, client)
	if err != nil {
		return ui.Prompt(t, le, "IAM instance profile (optional; the instance profile name, not the IAM role name -- see IAM console > Roles > <role> > Instance profile ARNs)")
	}

	for {
		choices := make([]instanceProfileChoice, 0, len(profiles)+2)
		choices = append(choices, instanceProfileChoice{label: "(none)"})
		for _, p := range profiles {
			choices = append(choices, instanceProfileChoice{label: instanceProfileLabel(p), name: p.Name})
		}
		choices = append(choices, instanceProfileChoice{label: "Create new instance profile (attach an existing role)", createNew: true})

		picked, err := ui.PickList(t, le, choices, instanceProfileChoiceLabel, "Select an IAM instance profile")
		if err != nil {
			return "", err
		}
		if !picked.createNew {
			return picked.name, nil
		}

		name, created, err := createInstanceProfileInteractive(ctx, t, le, client)
		if err != nil {
			return "", err
		}
		if created {
			return name, nil
		}
		// No roles were available to attach -- redisplay the picker
		// instead of failing the whole launch over it.
	}
}

// createInstanceProfileInteractive picks an existing IAM role and creates
// a new instance profile attached to it. Returns created=false (not an
// error) if there are no roles to attach, so the caller can redisplay
// the instance-profile picker.
func createInstanceProfileInteractive(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.IAMAPI) (name string, created bool, err error) {
	roles, err := listRoles(ctx, client)
	if err != nil {
		return "", false, err
	}
	if len(roles) == 0 {
		t.Println("No IAM roles found in this account -- cannot create an instance profile without one to attach.")
		t.Refresh()
		return "", false, nil
	}

	role, err := ui.PickList(t, le, roles, roleLabel, "Select a role to attach")
	if err != nil {
		return "", false, err
	}

	for {
		profileName, err := ui.Prompt(t, le, "New instance profile name", ui.WithDefault(role.Name), ui.WithValidator(requireNonEmpty))
		if err != nil {
			return "", false, err
		}

		if err := createInstanceProfileFromRole(ctx, client, profileName, role.Name); err != nil {
			if isDuplicateInstanceProfileError(err) {
				t.Printf("invalid input: an instance profile named %q already exists -- choose a different name\n", profileName)
				t.Refresh()
				continue
			}
			return "", false, err
		}

		t.Printf("Created instance profile %q attached to role %q. Note: newly created instance profiles can take a few seconds to propagate -- if launching the instance fails with an instance-profile-not-found error, wait a moment and retry.\n", profileName, role.Name)
		t.Refresh()
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
