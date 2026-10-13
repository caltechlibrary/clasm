package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Create Role from Template" (DR-0186).

func TestIAMRoleTemplateSlugs_AreTheMechanicalRuleAndUnique(t *testing.T) {
	want := []string{
		"static-website-s3-cloudfront",
		"rdm-repository-instance",
		"bridge-service",
		"patron-facing-service",
		"data-processing",
		"rdm-restore-source-read-only",
	}
	got := IAMRoleTemplateSlugs()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("slugs = %v, want %v", got, want)
	}
}

func TestIAMRoleTemplateParamOption_NameDerivedFromKey(t *testing.T) {
	for key, want := range map[string]string{
		"bucket_arn":        "bucket",
		"distribution_arn":  "distribution",
		"backup_bucket_arn": "backup-bucket",
		"second_bucket_arn": "second-bucket",
		"log_group_arn":     "log-group",
		"secret_arn":        "secret",
		"data_bucket_arn":   "data-bucket",
		"key_prefix":        "key-prefix",
	} {
		if got := iamRoleTemplateParamOption(key); got != want {
			t.Errorf("option for %q = %q, want %q", key, got, want)
		}
	}
}

func TestParseCreateRoleFromTemplateArgs_Valid(t *testing.T) {
	opts, err := ParseCreateRoleFromTemplateArgs([]string{
		"-json", "--backup-bucket", "sql-backups.library.caltech.edu", "--second-bucket", "opensearch-backups.library.caltech.edu",
		"--key-prefix", "caltechauthors-v13", "rdm-restore-source-read-only", "ec2-test-restore-role",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Template.Label != "RDM Restore Source (read-only)" || opts.RoleName != "ec2-test-restore-role" || opts.Format != ui.FormatJSON {
		t.Errorf("got %+v", opts)
	}
	want := map[string]string{
		"backup_bucket_arn": "sql-backups.library.caltech.edu",
		"second_bucket_arn": "opensearch-backups.library.caltech.edu",
		"key_prefix":        "caltechauthors-v13",
	}
	for k, v := range want {
		if opts.Params[k] != v {
			t.Errorf("param %s = %q, want %q (plain values: the ARN is built later, as the prompts do)", k, opts.Params[k], v)
		}
	}
}

func TestParseCreateRoleFromTemplateArgs_OptionalOnesMayBeLeftOut(t *testing.T) {
	opts, err := ParseCreateRoleFromTemplateArgs([]string{"--backup-bucket", "b", "rdm-restore-source-read-only", "r"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Params["second_bucket_arn"] != "" || opts.Params["key_prefix"] != "" || opts.Format != ui.FormatText {
		t.Errorf("got %+v", opts)
	}
}

func TestParseCreateRoleFromTemplateArgs_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":               {},
		"template only":              {"--backup-bucket", "b", "rdm-restore-source-read-only"},
		"unknown template":           {"--bucket", "b", "no-such-template", "r"},
		"missing required option":    {"rdm-restore-source-read-only", "r"},
		"blank required option":      {"--backup-bucket", "  ", "rdm-restore-source-read-only", "r"},
		"option of another template": {"--backup-bucket", "b", "--distribution", "E1", "rdm-restore-source-read-only", "r"},
		"unknown option":             {"--nope", "x", "--backup-bucket", "b", "rdm-restore-source-read-only", "r"},
		"stray extra word":           {"--backup-bucket", "b", "rdm-restore-source-read-only", "r", "extra"},
		"both formats":               {"-text", "-json", "--backup-bucket", "b", "rdm-restore-source-read-only", "r"},
		"blank role name":            {"--backup-bucket", "b", "rdm-restore-source-read-only", " "},
	} {
		_, err := ParseCreateRoleFromTemplateArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: want *UsageError, got %v", name, err)
			continue
		}
		if !strings.Contains(ue.Msg, "usage: clasm iam create-role-from-template") {
			t.Errorf("%s: the error must carry the usage line, got %q", name, ue.Msg)
		}
	}
}

func TestParseCreateRoleFromTemplateArgs_UnknownTemplateListsTheSlugs(t *testing.T) {
	_, err := ParseCreateRoleFromTemplateArgs([]string{"no-such-template", "r"})
	if err == nil || !strings.Contains(err.Error(), "rdm-restore-source-read-only") {
		t.Errorf("the error should list the template slugs, got %v", err)
	}
}

func TestParseCreateRoleFromTemplateArgs_HelpIsNotAnError(t *testing.T) {
	_, err := ParseCreateRoleFromTemplateArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) {
		t.Fatalf("want *HelpRequested, got %v", err)
	}
	for _, want := range []string{"rdm-restore-source-read-only", "--backup-bucket", "--key-prefix", "--distribution"} {
		if !strings.Contains(h.Usage, want) {
			t.Errorf("help should list %q:\n%s", want, h.Usage)
		}
	}
}

