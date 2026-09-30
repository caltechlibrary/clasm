package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

var testOriginTag = config.OriginTagConfig{Key: "origin", DLDValue: "dld"}

func iamListFake() *fakeIAMClient {
	created := time.Date(2026, 7, 23, 17, 30, 0, 0, time.UTC)
	return &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("rdm-backups"), CreateDate: &created}},
		roleTags: map[string][]iamtypes.Tag{
			"rdm-backups": {{Key: aws.String("origin"), Value: aws.String("dld")}},
		},
		attachedPolicyArns: map[string][]string{"rdm-backups": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"}},
		instanceProfiles: []iamtypes.InstanceProfile{{
			InstanceProfileName: aws.String("rdm-backups"), CreateDate: &created,
			Roles: []iamtypes.Role{{RoleName: aws.String("rdm-backups")}},
		}},
		instanceProfileTags: map[string][]iamtypes.Tag{"rdm-backups": {{Key: aws.String("origin"), Value: aws.String("dld")}}},
		policies:            []iamtypes.Policy{{PolicyName: aws.String("bucket-rw"), Arn: aws.String("arn:aws:iam::1:policy/bucket-rw"), CreateDate: &created}},
	}
}

func TestRunShowIAMListingsCLI(t *testing.T) {
	ctx := context.Background()
	fake := iamListFake()
	var b bytes.Buffer
	if err := RunShowIAMRolesCLI(ctx, &b, fake, testOriginTag, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var roles []map[string]any
	if err := json.Unmarshal(b.Bytes(), &roles); err != nil || len(roles) != 1 {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if roles[0]["name"] != "rdm-backups" || roles[0]["origin"] != "dld" || roles[0]["dld_owned"] != true || roles[0]["ssm_capable"] != true {
		t.Errorf("role: %v", roles[0])
	}
	if tags, _ := roles[0]["tags"].(map[string]any); tags["origin"] != "dld" {
		t.Errorf("the full tag set must reach the JSON, got %v", roles[0]["tags"])
	}

	b.Reset()
	if err := RunShowIAMInstanceProfilesCLI(ctx, &b, fake, testOriginTag, ui.FormatJSONL); err != nil || strings.Count(b.String(), "\n") != 1 || !strings.Contains(b.String(), `"role_names":["rdm-backups"]`) {
		t.Errorf("profiles jsonl: %q err=%v", b.String(), err)
	}
	b.Reset()
	if err := RunShowIAMPoliciesCLI(ctx, &b, fake, testOriginTag, ui.FormatText); err != nil || !strings.HasPrefix(b.String(), "POLICY NAME") || !strings.Contains(b.String(), "bucket-rw") {
		t.Errorf("policies text: %q err=%v", b.String(), err)
	}
}

func TestRunShowIAMListingsCLI_AWSFailureIsNotAUsageError(t *testing.T) {
	fake := &fakeIAMClient{listRolesErr: errors.New("boom")}
	err := RunShowIAMRolesCLI(context.Background(), &bytes.Buffer{}, fake, testOriginTag, ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %v", err)
	}
}

func roleDetailFake() *fakeIAMClient {
	return &fakeIAMClient{
		getRoleOut: &iam.GetRoleOutput{Role: &iamtypes.Role{
			RoleName: aws.String("rdm-backups"), CreateDate: aws.Time(time.Date(2026, 7, 23, 17, 30, 0, 0, time.UTC)),
			AssumeRolePolicyDocument: aws.String("%7B%22Version%22%3A%222012-10-17%22%7D"),
			Tags:                     []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
		}},
		attachedPolicyArns: map[string][]string{"rdm-backups": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"}},
		rolePolicyNames:    map[string][]string{"rdm-backups": {"s3-write"}},
		instanceProfiles: []iamtypes.InstanceProfile{{
			InstanceProfileName: aws.String("rdm-backups-profile"),
			Roles:               []iamtypes.Role{{RoleName: aws.String("rdm-backups")}},
		}},
	}
}

func TestRunShowIAMRoleDetailCLI(t *testing.T) {
	var b bytes.Buffer
	if err := RunShowIAMRoleDetailCLI(context.Background(), &b, roleDetailFake(), "rdm-backups", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if got["name"] != "rdm-backups" || got["ssm_capable"] != true || got["create_date"] != "2026-07-23T17:30:00Z" {
		t.Errorf("got %v", got)
	}
	// The trust policy is a JSON document: it is embedded as one, not as an
	// escaped string a script would have to parse twice.
	if tp, ok := got["trust_policy"].(map[string]any); !ok || tp["Version"] != "2012-10-17" {
		t.Errorf("trust_policy = %#v, want the parsed document", got["trust_policy"])
	}
	if ap, _ := got["attached_policies"].([]any); len(ap) != 1 {
		t.Errorf("attached_policies = %v", got["attached_policies"])
	}
	if ip, _ := got["inline_policy_names"].([]any); len(ip) != 1 || ip[0] != "s3-write" {
		t.Errorf("inline_policy_names = %v", got["inline_policy_names"])
	}
	if rb, _ := got["referenced_by_profiles"].([]any); len(rb) != 1 || rb[0] != "rdm-backups-profile" {
		t.Errorf("referenced_by_profiles = %v", got["referenced_by_profiles"])
	}

	b.Reset()
	if err := RunShowIAMRoleDetailCLI(context.Background(), &b, roleDetailFake(), "rdm-backups", ui.FormatText); err != nil || strings.HasPrefix(b.String(), "\n") || !strings.Contains(b.String(), "rdm-backups") {
		t.Errorf("text: %q err=%v", b.String(), err)
	}
}

// A role that does not exist is the caller's mistake (exit 2); any other AWS
// failure is exit 1.
func TestRunShowIAMRoleDetailCLI_Errors(t *testing.T) {
	missing := &fakeIAMClient{getRoleErr: &iamtypes.NoSuchEntityException{Message: aws.String("not found")}}
	err := RunShowIAMRoleDetailCLI(context.Background(), &bytes.Buffer{}, missing, "nope", ui.FormatText)
	usageErr(t, err, "nope")

	broken := &fakeIAMClient{getRoleErr: errors.New("boom")}
	if err := RunShowIAMRoleDetailCLI(context.Background(), &bytes.Buffer{}, broken, "x", ui.FormatText); err == nil || CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %v", err)
	}
}

func TestRunShowIAMInstanceProfileDetailCLI(t *testing.T) {
	fake := &fakeIAMClient{
		getInstanceProfileOut: &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
			InstanceProfileName: aws.String("rdm-backups"), CreateDate: aws.Time(time.Date(2026, 7, 23, 17, 30, 0, 0, time.UTC)),
			Roles: []iamtypes.Role{{RoleName: aws.String("rdm-backups")}},
			Tags:  []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
		}},
		attachedPolicyArns: map[string][]string{"rdm-backups": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"}},
	}
	var b bytes.Buffer
	if err := RunShowIAMInstanceProfileDetailCLI(context.Background(), &b, fake, "rdm-backups", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	roles, _ := got["roles"].([]any)
	if got["name"] != "rdm-backups" || len(roles) != 1 || roles[0].(map[string]any)["ssm_capable"] != true || got["create_date"] != "2026-07-23T17:30:00Z" {
		t.Errorf("got %v", got)
	}

	missing := &fakeIAMClient{getInstanceProfileErr: &iamtypes.NoSuchEntityException{Message: aws.String("x")}}
	usageErr(t, RunShowIAMInstanceProfileDetailCLI(context.Background(), &bytes.Buffer{}, missing, "nope", ui.FormatText), "nope")
}

// Every read-only IAM form must send reads only: after running them all, no
// create, tag, attach or delete call may have been recorded.
func TestReadOnlyIAMForms_SendNoMutatingCall(t *testing.T) {
	fake := iamListFake()
	d := roleDetailFake()
	fake.getRoleOut, fake.attachedPolicyArns, fake.rolePolicyNames = d.getRoleOut, d.attachedPolicyArns, d.rolePolicyNames
	fake.getInstanceProfileOut = &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
		InstanceProfileName: aws.String("rdm-backups"), Roles: []iamtypes.Role{{RoleName: aws.String("rdm-backups")}}}}
	ctx := context.Background()
	var w bytes.Buffer
	for _, f := range []ui.Format{ui.FormatText, ui.FormatJSON} {
		_ = RunShowIAMRolesCLI(ctx, &w, fake, testOriginTag, f)
		_ = RunShowIAMInstanceProfilesCLI(ctx, &w, fake, testOriginTag, f)
		_ = RunShowIAMPoliciesCLI(ctx, &w, fake, testOriginTag, f)
		_ = RunShowIAMRoleDetailCLI(ctx, &w, fake, "rdm-backups", f)
		_ = RunShowIAMInstanceProfileDetailCLI(ctx, &w, fake, "rdm-backups", f)
	}
	if w.Len() == 0 {
		t.Fatal("the guard is vacuous: nothing was written")
	}
	if fake.lastCreateRoleInput != nil || fake.lastCreatePolicyInput != nil || fake.lastDeleteRoleInput != nil ||
		fake.lastTagRoleInput != nil || fake.lastUntagRoleInput != nil || fake.lastTagInstanceProfileInput != nil ||
		fake.lastUntagInstanceProfileInput != nil || fake.lastTagPolicyInput != nil || fake.lastUntagPolicyInput != nil ||
		fake.lastCreateInstanceProfileInput != nil || fake.lastAddRoleToInstanceProfileInput != nil {
		t.Error("a read-only IAM form sent a mutating call")
	}
}
