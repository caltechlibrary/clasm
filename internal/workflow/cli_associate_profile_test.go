package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Associate/replace IAM instance profile" (DR-0185): an
// in-place swap on a running instance, safe to repeat. Asking for the profile
// the instance already has changes nothing and succeeds, so a script can swap on
// and swap back without reading state first.

var swapInstance = inventory.Instance{InstanceID: "i-057d8cc298da8f186", Name: "caltechauthors-rsdoiel-v13b", Region: "us-west-2", State: "running"}

func swapFakes(currentARN string) (*fakeEC2Client, *fakeIAMClient) {
	ec2Fake := &fakeEC2Client{}
	if currentARN != "" {
		ec2Fake.currentInstanceProfileAssociationID = "iip-assoc-1"
		ec2Fake.currentInstanceProfileARN = currentARN
	}
	iamFake := &fakeIAMClient{
		instanceProfiles: []iamtypes.InstanceProfile{
			{InstanceProfileName: aws.String("restore-profile"), Roles: []iamtypes.Role{{RoleName: aws.String("restore-role")}}},
			{InstanceProfileName: aws.String("rdm-backups"), Roles: []iamtypes.Role{{RoleName: aws.String("rdm-backups")}}},
			{InstanceProfileName: aws.String("no-ssm-profile"), Roles: []iamtypes.Role{{RoleName: aws.String("no-ssm-role")}}},
		},
		attachedPolicyArns: map[string][]string{
			"restore-role": {ssmManagedInstanceCorePolicyArn}, "rdm-backups": {ssmManagedInstanceCorePolicyArn},
			"no-ssm-role": {"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"},
		},
	}
	return ec2Fake, iamFake
}

func runSwap(ec2Fake *fakeEC2Client, iamFake *fakeIAMClient, inst, profile string, format ui.Format) (string, error) {
	var out bytes.Buffer
	err := RunAssociateInstanceProfileCLI(context.Background(), &out, map[string]awsclient.EC2API{"us-west-2": ec2Fake}, iamFake,
		[]inventory.Instance{swapInstance}, inst, profile, format)
	return out.String(), err
}

func noSwap(t *testing.T, f *fakeEC2Client) {
	t.Helper()
	if f.lastAssociateIamInstanceProfileInput != nil || f.lastReplaceIamInstanceProfileAssociationInput != nil {
		t.Errorf("nothing may be changed: associate=%v replace=%v", f.lastAssociateIamInstanceProfileInput, f.lastReplaceIamInstanceProfileAssociationInput)
	}
}

func TestParseAssociateInstanceProfileArgs(t *testing.T) {
	inst, profile, format, err := ParseAssociateInstanceProfileArgs([]string{"-json", "box", "restore-profile"})
	if err != nil || inst != "box" || profile != "restore-profile" || format != ui.FormatJSON {
		t.Fatalf("got %q %q %v %v", inst, profile, format, err)
	}
	if inst, profile, _, err = ParseAssociateInstanceProfileArgs([]string{" box ", " restore-profile "}); err != nil || inst != "box" || profile != "restore-profile" {
		t.Fatalf("surrounding blanks are trimmed: got %q %q %v", inst, profile, err)
	}
	if _, _, format, err = ParseAssociateInstanceProfileArgs([]string{"box", "p"}); err != nil || format != ui.FormatText {
		t.Fatalf("text is the default: %v %v", format, err)
	}
	for _, args := range [][]string{{}, {"box"}, {"a", "b", "c"}, {"-text", "-json", "a", "b"}, {"-jsonl", "a", "b"}, {"a", "  "}, {"  ", "b"}} {
		_, _, _, err := ParseAssociateInstanceProfileArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm compute associate-replace-iam-instance-profile") {
			t.Errorf("%v: want a usage error carrying the usage, got %v", args, err)
		}
	}
	_, _, _, err = ParseAssociateInstanceProfileArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "already has") {
		t.Errorf("want help that says a repeat is a no-op, got %v", err)
	}
}

func TestRunAssociateInstanceProfileCLI_AssociatesWhenThereIsNone(t *testing.T) {
	ec2Fake, iamFake := swapFakes("")
	out, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText)
	if err != nil {
		t.Fatal(err)
	}
	in := ec2Fake.lastAssociateIamInstanceProfileInput
	if in == nil || aws.ToString(in.InstanceId) != "i-057d8cc298da8f186" || aws.ToString(in.IamInstanceProfile.Name) != "restore-profile" {
		t.Fatalf("associate input = %+v", in)
	}
	if ec2Fake.lastReplaceIamInstanceProfileAssociationInput != nil {
		t.Errorf("nothing to replace")
	}
	if !strings.Contains(out, "Associated instance profile") || !strings.Contains(out, "restore-profile") {
		t.Errorf("output = %q", out)
	}
}

