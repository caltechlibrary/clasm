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

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// create-instance-profile and remove-role-from-instance-profile (DR-0188) close the
// scripted cycle: the template makes a role, the swap needs a profile, and a role
// cannot be deleted while it is in one.

func roleExistsFake() *fakeIAMClient {
	return &fakeIAMClient{getRoleOut: &iam.GetRoleOutput{Role: &iamtypes.Role{
		RoleName: aws.String("ec2-test-restore-role"),
		Tags:     []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
	}}}
}

func TestParseCreateInstanceProfileArgs(t *testing.T) {
	profile, role, format, err := ParseCreateInstanceProfileArgs([]string{"-json", " restore-profile ", "ec2-test-restore-role"})
	if err != nil || profile != "restore-profile" || role != "ec2-test-restore-role" || format != ui.FormatJSON {
		t.Fatalf("got %q %q %v %v", profile, role, format, err)
	}
	if _, _, format, err = ParseCreateInstanceProfileArgs([]string{"p", "r"}); err != nil || format != ui.FormatText {
		t.Fatalf("text is the default: %v %v", format, err)
	}
	for _, args := range [][]string{{}, {"p"}, {"a", "b", "c"}, {"-text", "-json", "a", "b"}, {"-jsonl", "a", "b"}, {"  ", "b"}, {"a", "  "}} {
		_, _, _, err := ParseCreateInstanceProfileArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm iam create-instance-profile") {
			t.Errorf("%v: want a usage error carrying the usage, got %v", args, err)
		}
	}
	_, _, _, err = ParseCreateInstanceProfileArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "DLD-owned") {
		t.Errorf("want help that says the profile is tagged DLD-owned, got %v", err)
	}
}

func TestRunCreateInstanceProfileCLI_CreatesTagsAndAddsTheRole(t *testing.T) {
	fake := roleExistsFake()
	var out bytes.Buffer
	err := RunCreateInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "restore-profile", "ec2-test-restore-role", ui.FormatText)
	if err != nil {
		t.Fatal(err)
	}
	in := fake.lastCreateInstanceProfileInput
	if in == nil || aws.ToString(in.InstanceProfileName) != "restore-profile" {
		t.Fatalf("create input = %+v", in)
	}
	if len(in.Tags) != 1 || aws.ToString(in.Tags[0].Key) != "origin" || aws.ToString(in.Tags[0].Value) != "dld" {
		t.Errorf("the profile must be tagged DLD-owned so delete-instance-profile can remove it: %+v", in.Tags)
	}
	add := fake.lastAddRoleToInstanceProfileInput
	if add == nil || aws.ToString(add.InstanceProfileName) != "restore-profile" || aws.ToString(add.RoleName) != "ec2-test-restore-role" {
		t.Errorf("add input = %+v", add)
	}
	if !strings.Contains(out.String(), "Created instance profile restore-profile") || !strings.Contains(out.String(), "ec2-test-restore-role") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunCreateInstanceProfileCLI_NoOriginValueMeansNoTags(t *testing.T) {
	fake := roleExistsFake()
	if err := RunCreateInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, config.OriginTagConfig{Key: "origin"}, "p", "ec2-test-restore-role", ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if len(fake.lastCreateInstanceProfileInput.Tags) != 0 {
		t.Errorf("no configured DLD value, so no tag: %+v", fake.lastCreateInstanceProfileInput.Tags)
	}
}

func TestRunCreateInstanceProfileCLI_JSON(t *testing.T) {
	var out bytes.Buffer
	if err := RunCreateInstanceProfileCLI(context.Background(), &out, roleExistsFake(), deleteOrigin, "restore-profile", "ec2-test-restore-role", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"instance_profile_name": "restore-profile"`, `"instance_profile_arn": "arn:aws:iam::123456789012:instance-profile/restore-profile"`, `"role_name": "ec2-test-restore-role"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunCreateInstanceProfileCLI_UnknownRoleIsAUsageErrorAndCreatesNothing(t *testing.T) {
	fake := roleExistsFake()
	fake.getRoleErr = &iamtypes.NoSuchEntityException{}
	err := RunCreateInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "p", "ghost", ui.FormatText)
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `no IAM role found named "ghost"`) {
		t.Fatalf("got %v", err)
	}
	if fake.lastCreateInstanceProfileInput != nil {
		t.Errorf("nothing may be created for an unknown role")
	}
}

