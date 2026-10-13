package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Delete Role and Delete Instance Profile (DR-0186) run on
// DR-0180's gate: a dry run by default that prints the plan (exit 0), --confirm
// <name> runs it, a mismatch is a usage error before any AWS call. Refusals the
// action itself has (not DLD-owned, still referenced) are errors (exit 1), even
// in a dry run, because confirming would not help.

var deleteOrigin = config.OriginTagConfig{Key: "origin", DLDValue: "dld"}

const dedicatedPolicyArn = "arn:aws:iam::123456789012:policy/ec2-test-restore-role-policy"

func dldRoleFake() *fakeIAMClient {
	return &fakeIAMClient{
		getRoleOut: &iam.GetRoleOutput{Role: &iamtypes.Role{
			RoleName: aws.String("ec2-test-restore-role"),
			Tags:     []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
		}},
		attachedPolicyArns: map[string][]string{"ec2-test-restore-role": {ssmManagedInstanceCorePolicyArn, dedicatedPolicyArn}},
		rolePolicyNames:    map[string][]string{"ec2-test-restore-role": {"InlineA"}},
		entitiesByPolicyArn: map[string]iam.ListEntitiesForPolicyOutput{
			dedicatedPolicyArn: {}, // unused elsewhere once detached
		},
	}
}

func noRoleMutation(t *testing.T, f *fakeIAMClient) {
	t.Helper()
	if f.lastDeleteRoleInput != nil || len(f.lastDetachRolePolicyInputs) != 0 || len(f.lastDeleteRolePolicyInputs) != 0 || len(f.lastDeletePolicyInputs) != 0 {
		t.Errorf("nothing may be changed: deleteRole=%v detaches=%d inline=%d deletePolicy=%d",
			f.lastDeleteRoleInput, len(f.lastDetachRolePolicyInputs), len(f.lastDeleteRolePolicyInputs), len(f.lastDeletePolicyInputs))
	}
}

func TestParseDeleteIAMRoleArgs(t *testing.T) {
	name, opts, err := ParseDeleteIAMRoleArgs([]string{"--confirm", "r1", "r1"})
	if err != nil || name != "r1" || strings.Join(opts.Confirms, ",") != "r1" {
		t.Fatalf("got %q %+v %v", name, opts, err)
	}
	name, opts, err = ParseDeleteIAMRoleArgs([]string{"r1"})
	if err != nil || name != "r1" || len(opts.Confirms) != 0 {
		t.Fatalf("got %q %+v %v", name, opts, err)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"--nope", "a"}, {"--confirm"}, {"  "}} {
		_, _, err := ParseDeleteIAMRoleArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm iam delete-role") {
			t.Errorf("%v: want a usage error carrying the usage, got %v", args, err)
		}
	}
	_, _, err = ParseDeleteIAMRoleArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "--confirm <role-name>") {
		t.Errorf("want help naming --confirm <role-name>, got %v", err)
	}
}

func TestParseDeleteIAMInstanceProfileArgs(t *testing.T) {
	name, opts, err := ParseDeleteIAMInstanceProfileArgs([]string{"--confirm", "p1", "p1"})
	if err != nil || name != "p1" || strings.Join(opts.Confirms, ",") != "p1" {
		t.Fatalf("got %q %+v %v", name, opts, err)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"--nope", "a"}} {
		_, _, err := ParseDeleteIAMInstanceProfileArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm iam delete-instance-profile") {
			t.Errorf("%v: want a usage error carrying the usage, got %v", args, err)
		}
	}
	_, _, err = ParseDeleteIAMInstanceProfileArgs([]string{"-h"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "--confirm <profile-name>") {
		t.Errorf("want help naming --confirm <profile-name>, got %v", err)
	}
}

func TestRunDeleteIAMRoleCLI_DryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	fake := dldRoleFake()
	var out bytes.Buffer
	err := RunDeleteIAMRoleCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(""), false, failingReader{t}, &out)
	if err != nil || CLIExitCode(err) != 0 {
		t.Fatalf("a dry run is not an error: %v", err)
	}
	noRoleMutation(t, fake)
	for _, want := range []string{
		"Plan for IAM role ec2-test-restore-role",
		"delete inline policy InlineA",
		"detach policy AmazonSSMManagedInstanceCore",
		"detach policy ec2-test-restore-role-policy",
		"delete role ec2-test-restore-role",
		"delete policy ec2-test-restore-role-policy if nothing else uses it",
		"Nothing was changed", "--confirm ec2-test-restore-role",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunDeleteIAMRoleCLI_PlanOmitsTheDedicatedPolicyWhenThereIsNone(t *testing.T) {
	fake := dldRoleFake()
	fake.attachedPolicyArns = map[string][]string{"ec2-test-restore-role": {ssmManagedInstanceCorePolicyArn}}
	var out bytes.Buffer
	if err := RunDeleteIAMRoleCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(""), false, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "if nothing else uses it") {
		t.Errorf("no dedicated policy, so no line about deleting one:\n%s", out.String())
	}
}