func TestRunAssociateInstanceProfileCLI_ReplacesAndNamesTheOldProfile(t *testing.T) {
	ec2Fake, iamFake := swapFakes("arn:aws:iam::123456789012:instance-profile/rdm-backups")
	out, err := runSwap(ec2Fake, iamFake, "i-057d8cc298da8f186", "restore-profile", ui.FormatText)
	if err != nil {
		t.Fatal(err)
	}
	in := ec2Fake.lastReplaceIamInstanceProfileAssociationInput
	if in == nil || aws.ToString(in.AssociationId) != "iip-assoc-1" || aws.ToString(in.IamInstanceProfile.Name) != "restore-profile" {
		t.Fatalf("replace input = %+v", in)
	}
	if ec2Fake.lastAssociateIamInstanceProfileInput != nil {
		t.Errorf("an instance with an association is replaced, not associated again")
	}
	if !strings.Contains(out, "rdm-backups") || !strings.Contains(out, "restore-profile") {
		t.Errorf("the output must show the old and the new profile: %q", out)
	}
}

// The repeat run: the same profile, including a profile with an IAM path in its
// ARN, is recognised by name and nothing is sent.
func TestRunAssociateInstanceProfileCLI_AlreadyInPlaceIsANoOp(t *testing.T) {
	for _, arn := range []string{
		"arn:aws:iam::123456789012:instance-profile/restore-profile",
		"arn:aws:iam::123456789012:instance-profile/dld/restore-profile",
	} {
		ec2Fake, iamFake := swapFakes(arn)
		out, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText)
		if err != nil || CLIExitCode(err) != 0 {
			t.Fatalf("%s: a repeat is a success: %v", arn, err)
		}
		noSwap(t, ec2Fake)
		if !strings.Contains(out, "already has") {
			t.Errorf("%s: output = %q", arn, out)
		}
	}
}

func TestRunAssociateInstanceProfileCLI_ANameThatOnlyEndsTheSameIsNotTheSameProfile(t *testing.T) {
	ec2Fake, iamFake := swapFakes("arn:aws:iam::123456789012:instance-profile/old-restore-profile")
	if _, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if ec2Fake.lastReplaceIamInstanceProfileAssociationInput == nil {
		t.Errorf("old-restore-profile is a different profile, so it must be replaced")
	}
}

func TestRunAssociateInstanceProfileCLI_JSON(t *testing.T) {
	for _, tc := range []struct{ current, profile, action, previous string }{
		{"", "restore-profile", "associated", ""},
		{"arn:aws:iam::1:instance-profile/rdm-backups", "restore-profile", "replaced", "rdm-backups"},
		{"arn:aws:iam::1:instance-profile/restore-profile", "restore-profile", "unchanged", "restore-profile"},
	} {
		ec2Fake, iamFake := swapFakes(tc.current)
		out, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", tc.profile, ui.FormatJSON)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`"instance_id": "i-057d8cc298da8f186"`, `"name": "caltechauthors-rsdoiel-v13b"`, `"profile": "restore-profile"`,
			`"previous_profile": "` + tc.previous + `"`, `"action": "` + tc.action + `"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: JSON missing %q:\n%s", tc.action, want, out)
			}
		}
	}
}

func TestRunAssociateInstanceProfileCLI_UnknownInstanceOrProfileIsAUsageError(t *testing.T) {
	for name, tc := range map[string]struct{ inst, profile string }{
		"instance": {"no-such-box", "restore-profile"},
		"profile":  {"caltechauthors-rsdoiel-v13b", "no-such-profile"},
	} {
		ec2Fake, iamFake := swapFakes("")
		_, err := runSwap(ec2Fake, iamFake, tc.inst, tc.profile, ui.FormatText)
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
			t.Errorf("unknown %s: want a usage error, got %v", name, err)
		}
		noSwap(t, ec2Fake)
	}
}

// DR-0185: the SSM-capable enforcement applies. The refusal is the state forbidding
// the swap, not a mistyped argument, so it is exit 1.
func TestRunAssociateInstanceProfileCLI_ProfileWithoutSSMIsRefusedWithExitOne(t *testing.T) {
	ec2Fake, iamFake := swapFakes("arn:aws:iam::1:instance-profile/rdm-backups")
	_, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "no-ssm-profile", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "not SSM-capable") {
		t.Fatalf("want an exit-1 refusal naming SSM, got %v", err)
	}
	noSwap(t, ec2Fake)
}

func TestRunAssociateInstanceProfileCLI_AWSFailuresAreExitOne(t *testing.T) {
	ec2Fake, iamFake := swapFakes("")
	ec2Fake.associateIamInstanceProfileErr = errors.New("IncorrectState")
	if _, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText); err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "IncorrectState") {
		t.Errorf("associate failure: got %v", err)
	}
	ec2Fake, iamFake = swapFakes("arn:aws:iam::1:instance-profile/rdm-backups")
	ec2Fake.replaceIamInstanceProfileAssociationErr = errors.New("InvalidParameterValue")
	if _, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText); err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "InvalidParameterValue") {
		t.Errorf("replace failure: got %v", err)
	}
	ec2Fake, iamFake = swapFakes("")
	ec2Fake.describeIamInstanceProfileAssociationsErr = errors.New("throttled")
	if _, err := runSwap(ec2Fake, iamFake, "caltechauthors-rsdoiel-v13b", "restore-profile", ui.FormatText); err == nil || CLIExitCode(err) != 1 {
		t.Errorf("describe failure: got %v", err)
	}
	noSwap(t, ec2Fake)
}
