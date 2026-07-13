package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
)

// fakeIAMClient is the workflow package's fake for awsclient.IAMAPI,
// mirroring fakeEC2Client's style (launch_execute_test.go): embed the
// real interface so any unimplemented method panics loudly if a test
// exercises a path that needs it, and override just what's used.
type fakeIAMClient struct {
	instanceProfiles             []iamtypes.InstanceProfile
	listInstanceProfilesErr      error
	roles                        []iamtypes.Role
	listRolesErr                 error
	createInstanceProfileErr     error
	createInstanceProfileErrOnce bool // if true, only the first CreateInstanceProfile call errors
	createInstanceProfileCalls   int
	addRoleToInstanceProfile     error

	lastCreateInstanceProfileInput    *iam.CreateInstanceProfileInput
	lastAddRoleToInstanceProfileInput *iam.AddRoleToInstanceProfileInput
}

func (f *fakeIAMClient) ListInstanceProfiles(ctx context.Context, params *iam.ListInstanceProfilesInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error) {
	if f.listInstanceProfilesErr != nil {
		return nil, f.listInstanceProfilesErr
	}
	return &iam.ListInstanceProfilesOutput{InstanceProfiles: f.instanceProfiles}, nil
}

func (f *fakeIAMClient) ListRoles(ctx context.Context, params *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	if f.listRolesErr != nil {
		return nil, f.listRolesErr
	}
	return &iam.ListRolesOutput{Roles: f.roles}, nil
}

func (f *fakeIAMClient) CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	f.createInstanceProfileCalls++
	f.lastCreateInstanceProfileInput = params
	if f.createInstanceProfileErr != nil && (!f.createInstanceProfileErrOnce || f.createInstanceProfileCalls == 1) {
		return nil, f.createInstanceProfileErr
	}
	return &iam.CreateInstanceProfileOutput{
		InstanceProfile: &iamtypes.InstanceProfile{
			InstanceProfileName: params.InstanceProfileName,
			Arn:                 aws.String("arn:aws:iam::123456789012:instance-profile/" + aws.ToString(params.InstanceProfileName)),
		},
	}, nil
}

func (f *fakeIAMClient) AddRoleToInstanceProfile(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
	f.lastAddRoleToInstanceProfileInput = params
	if f.addRoleToInstanceProfile != nil {
		return nil, f.addRoleToInstanceProfile
	}
	return &iam.AddRoleToInstanceProfileOutput{}, nil
}

func newDuplicateInstanceProfileError() error {
	return &smithy.GenericAPIError{Code: "EntityAlreadyExists", Message: "already exists"}
}

// fakeIAMClientNoProfiles returns a fakeIAMClient configured to fail
// ListInstanceProfiles, forcing promptIAMInstanceProfileOrCreate's
// free-text fallback. Used by launch-params tests elsewhere in this
// package that aren't about IAM profile selection itself -- the
// instance-profile/role pickers converted to tui.RunPicker (Picker
// tier, DESIGN.md's full conversion punch list) are real bubbletea
// Programs that can't be pipe-tested, and a bare &fakeIAMClient{}
// succeeds with an empty (but non-error) profile list, which still
// reaches the picker.
func fakeIAMClientNoProfiles() *fakeIAMClient {
	return &fakeIAMClient{listInstanceProfilesErr: errors.New("no instance profiles configured for this test")}
}

// The instance-profile and role pickers converted to tui.RunPicker
// (DESIGN.md's full conversion punch list, Picker tier): real bubbletea
// Programs that can't be pipe-tested. promptIAMInstanceProfileOrCreate
// always builds a choices list of at least ["(none)", "Create new..."],
// so it reaches the picker on every path except the list-fetch-error
// free-text fallback -- the tests below that used to pick from that list
// ("PicksFromList", "NoneSkipsIt") are retired; createInstanceProfile
// ForRole (the create-new sub-flow once a role is resolved) and
// createInstanceProfileInteractive's own no-roles-found short-circuit
// (which returns before ever reaching the role picker) still exercise
// their own logic directly below. Covered only by manual/interactive
// verification otherwise, the same accepted limitation this session's
// other Picker-tier conversions already have.

func TestPromptIAMInstanceProfileOrCreate_FallsBackToFreeTextWhenListFails(t *testing.T) {
	fake := &fakeIAMClient{listInstanceProfilesErr: errors.New("access denied")}
	term, le, buf := newPipeEditor("manual-profile-name\n")

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "manual-profile-name" {
		t.Errorf("got %q, want %q", got, "manual-profile-name")
	}
}

