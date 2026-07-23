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

	// attachedPolicyArns maps a role name to the policy ARNs
	// ListAttachedRolePolicies should report for it -- supports
	// roleHasSSMPermissions (ssm_iam_check_test.go).
	attachedPolicyArns          map[string][]string
	listAttachedRolePoliciesErr error

	// policies/listPoliciesErr support the IAM domain's Policies
	// discovery view (iam_domain_test.go, DESIGN.md "IAM Profile & Role
	// Management Domain").
	policies              []iamtypes.Policy
	listPoliciesErr       error
	lastListPoliciesInput *iam.ListPoliciesInput

	// roleTags/instanceProfileTags/policyTags key by name (ARN for
	// policies) and back ListRoleTags/ListInstanceProfileTags/
	// ListPolicyTags -- support the Tag Management IAM fetch/apply
	// closures (iam_tags_test.go, PLAN.md Phase 20.37).
	roleTags            map[string][]iamtypes.Tag
	listRoleTagsErr     error
	instanceProfileTags map[string][]iamtypes.Tag
	listInstProfTagsErr error
	policyTags          map[string][]iamtypes.Tag
	listPolicyTagsErr   error

	tagRoleErr                    error
	lastTagRoleInput              *iam.TagRoleInput
	untagRoleErr                  error
	lastUntagRoleInput            *iam.UntagRoleInput
	tagInstanceProfileErr         error
	lastTagInstanceProfileInput   *iam.TagInstanceProfileInput
	untagInstanceProfileErr       error
	lastUntagInstanceProfileInput *iam.UntagInstanceProfileInput
	tagPolicyErr                  error
	lastTagPolicyInput            *iam.TagPolicyInput
	untagPolicyErr                error
	lastUntagPolicyInput          *iam.UntagPolicyInput

	lastCreateInstanceProfileInput    *iam.CreateInstanceProfileInput
	lastAddRoleToInstanceProfileInput *iam.AddRoleToInstanceProfileInput

	// getRoleOut/getRoleErr, rolePolicyNames/listRolePoliciesErr,
	// rolePolicyDocuments/getRolePolicyErr, policiesByArn/getPolicyErr,
	// policyVersions/getPolicyVersionErr support the Role detail view
	// (iam_detail_test.go, PLAN.md Phase 20.38).
	getRoleOut            *iam.GetRoleOutput
	getRoleErr            error
	getInstanceProfileOut *iam.GetInstanceProfileOutput
	getInstanceProfileErr error
	rolePolicyNames       map[string][]string
	listRolePoliciesErr   error
	rolePolicyDocuments   map[string]string // key: roleName+"/"+policyName
	getRolePolicyErr      error
	policiesByArn         map[string]iamtypes.Policy
	getPolicyErr          error
	policyVersions        map[string]string // key: policyArn+"/"+versionId
	getPolicyVersionErr   error
}

func (f *fakeIAMClient) GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	if f.getRoleErr != nil {
		return nil, f.getRoleErr
	}
	if f.getRoleOut != nil {
		return f.getRoleOut, nil
	}
	return &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: params.RoleName}}, nil
}

func (f *fakeIAMClient) GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error) {
	if f.getInstanceProfileErr != nil {
		return nil, f.getInstanceProfileErr
	}
	if f.getInstanceProfileOut != nil {
		return f.getInstanceProfileOut, nil
	}
	return &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{InstanceProfileName: params.InstanceProfileName}}, nil
}

func (f *fakeIAMClient) ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if f.listRolePoliciesErr != nil {
		return nil, f.listRolePoliciesErr
	}
	return &iam.ListRolePoliciesOutput{PolicyNames: f.rolePolicyNames[aws.ToString(params.RoleName)]}, nil
}

func (f *fakeIAMClient) GetRolePolicy(ctx context.Context, params *iam.GetRolePolicyInput, optFns ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	if f.getRolePolicyErr != nil {
		return nil, f.getRolePolicyErr
	}
	key := aws.ToString(params.RoleName) + "/" + aws.ToString(params.PolicyName)
	doc := f.rolePolicyDocuments[key]
	return &iam.GetRolePolicyOutput{RoleName: params.RoleName, PolicyName: params.PolicyName, PolicyDocument: aws.String(doc)}, nil
}