func TestRunCreateInstanceProfileCLI_ADuplicateNameIsExitOneAndAddsNothing(t *testing.T) {
	fake := roleExistsFake()
	fake.createInstanceProfileErr = newDuplicateInstanceProfileError()
	err := RunCreateInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "p", "ec2-test-restore-role", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v", err)
	}
	if fake.lastAddRoleToInstanceProfileInput != nil {
		t.Errorf("the role must not be added to a profile that was not created")
	}
}

// If adding the role fails the profile exists, empty; the error says so and how to clean it up.
func TestRunCreateInstanceProfileCLI_AddRoleFailureNamesTheProfileLeftBehind(t *testing.T) {
	fake := roleExistsFake()
	fake.addRoleToInstanceProfile = errors.New("LimitExceeded")
	err := RunCreateInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "restore-profile", "ec2-test-restore-role", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 {
		t.Fatalf("got %v", err)
	}
	for _, want := range []string{"LimitExceeded", "restore-profile", "delete-instance-profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q: %v", want, err)
		}
	}
}

// --- remove-role-from-instance-profile ---

func memberFakes() *fakeIAMClient {
	fake := dldProfileFake("ec2-test-restore-role")
	fake.getRoleOut = roleExistsFake().getRoleOut
	return fake
}

func noRemoval(t *testing.T, f *fakeIAMClient) {
	t.Helper()
	if len(f.lastRemoveRoleFromInstanceProfileInputs) != 0 {
		t.Errorf("nothing may be removed: %v", f.lastRemoveRoleFromInstanceProfileInputs)
	}
}

func TestParseRemoveRoleFromInstanceProfileArgs(t *testing.T) {
	role, profile, opts, err := ParseRemoveRoleFromInstanceProfileArgs([]string{"--confirm", "p1", "r1", "p1"})
	if err != nil || role != "r1" || profile != "p1" || strings.Join(opts.Confirms, ",") != "p1" {
		t.Fatalf("got %q %q %+v %v", role, profile, opts, err)
	}
	for _, args := range [][]string{{}, {"r"}, {"a", "b", "c"}, {"--nope", "a", "b"}, {"  ", "b"}, {"a", "  "}} {
		_, _, _, err := ParseRemoveRoleFromInstanceProfileArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm iam remove-role-from-instance-profile") {
			t.Errorf("%v: want a usage error carrying the usage, got %v", args, err)
		}
	}
	_, _, _, err = ParseRemoveRoleFromInstanceProfileArgs([]string{"-h"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "--confirm <profile-name>") {
		t.Errorf("want help naming --confirm <profile-name>, got %v", err)
	}
}

func TestRunRemoveRoleFromInstanceProfileCLI_DryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	fake := memberFakes()
	var out bytes.Buffer
	err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", nil, false, failingReader{t}, &out)
	if err != nil || CLIExitCode(err) != 0 {
		t.Fatalf("a dry run is not an error: %v", err)
	}
	noRemoval(t, fake)
	for _, want := range []string{
		"Plan for IAM instance profile restore-profile",
		"remove role ec2-test-restore-role from instance profile restore-profile",
		"lose", "immediately",
		"Nothing was changed", "--confirm restore-profile",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunRemoveRoleFromInstanceProfileCLI_ConfirmRemovesTheRole(t *testing.T) {
	fake := memberFakes()
	var out bytes.Buffer
	err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &out, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", []string{"restore-profile"}, false, failingReader{t}, &out)
	if err != nil {
		t.Fatal(err)
	}
	in := fake.lastRemoveRoleFromInstanceProfileInputs
	if len(in) != 1 || aws.ToString(in[0].InstanceProfileName) != "restore-profile" || aws.ToString(in[0].RoleName) != "ec2-test-restore-role" {
		t.Errorf("remove inputs = %+v", in)
	}
	if !strings.Contains(out.String(), "Removed role ec2-test-restore-role from instance profile restore-profile") {
		t.Errorf("output = %q", out.String())
	}
}

// --confirm names the profile, not the role.
func TestRunRemoveRoleFromInstanceProfileCLI_ConfirmMustNameTheProfileBeforeAnyAWSCall(t *testing.T) {
	for _, wrong := range []string{"ec2-test-restore-role", "other"} {
		fake := memberFakes()
		fake.getInstanceProfileErr = errors.New("an AWS call was made")
		err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", []string{wrong}, false, failingReader{t}, &bytes.Buffer{})
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
			t.Errorf("--confirm %q: want a usage error, got %v", wrong, err)
		}
		noRemoval(t, fake)
	}
}