func TestCreateInstanceProfileForRole_AcceptsDefaultName(t *testing.T) {
	fake := &fakeIAMClient{}
	role := RoleInfo{Name: "ec2-invenio-role"}
	term, le, buf := newPipeEditor("\n") // accept the default profile name (same as role name)

	got, created, err := createInstanceProfileForRole(context.Background(), term, fake, role, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created || got != "ec2-invenio-role" {
		t.Errorf("got name=%q created=%v, want %q/true (default profile name = role name)", got, created, "ec2-invenio-role")
	}
	if aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName) != "ec2-invenio-role" {
		t.Errorf("CreateInstanceProfile name = %q, want %q", aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName), "ec2-invenio-role")
	}
	if aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName) != "ec2-invenio-role" {
		t.Errorf("AddRoleToInstanceProfile role = %q, want %q", aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName), "ec2-invenio-role")
	}
}

func TestCreateInstanceProfileInteractive_NoRolesReturnsWithoutError(t *testing.T) {
	fake := &fakeIAMClient{} // no roles
	term, le, buf := newPipeEditor("")

	got, created, err := createInstanceProfileInteractive(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created || got != "" {
		t.Errorf("got name=%q created=%v, want empty/false when there are no roles to attach", got, created)
	}
	if !strings.Contains(buf.String(), "No IAM roles found") {
		t.Errorf("expected a no-roles message, got:\n%s", buf.String())
	}
}

func TestCreateInstanceProfileForRole_NameCollisionRetries(t *testing.T) {
	// huh's accessible-mode input never surfaces EOF as an error (it
	// falls back to the field's default instead, DESIGN.md's own
	// accepted limitation for this mode) -- so a fake that always
	// errors would retry forever rather than eventually surfacing an
	// error the way termlib's LineEditor.Prompt used to. Matches
	// TestCreateNewKeyPairInteractive_RetriesOnDuplicateName's own
	// errOnce shape: the first name collides, the retry succeeds.
	fake := &fakeIAMClient{createInstanceProfileErr: newDuplicateInstanceProfileError(), createInstanceProfileErrOnce: true}
	role := RoleInfo{Name: "ec2-invenio-role"}
	term, le, buf := newPipeEditor("taken-name\nfresh-name\n")

	got, created, err := createInstanceProfileForRole(context.Background(), term, fake, role, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created || got != "fresh-name" {
		t.Errorf("got name=%q created=%v, want %q/true", got, created, "fresh-name")
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected a name-collision message, got:\n%s", buf.String())
	}
}

func TestCreateInstanceProfileFromRole_CallsCreateThenAddRole(t *testing.T) {
	fake := &fakeIAMClient{}
	if err := createInstanceProfileFromRole(context.Background(), fake, "my-profile", "my-role"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName) != "my-profile" {
		t.Errorf("CreateInstanceProfile name = %q, want %q", aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName), "my-profile")
	}
	if aws.ToString(fake.lastAddRoleToInstanceProfileInput.InstanceProfileName) != "my-profile" || aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName) != "my-role" {
		t.Errorf("AddRoleToInstanceProfile got profile=%q role=%q, want profile=%q role=%q",
			aws.ToString(fake.lastAddRoleToInstanceProfileInput.InstanceProfileName), aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName),
			"my-profile", "my-role")
	}
}

func TestCreateInstanceProfileFromRole_PropagatesCreateError(t *testing.T) {
	fake := &fakeIAMClient{createInstanceProfileErr: errors.New("boom")}
	err := createInstanceProfileFromRole(context.Background(), fake, "my-profile", "my-role")
	if err == nil {
		t.Fatal("expected an error")
	}
	if fake.lastAddRoleToInstanceProfileInput != nil {
		t.Error("AddRoleToInstanceProfile should not be called after CreateInstanceProfile fails")
	}
}

func TestCreateInstanceProfileFromRole_PropagatesAddRoleError(t *testing.T) {
	fake := &fakeIAMClient{addRoleToInstanceProfile: errors.New("boom")}
	err := createInstanceProfileFromRole(context.Background(), fake, "my-profile", "my-role")
	if err == nil {
		t.Fatal("expected an error")
	}
}