func restoreSourceOptions(t *testing.T, format string) CreateRoleFromTemplateOptions {
	t.Helper()
	args := []string{"--backup-bucket", "sql-backups.library.caltech.edu", "--second-bucket", "opensearch-backups.library.caltech.edu",
		"--key-prefix", "caltechauthors-v13"}
	if format != "" {
		args = append(args, format)
	}
	opts, err := ParseCreateRoleFromTemplateArgs(append(args, "rdm-restore-source-read-only", "ec2-test-restore-role"))
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestRunCreateRoleFromTemplateCLI_CreatesTheSameThingTheMenuDoes(t *testing.T) {
	origin := config.OriginTagConfig{Key: "Origin", DLDValue: "dld"}

	term, menuInput, buf := newPipeEditor("6\nec2-test-restore-role\nsql-backups.library.caltech.edu\nopensearch-backups.library.caltech.edu\ncaltechauthors-v13\ny\n")
	viaMenu := &fakeIAMClient{}
	if err := createIAMRoleFromTemplate(context.Background(), term, viaMenu, origin, "123456789012", "us-west-2", menuInput, buf); err != nil {
		t.Fatalf("menu path: %v\n%s", err, buf.String())
	}

	viaCLI := &fakeIAMClient{}
	var out bytes.Buffer
	if err := RunCreateRoleFromTemplateCLI(context.Background(), &out, viaCLI, origin, "123456789012", "us-west-2", restoreSourceOptions(t, "")); err != nil {
		t.Fatalf("CLI path: %v", err)
	}

	if aws.ToString(viaCLI.lastCreateRoleInput.RoleName) != "ec2-test-restore-role" ||
		aws.ToString(viaCLI.lastCreateRoleInput.AssumeRolePolicyDocument) != aws.ToString(viaMenu.lastCreateRoleInput.AssumeRolePolicyDocument) {
		t.Errorf("role differs: %+v vs %+v", viaCLI.lastCreateRoleInput, viaMenu.lastCreateRoleInput)
	}
	if aws.ToString(viaCLI.lastCreatePolicyInput.PolicyDocument) != aws.ToString(viaMenu.lastCreatePolicyInput.PolicyDocument) ||
		aws.ToString(viaCLI.lastCreatePolicyInput.PolicyName) != "ec2-test-restore-role-policy" {
		t.Errorf("policy differs:\n%s\nvs\n%s", aws.ToString(viaCLI.lastCreatePolicyInput.PolicyDocument), aws.ToString(viaMenu.lastCreatePolicyInput.PolicyDocument))
	}
	if len(viaCLI.lastAttachRolePolicyInputs) != 2 || len(viaMenu.lastAttachRolePolicyInputs) != 2 {
		t.Errorf("want SSM + custom policy attached on both paths, got %d and %d", len(viaCLI.lastAttachRolePolicyInputs), len(viaMenu.lastAttachRolePolicyInputs))
	}
	if len(viaCLI.lastCreateRoleInput.Tags) != 1 || aws.ToString(viaCLI.lastCreateRoleInput.Tags[0].Value) != "dld" {
		t.Errorf("the DLD Origin tag must be applied, got %+v", viaCLI.lastCreateRoleInput.Tags)
	}
	if !strings.Contains(out.String(), "Created role ec2-test-restore-role") {
		t.Errorf("text output = %q", out.String())
	}
}

func TestRunCreateRoleFromTemplateCLI_JSONOutput(t *testing.T) {
	var out bytes.Buffer
	err := RunCreateRoleFromTemplateCLI(context.Background(), &out, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", restoreSourceOptions(t, "-json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"role_name": "ec2-test-restore-role"`, `"template": "rdm-restore-source-read-only"`, `"policy_name": "ec2-test-restore-role-policy"`, `"role_arn"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON missing %q:\n%s", want, out.String())
		}
	}
}