func TestRunRemoveRoleFromInstanceProfileCLI_RefusalsHoldEvenWithConfirm(t *testing.T) {
	// A profile that is not DLD-owned.
	fake := memberFakes()
	fake.getInstanceProfileOut.InstanceProfile.Tags = nil
	err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", []string{"restore-profile"}, false, failingReader{t}, &bytes.Buffer{})
	if !errors.Is(err, inventory.ErrNotDLDOwned) || CLIExitCode(err) != 1 {
		t.Errorf("profile not DLD-owned: got %v", err)
	}
	noRemoval(t, fake)

	// A role that is not DLD-owned.
	fake = memberFakes()
	fake.getRoleOut.Role.Tags = nil
	err = RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", []string{"restore-profile"}, false, failingReader{t}, &bytes.Buffer{})
	if !errors.Is(err, inventory.ErrNotDLDOwned) || CLIExitCode(err) != 1 {
		t.Errorf("role not DLD-owned: got %v", err)
	}
	noRemoval(t, fake)

	// A role that is not a member: refused in a dry run too, naming what is in the profile.
	fake = dldProfileFake("another-role")
	fake.getRoleOut = roleExistsFake().getRoleOut
	for _, confirms := range [][]string{nil, {"restore-profile"}} {
		err = RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", confirms, false, failingReader{t}, &bytes.Buffer{})
		if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "not a member") || !strings.Contains(err.Error(), "another-role") {
			t.Errorf("confirms %v: want a not-a-member refusal naming the real members, got %v", confirms, err)
		}
	}
	noRemoval(t, fake)
}

func TestRunRemoveRoleFromInstanceProfileCLI_UnknownRoleOrProfileIsAUsageError(t *testing.T) {
	fake := memberFakes()
	fake.getInstanceProfileErr = &iamtypes.NoSuchEntityException{}
	err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "ghost", []string{"ghost"}, false, failingReader{t}, &bytes.Buffer{})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `no IAM instance profile found named "ghost"`) {
		t.Errorf("unknown profile: got %v", err)
	}
	fake = memberFakes()
	fake.getRoleErr = &iamtypes.NoSuchEntityException{}
	err = RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ghost", "restore-profile", []string{"restore-profile"}, false, failingReader{t}, &bytes.Buffer{})
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, `no IAM role found named "ghost"`) {
		t.Errorf("unknown role: got %v", err)
	}
	noRemoval(t, fake)
}

func TestRunRemoveRoleFromInstanceProfileCLI_TerminalPromptsForTheProfileName(t *testing.T) {
	fake := memberFakes()
	w, in, buf := newPipeEditor("restore-profile\n")
	if err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), w, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", nil, true, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if len(fake.lastRemoveRoleFromInstanceProfileInputs) != 1 {
		t.Errorf("the typed name must remove:\n%s", buf.String())
	}
	fake = memberFakes()
	w, in, buf = newPipeEditor("nope\n")
	if err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), w, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", nil, true, in, buf); err != nil {
		t.Fatal(err)
	}
	noRemoval(t, fake)
}