func (f *fakeIAMClient) GetPolicy(ctx context.Context, params *iam.GetPolicyInput, optFns ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	if f.getPolicyErr != nil {
		return nil, f.getPolicyErr
	}
	p := f.policiesByArn[aws.ToString(params.PolicyArn)]
	return &iam.GetPolicyOutput{Policy: &p}, nil
}

func (f *fakeIAMClient) GetPolicyVersion(ctx context.Context, params *iam.GetPolicyVersionInput, optFns ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	if f.getPolicyVersionErr != nil {
		return nil, f.getPolicyVersionErr
	}
	key := aws.ToString(params.PolicyArn) + "/" + aws.ToString(params.VersionId)
	doc := f.policyVersions[key]
	return &iam.GetPolicyVersionOutput{PolicyVersion: &iamtypes.PolicyVersion{VersionId: params.VersionId, Document: aws.String(doc)}}, nil
}

func (f *fakeIAMClient) ListRoleTags(ctx context.Context, params *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	if f.listRoleTagsErr != nil {
		return nil, f.listRoleTagsErr
	}
	return &iam.ListRoleTagsOutput{Tags: f.roleTags[aws.ToString(params.RoleName)]}, nil
}

func (f *fakeIAMClient) ListInstanceProfileTags(ctx context.Context, params *iam.ListInstanceProfileTagsInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error) {
	if f.listInstProfTagsErr != nil {
		return nil, f.listInstProfTagsErr
	}
	return &iam.ListInstanceProfileTagsOutput{Tags: f.instanceProfileTags[aws.ToString(params.InstanceProfileName)]}, nil
}

func (f *fakeIAMClient) ListPolicyTags(ctx context.Context, params *iam.ListPolicyTagsInput, optFns ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error) {
	if f.listPolicyTagsErr != nil {
		return nil, f.listPolicyTagsErr
	}
	return &iam.ListPolicyTagsOutput{Tags: f.policyTags[aws.ToString(params.PolicyArn)]}, nil
}

func (f *fakeIAMClient) TagRole(ctx context.Context, params *iam.TagRoleInput, optFns ...func(*iam.Options)) (*iam.TagRoleOutput, error) {
	f.lastTagRoleInput = params
	if f.tagRoleErr != nil {
		return nil, f.tagRoleErr
	}
	return &iam.TagRoleOutput{}, nil
}

func (f *fakeIAMClient) UntagRole(ctx context.Context, params *iam.UntagRoleInput, optFns ...func(*iam.Options)) (*iam.UntagRoleOutput, error) {
	f.lastUntagRoleInput = params
	if f.untagRoleErr != nil {
		return nil, f.untagRoleErr
	}
	return &iam.UntagRoleOutput{}, nil
}

func (f *fakeIAMClient) TagInstanceProfile(ctx context.Context, params *iam.TagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error) {
	f.lastTagInstanceProfileInput = params
	if f.tagInstanceProfileErr != nil {
		return nil, f.tagInstanceProfileErr
	}
	return &iam.TagInstanceProfileOutput{}, nil
}

func (f *fakeIAMClient) UntagInstanceProfile(ctx context.Context, params *iam.UntagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.UntagInstanceProfileOutput, error) {
	f.lastUntagInstanceProfileInput = params
	if f.untagInstanceProfileErr != nil {
		return nil, f.untagInstanceProfileErr
	}
	return &iam.UntagInstanceProfileOutput{}, nil
}

func (f *fakeIAMClient) TagPolicy(ctx context.Context, params *iam.TagPolicyInput, optFns ...func(*iam.Options)) (*iam.TagPolicyOutput, error) {
	f.lastTagPolicyInput = params
	if f.tagPolicyErr != nil {
		return nil, f.tagPolicyErr
	}
	return &iam.TagPolicyOutput{}, nil
}