// An AWS failure is an ordinary error (exit 1), not a usage error, and nothing
// after the failed step runs.
func TestRunCreateRoleFromTemplateCLI_RoleCreateFailureStopsEverythingAfterIt(t *testing.T) {
	fake := &fakeIAMClient{createRoleErr: errors.New("EntityAlreadyExists")}
	err := RunCreateRoleFromTemplateCLI(context.Background(), &bytes.Buffer{}, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", restoreSourceOptions(t, ""))
	if err == nil || !strings.Contains(err.Error(), "EntityAlreadyExists") {
		t.Fatalf("want the AWS error, got %v", err)
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		t.Errorf("an AWS failure must not be a usage error")
	}
	if fake.lastCreatePolicyInput != nil || len(fake.lastAttachRolePolicyInputs) != 0 {
		t.Errorf("nothing may follow a failed CreateRole: policy=%v attaches=%d", fake.lastCreatePolicyInput, len(fake.lastAttachRolePolicyInputs))
	}
}

// A template with only optional parameters left blank still builds its policy.
func TestRunCreateRoleFromTemplateCLI_OtherTemplatesWork(t *testing.T) {
	opts, err := ParseCreateRoleFromTemplateArgs([]string{"--log-group", "/bridge/app", "bridge-service", "ec2-bridge"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIAMClient{}
	if err := RunCreateRoleFromTemplateCLI(context.Background(), &bytes.Buffer{}, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", opts); err != nil {
		t.Fatal(err)
	}
	if doc := aws.ToString(fake.lastCreatePolicyInput.PolicyDocument); !strings.Contains(doc, "arn:aws:logs:us-west-2:123456789012:log-group:/bridge/app") {
		t.Errorf("the log group ARN must be built from the account and region:\n%s", doc)
	}
}

// A template with no managed policy (Static Website) must report an empty list,
// not JSON null, so a script can iterate it.
func TestRunCreateRoleFromTemplateCLI_JSONManagedPoliciesIsAnEmptyListNotNull(t *testing.T) {
	opts, err := ParseCreateRoleFromTemplateArgs([]string{"-json", "--bucket", "my-bucket", "static-website-s3-cloudfront", "ec2-site"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunCreateRoleFromTemplateCLI(context.Background(), &out, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"managed_policies": []`) {
		t.Errorf("want an empty list:\n%s", out.String())
	}
}

func TestRunCreateRoleFromTemplateCLI_TextNamesTheCustomPolicy(t *testing.T) {
	var out bytes.Buffer
	if err := RunCreateRoleFromTemplateCLI(context.Background(), &out, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", restoreSourceOptions(t, "")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Created and attached policy ec2-test-restore-role-policy") {
		t.Errorf("text output = %q", out.String())
	}
}

// A failed CreatePolicy is reported and nothing is attached after it; the role
// that was already created stays (the menu flow behaves the same).
func TestRunCreateRoleFromTemplateCLI_PolicyCreateFailureIsReportedAndNothingFollows(t *testing.T) {
	fake := &fakeIAMClient{createPolicyErr: errors.New("LimitExceeded")}
	err := RunCreateRoleFromTemplateCLI(context.Background(), &bytes.Buffer{}, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", restoreSourceOptions(t, ""))
	if err == nil || !strings.Contains(err.Error(), "creating policy") || !strings.Contains(err.Error(), "LimitExceeded") {
		t.Fatalf("want a 'creating policy' error, got %v", err)
	}
	if len(fake.lastAttachRolePolicyInputs) != 1 {
		t.Errorf("only the managed SSM policy may have been attached, got %d attaches", len(fake.lastAttachRolePolicyInputs))
	}
}