func TestRunDeleteIAMRoleCLI_ConfirmDeletesTheRoleAndItsDedicatedPolicy(t *testing.T) {
	fake := dldRoleFake()
	var out bytes.Buffer
	err := RunDeleteIAMRoleCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf("ec2-test-restore-role"), false, failingReader{t}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastDeleteRoleInput == nil || aws.ToString(fake.lastDeleteRoleInput.RoleName) != "ec2-test-restore-role" {
		t.Errorf("DeleteRole not called: %v", fake.lastDeleteRoleInput)
	}
	if len(fake.lastDeleteRolePolicyInputs) != 1 || len(fake.lastDetachRolePolicyInputs) != 2 || len(fake.lastDeletePolicyInputs) != 1 {
		t.Errorf("inline=%d detaches=%d policyDeletes=%d", len(fake.lastDeleteRolePolicyInputs), len(fake.lastDetachRolePolicyInputs), len(fake.lastDeletePolicyInputs))
	}
	if !strings.Contains(out.String(), "Deleted role ec2-test-restore-role") {
		t.Errorf("output = %q", out.String())
	}
}

// A typo in --confirm fails before the first AWS call: the fake would return its
// error to any call, so a *UsageError proves none was made.
func TestRunDeleteIAMRoleCLI_ConfirmMismatchIsAUsageErrorBeforeAnyAWSCall(t *testing.T) {
	fake := dldRoleFake()
	fake.getRoleErr = errors.New("an AWS call was made")
	err := RunDeleteIAMRoleCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf("other-role"), false, failingReader{t}, &bytes.Buffer{})
	var ue *UsageError
	if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
		t.Fatalf("want a *UsageError, got %v", err)
	}
	noRoleMutation(t, fake)
}

func TestRunDeleteIAMRoleCLI_UnknownRoleIsAUsageError(t *testing.T) {
	fake := dldRoleFake()
	fake.getRoleErr = &iamtypes.NoSuchEntityException{}
	err := RunDeleteIAMRoleCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ghost", confirmsOf("ghost"), false, failingReader{t}, &bytes.Buffer{})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `no IAM role found named "ghost"`) {
		t.Fatalf("got %v", err)
	}
	noRoleMutation(t, fake)
}

func TestRunDeleteIAMRoleCLI_NotDLDOwnedIsRefusedEvenWithConfirm(t *testing.T) {
	fake := dldRoleFake()
	fake.getRoleOut.Role.Tags = nil
	var out bytes.Buffer
	for _, confirm := range []string{"", "ec2-test-restore-role"} {
		err := RunDeleteIAMRoleCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(confirm), false, failingReader{t}, &out)
		if !errors.Is(err, inventory.ErrNotDLDOwned) || CLIExitCode(err) != 1 {
			t.Errorf("confirm=%q: want ErrNotDLDOwned (exit 1), got %v", confirm, err)
		}
	}
	noRoleMutation(t, fake)
}

func TestRunDeleteIAMRoleCLI_StillInAnInstanceProfileIsRefusedEvenInADryRun(t *testing.T) {
	fake := dldRoleFake()
	fake.instanceProfiles = []iamtypes.InstanceProfile{{
		InstanceProfileName: aws.String("restore-profile"),
		Roles:               []iamtypes.Role{{RoleName: aws.String("ec2-test-restore-role")}},
	}}
	var out bytes.Buffer
	for _, confirm := range []string{"", "ec2-test-restore-role"} {
		err := RunDeleteIAMRoleCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(confirm), false, failingReader{t}, &out)
		if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "restore-profile") || !strings.Contains(err.Error(), "remove it from each first") {
			t.Errorf("confirm=%q: want a refusal naming the profile and the remedy, got %v", confirm, err)
		}
	}
	noRoleMutation(t, fake)
}