func (f *fakeIAMClient) UntagPolicy(ctx context.Context, params *iam.UntagPolicyInput, optFns ...func(*iam.Options)) (*iam.UntagPolicyOutput, error) {
	f.lastUntagPolicyInput = params
	if f.untagPolicyErr != nil {
		return nil, f.untagPolicyErr
	}
	return &iam.UntagPolicyOutput{}, nil
}

func (f *fakeIAMClient) ListPolicies(ctx context.Context, params *iam.ListPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListPoliciesOutput, error) {
	f.lastListPoliciesInput = params
	if f.listPoliciesErr != nil {
		return nil, f.listPoliciesErr
	}
	return &iam.ListPoliciesOutput{Policies: f.policies}, nil
}

func (f *fakeIAMClient) ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	if f.listAttachedRolePoliciesErr != nil {
		return nil, f.listAttachedRolePoliciesErr
	}
	arns := f.attachedPolicyArns[aws.ToString(params.RoleName)]
	out := &iam.ListAttachedRolePoliciesOutput{}
	for _, arn := range arns {
		out.AttachedPolicies = append(out.AttachedPolicies, iamtypes.AttachedPolicy{
			PolicyArn:  aws.String(arn),
			PolicyName: aws.String(policyNameFromArn(arn)),
		})
	}
	return out, nil
}

// policyNameFromArn derives a policy's name from its ARN's last path
// segment, matching real AWS ARN structure (e.g.
// "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole" ->
// "AWSLambdaBasicExecutionRole") -- attachedPolicyArns (used by
// roleHasSSMPermissions tests) only ever needed the ARN itself before
// this, so the fake never derived a name; iam_detail_test.go's Role
// detail tests need both.
func policyNameFromArn(arn string) string {
	if i := strings.LastIndex(arn, "/"); i != -1 {
		return arn[i+1:]
	}
	return arn
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
// always builds a choices list of at least ["Create new..."] (no
// "(none)" entry as of "SSM-Capable Instance Profile Enforcement +
// Retrofit" -- an instance profile is now mandatory), so it reaches the
// picker on every path except the list-fetch-error free-text fallback
// -- the tests below that used to pick from that list ("PicksFromList",
// "NoneSkipsIt") are retired; createInstanceProfileForRole (the
// create-new sub-flow once a role is resolved) and
// createInstanceProfileInteractive's own no-roles-found short-circuit
// (which returns before ever reaching the role picker) still exercise
// their own logic directly below, alongside
// buildInstanceProfileChoices/buildRoleChoices (the SSM-capability
// annotation logic, also directly testable since it runs before either
// picker). Actual picker selection is covered only by manual/interactive
// verification, the same accepted limitation this session's other
// Picker-tier conversions already have.

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

// TestCreateInstanceProfileInteractive_NoSSMCapableRolesReturnsWithoutError
// covers the case buildRoleChoices' filtering introduces: roles exist,
// but none are SSM-capable (DECISIONS.md, "Filter non-SSM-capable
// profiles/roles from the picker, don't just annotate them") --
// distinct from the no-roles-at-all case above.
func TestCreateInstanceProfileInteractive_NoSSMCapableRolesReturnsWithoutError(t *testing.T) {
	fake := &fakeIAMClient{roles: []iamtypes.Role{{RoleName: aws.String("not-capable-role")}}}
	term, le, buf := newPipeEditor("")

	got, created, err := createInstanceProfileInteractive(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created || got != "" {
		t.Errorf("got name=%q created=%v, want empty/false when no roles are SSM-capable", got, created)
	}
	if !strings.Contains(buf.String(), "No SSM-capable IAM roles found") {
		t.Errorf("expected a no-SSM-capable-roles message, got:\n%s", buf.String())
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

// buildInstanceProfileChoices/buildRoleChoices (DESIGN.md, "SSM-Capable
// Instance Profile Enforcement + Retrofit"; DECISIONS.md, "Filter
// non-SSM-capable profiles/roles from the picker, don't just annotate
// them") -- the testable core of the picker filtering logic,
// independent of the Picker-tier UI.

func TestBuildInstanceProfileChoices_NoNoneEntry(t *testing.T) {
	fake := &fakeIAMClient{}
	choices, err := buildInstanceProfileChoices(context.Background(), fake, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range choices {
		if c.label == "(none)" {
			t.Errorf("expected no \"(none)\" entry, got %+v", choices)
		}
	}
	if len(choices) != 1 || !choices[0].createNew {
		t.Errorf("expected exactly one \"create new\" entry with no profiles, got %+v", choices)
	}
}

func TestBuildInstanceProfileChoices_FiltersOutNonCapableProfiles(t *testing.T) {
	profiles := []InstanceProfileInfo{
		{Name: "capable-profile", Roles: []string{"capable-role"}},
		{Name: "not-capable-profile", Roles: []string{"other-role"}},
	}
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"capable-role": {ssmManagedInstanceCorePolicyArn},
		},
	}
	choices, err := buildInstanceProfileChoices(context.Background(), fake, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(choices) != 2 { // capable-profile + "create new" -- not-capable-profile excluded
		t.Fatalf("got %d choices, want 2: %+v", len(choices), choices)
	}
	if choices[0].name != "capable-profile" {
		t.Errorf("got %+v, want capable-profile first", choices[0])
	}
	if !choices[1].createNew {
		t.Errorf("expected the last choice to be \"create new\", got %+v", choices[1])
	}
}

func TestBuildInstanceProfileChoices_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listAttachedRolePoliciesErr: errors.New("boom")}
	profiles := []InstanceProfileInfo{{Name: "p", Roles: []string{"r"}}}
	if _, err := buildInstanceProfileChoices(context.Background(), fake, profiles); err == nil {
		t.Fatal("expected an error")
	}
}

func TestBuildRoleChoices_FiltersOutNonCapableRoles(t *testing.T) {
	roles := []RoleInfo{{Name: "capable-role"}, {Name: "other-role"}}
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"capable-role": {ssmManagedInstanceCorePolicyArn},
		},
	}
	choices, err := buildRoleChoices(context.Background(), fake, roles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(choices) != 1 {
		t.Fatalf("got %d choices, want 1: %+v", len(choices), choices)
	}
	if choices[0].role.Name != "capable-role" {
		t.Errorf("got %+v, want capable-role", choices[0])
	}
}

func TestBuildRoleChoices_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listAttachedRolePoliciesErr: errors.New("boom")}
	roles := []RoleInfo{{Name: "r"}}
	if _, err := buildRoleChoices(context.Background(), fake, roles); err == nil {
		t.Fatal("expected an error")
	}
}

