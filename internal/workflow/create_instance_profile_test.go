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
	instanceProfiles         []iamtypes.InstanceProfile
	listInstanceProfilesErr  error
	roles                    []iamtypes.Role
	listRolesErr             error
	createInstanceProfileErr error
	addRoleToInstanceProfile error

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
	f.lastCreateInstanceProfileInput = params
	if f.createInstanceProfileErr != nil {
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

func TestPromptIAMInstanceProfileOrCreate_PicksFromList(t *testing.T) {
	fake := &fakeIAMClient{instanceProfiles: []iamtypes.InstanceProfile{
		{InstanceProfileName: aws.String("ec2-invenio-profile"), Roles: []iamtypes.Role{{RoleName: aws.String("ec2-invenio-role")}}},
	}}
	term, le, buf := newPipeEditor(t, "2\n") // 1) (none), 2) ec2-invenio-profile, 3) Create new

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ec2-invenio-profile" {
		t.Errorf("got %q, want %q", got, "ec2-invenio-profile")
	}
	if !strings.Contains(buf.String(), "ec2-invenio-role") {
		t.Errorf("expected the attached role name in the listing, got:\n%s", buf.String())
	}
}

func TestPromptIAMInstanceProfileOrCreate_NoneSkipsIt(t *testing.T) {
	fake := &fakeIAMClient{instanceProfiles: []iamtypes.InstanceProfile{
		{InstanceProfileName: aws.String("some-profile")},
	}}
	term, le, _ := newPipeEditor(t, "1\n") // (none)

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty (none)", got)
	}
}

func TestPromptIAMInstanceProfileOrCreate_FallsBackToFreeTextWhenListFails(t *testing.T) {
	fake := &fakeIAMClient{listInstanceProfilesErr: errors.New("access denied")}
	term, le, _ := newPipeEditor(t, "manual-profile-name\n")

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "manual-profile-name" {
		t.Errorf("got %q, want %q", got, "manual-profile-name")
	}
}

func TestPromptIAMInstanceProfileOrCreate_EmptyListStillOffersCreateNew(t *testing.T) {
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("ec2-invenio-role")}},
	}
	// choices: 1) (none), 2) Create new -- pick "create new", then role 1,
	// then accept the default profile name (same as role name).
	term, le, _ := newPipeEditor(t, "2\n1\n\n")

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ec2-invenio-role" {
		t.Errorf("got %q, want %q (default profile name = role name)", got, "ec2-invenio-role")
	}
	if aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName) != "ec2-invenio-role" {
		t.Errorf("CreateInstanceProfile name = %q, want %q", aws.ToString(fake.lastCreateInstanceProfileInput.InstanceProfileName), "ec2-invenio-role")
	}
	if aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName) != "ec2-invenio-role" {
		t.Errorf("AddRoleToInstanceProfile role = %q, want %q", aws.ToString(fake.lastAddRoleToInstanceProfileInput.RoleName), "ec2-invenio-role")
	}
}

func TestPromptIAMInstanceProfileOrCreate_CreateNewWithNoRolesRedisplaysPicker(t *testing.T) {
	fake := &fakeIAMClient{} // no profiles, no roles
	// 1) (none), 2) Create new -- pick "create new" (no roles -> message,
	// redisplay), then pick (none).
	term, le, buf := newPipeEditor(t, "2\n1\n")

	got, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty (none, after redisplay)", got)
	}
	if !strings.Contains(buf.String(), "No IAM roles found") {
		t.Errorf("expected a no-roles message, got:\n%s", buf.String())
	}
}

func TestPromptIAMInstanceProfileOrCreate_NameCollisionRetries(t *testing.T) {
	fake := &fakeIAMClient{
		roles:                    []iamtypes.Role{{RoleName: aws.String("ec2-invenio-role")}},
		createInstanceProfileErr: newDuplicateInstanceProfileError(),
	}
	term, le, buf := newPipeEditor(t, "2\n1\ntaken-name\n")

	_, err := promptIAMInstanceProfileOrCreate(context.Background(), term, le, fake)
	if err == nil {
		t.Fatal("expected the duplicate-name error to eventually surface (fake always errors)")
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