func TestRunDeleteIAMRoleCLI_TerminalPromptsForTheName(t *testing.T) {
	fake := dldRoleFake()
	w, in, buf := newPipeEditor("ec2-test-restore-role\n")
	if err := RunDeleteIAMRoleCLI(context.Background(), w, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(""), true, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if fake.lastDeleteRoleInput == nil {
		t.Errorf("the typed name must delete:\n%s", buf.String())
	}

	fake = dldRoleFake()
	w, in, buf = newPipeEditor("nope\n")
	if err := RunDeleteIAMRoleCLI(context.Background(), w, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf(""), true, in, buf); err != nil {
		t.Fatal(err)
	}
	noRoleMutation(t, fake)
}

// An AWS failure part way is reported (exit 1) and stops what follows.
func TestRunDeleteIAMRoleCLI_AWSFailureIsExitOne(t *testing.T) {
	fake := dldRoleFake()
	fake.deleteRoleErr = errors.New("DeleteConflict")
	err := RunDeleteIAMRoleCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", confirmsOf("ec2-test-restore-role"), false, failingReader{t}, &bytes.Buffer{})
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "DeleteConflict") {
		t.Fatalf("got %v", err)
	}
	if len(fake.lastDeletePolicyInputs) != 0 {
		t.Errorf("the dedicated policy must not be deleted after DeleteRole failed")
	}
}

func dldProfileFake(roles ...string) *fakeIAMClient {
	var rs []iamtypes.Role
	for _, r := range roles {
		rs = append(rs, iamtypes.Role{RoleName: aws.String(r)})
	}
	return &fakeIAMClient{getInstanceProfileOut: &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
		InstanceProfileName: aws.String("restore-profile"),
		Roles:               rs,
		Tags:                []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
	}}}
}

func TestRunDeleteIAMInstanceProfileCLI_DryRunConfirmAndMismatch(t *testing.T) {
	fake := dldProfileFake()
	var out bytes.Buffer
	if err := RunDeleteIAMInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "restore-profile", confirmsOf(""), false, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 0 {
		t.Errorf("a dry run must not delete")
	}
	for _, want := range []string{"Plan for IAM instance profile restore-profile", "delete instance profile restore-profile", "--confirm restore-profile"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := RunDeleteIAMInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "restore-profile", confirmsOf("restore-profile"), false, failingReader{t}, &out); err != nil {
		t.Fatal(err)
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 1 || aws.ToString(fake.lastDeleteInstanceProfileInputs[0].InstanceProfileName) != "restore-profile" {
		t.Errorf("DeleteInstanceProfile not called: %v", fake.lastDeleteInstanceProfileInputs)
	}
	if !strings.Contains(out.String(), "Deleted instance profile restore-profile") {
		t.Errorf("output = %q", out.String())
	}

	fake = dldProfileFake()
	fake.getInstanceProfileErr = errors.New("an AWS call was made")
	err := RunDeleteIAMInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "restore-profile", confirmsOf("other"), false, failingReader{t}, &out)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("a mismatch must be a usage error before any AWS call, got %v", err)
	}
}

func TestRunDeleteIAMInstanceProfileCLI_Refusals(t *testing.T) {
	// Not DLD-owned.
	fake := dldProfileFake()
	fake.getInstanceProfileOut.InstanceProfile.Tags = nil
	err := RunDeleteIAMInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "restore-profile", confirmsOf("restore-profile"), false, failingReader{t}, &bytes.Buffer{})
	if !errors.Is(err, inventory.ErrNotDLDOwned) || len(fake.lastDeleteInstanceProfileInputs) != 0 {
		t.Errorf("not DLD-owned: got %v, deletes=%d", err, len(fake.lastDeleteInstanceProfileInputs))
	}
	// A role is still in it: refused in a dry run too, naming the remedy.
	fake = dldProfileFake("ec2-test-restore-role")
	for _, confirm := range []string{"", "restore-profile"} {
		err = RunDeleteIAMInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "restore-profile", confirmsOf(confirm), false, failingReader{t}, &bytes.Buffer{})
		if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "ec2-test-restore-role") || !strings.Contains(err.Error(), "remove") {
			t.Errorf("confirm=%q: want a refusal naming the role, got %v", confirm, err)
		}
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 0 {
		t.Errorf("a profile that still has a role must not be deleted")
	}
	// Unknown.
	fake = dldProfileFake()
	fake.getInstanceProfileErr = &iamtypes.NoSuchEntityException{}
	err = RunDeleteIAMInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ghost", confirmsOf("ghost"), false, failingReader{t}, &bytes.Buffer{})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `no IAM instance profile found named "ghost"`) {
		t.Errorf("unknown: got %v", err)
	}
}