// The picker itself (pickInstanceProfileChoice/pickRole) can't be
// pipe-tested (Picker tier, DESIGN.md's full conversion punch list),
// but buildInstanceProfileChoices/buildRoleChoices run *before* the
// picker in both promptIAMInstanceProfileOrCreate and
// createInstanceProfileInteractive, so an error from the SSM-capability
// check itself is reachable and testable without ever touching the UI.

func TestPromptIAMInstanceProfileOrCreate_PropagatesSSMCheckError(t *testing.T) {
	fake := &fakeIAMClient{
		instanceProfiles:            []iamtypes.InstanceProfile{{InstanceProfileName: aws.String("p"), Roles: []iamtypes.Role{{RoleName: aws.String("r")}}}},
		listAttachedRolePoliciesErr: errors.New("boom"),
	}
	term, le, buf := newPipeEditor("")
	if _, err := promptIAMInstanceProfileOrCreate(context.Background(), term, fake, le, buf); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCreateInstanceProfileInteractive_PropagatesSSMCheckError(t *testing.T) {
	fake := &fakeIAMClient{
		roles:                       []iamtypes.Role{{RoleName: aws.String("r")}},
		listAttachedRolePoliciesErr: errors.New("boom"),
	}
	term, le, buf := newPipeEditor("")
	if _, _, err := createInstanceProfileInteractive(context.Background(), term, fake, le, buf); err == nil {
		t.Fatal("expected an error")
	}
}