func TestRunRemoveRoleFromInstanceProfileCLI_AWSFailureIsExitOne(t *testing.T) {
	fake := memberFakes()
	fake.removeRoleFromInstanceProfileErr = errors.New("DeleteConflict")
	err := RunRemoveRoleFromInstanceProfileCLI(context.Background(), &bytes.Buffer{}, fake, deleteOrigin, "ec2-test-restore-role", "restore-profile", []string{"restore-profile"}, false, failingReader{t}, &bytes.Buffer{})
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "DeleteConflict") {
		t.Fatalf("got %v", err)
	}
}

// --- the interactive Create Instance Profile leaf ---

func TestCreateIAMInstanceProfileConfirmed_TheNameDefaultsToTheRoleAndTheProfileIsTagged(t *testing.T) {
	fake := roleExistsFake()
	term, in, buf := newPipeEditor("\ny\n") // accept the default name, confirm
	if err := createIAMInstanceProfileConfirmed(context.Background(), term, fake, deleteOrigin, "ec2-test-restore-role", in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	got := fake.lastCreateInstanceProfileInput
	if got == nil || aws.ToString(got.InstanceProfileName) != "ec2-test-restore-role" || len(got.Tags) != 1 {
		t.Errorf("create input = %+v", got)
	}
	if fake.lastAddRoleToInstanceProfileInput == nil {
		t.Errorf("the role must be added")
	}
}

func TestCreateIAMInstanceProfileConfirmed_DecliningCreatesNothing(t *testing.T) {
	fake := roleExistsFake()
	term, in, buf := newPipeEditor("my-profile\nn\n")
	if err := createIAMInstanceProfileConfirmed(context.Background(), term, fake, deleteOrigin, "ec2-test-restore-role", in, buf); err != nil {
		t.Fatal(err)
	}
	if fake.lastCreateInstanceProfileInput != nil || !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("declining must create nothing and say so:\n%s", buf.String())
	}
}

// The pick-or-create step of a launch (and of Associate/replace) made untagged profiles,
// which clasm then could not delete (DR-0189). With the IAM client wrapped as main wraps
// it, that path is tagged too -- without the wrapper it still is not, which is why
// main must wrap.
func TestTheLaunchFlowsProfileCreationIsTaggedOnceTheClientIsWrapped(t *testing.T) {
	origin := func() (string, string) { return "origin", "dld" }

	bare := &fakeIAMClient{}
	term, in, buf := newPipeEditor("\n") // accept the default profile name
	if _, _, err := createInstanceProfileForRole(context.Background(), term, bare, RoleInfo{Name: "ec2-test-restore-role"}, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if len(bare.lastCreateInstanceProfileInput.Tags) != 0 {
		t.Fatalf("the interactive creation itself adds no tag, the wrapper does: %+v", bare.lastCreateInstanceProfileInput.Tags)
	}

	inner := &fakeIAMClient{}
	term, in, buf = newPipeEditor("\n")
	if _, _, err := createInstanceProfileForRole(context.Background(), term, awsclient.TagIAMCreations(inner, origin), RoleInfo{Name: "ec2-test-restore-role"}, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	tags := inner.lastCreateInstanceProfileInput.Tags
	if len(tags) != 1 || aws.ToString(tags[0].Key) != "origin" || aws.ToString(tags[0].Value) != "dld" {
		t.Errorf("the profile made by the launch flow must be tagged DLD-owned: %+v", tags)
	}
}

// An explicit tag from a form is not doubled by the wrapper.
func TestTheFormsTagAndTheWrapperDoNotDoubleUp(t *testing.T) {
	inner := roleExistsFake()
	client := awsclient.TagIAMCreations(inner, func() (string, string) { return "origin", "dld" })
	if err := RunCreateInstanceProfileCLI(context.Background(), &bytes.Buffer{}, client, deleteOrigin, "restore-profile", "ec2-test-restore-role", ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if n := len(inner.lastCreateInstanceProfileInput.Tags); n != 1 {
		t.Errorf("want exactly one origin tag, got %d: %+v", n, inner.lastCreateInstanceProfileInput.Tags)
	}
}
