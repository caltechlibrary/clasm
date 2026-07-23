package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/config"
)

func TestTrustPolicyDocument_EC2(t *testing.T) {
	doc := trustPolicyDocument(TrustPrincipalEC2)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("trust policy document isn't valid JSON: %v\n%s", err, doc)
	}
	if !strings.Contains(doc, "ec2.amazonaws.com") {
		t.Errorf("expected the EC2 service principal, got:\n%s", doc)
	}
	if !strings.Contains(doc, "sts:AssumeRole") {
		t.Errorf("expected sts:AssumeRole, got:\n%s", doc)
	}
}

func TestIAMRoleTemplates_FiveEntries(t *testing.T) {
	if len(iamRoleTemplates) != 5 {
		t.Fatalf("len(iamRoleTemplates) = %d, want 5", len(iamRoleTemplates))
	}
	wantThin := map[string]bool{
		"Static Website (S3 + CloudFront)": false,
		"RDM Repository Instance":          false,
		"Bridge Service":                   true,
		"Patron-Facing Service":            true,
		"Data Processing":                  true,
	}
	for _, tmpl := range iamRoleTemplates {
		if tmpl.BuildPolicy == nil {
			t.Errorf("template %q has a nil BuildPolicy", tmpl.Label)
		}
		want, ok := wantThin[tmpl.Label]
		if !ok {
			t.Errorf("unexpected template label %q", tmpl.Label)
			continue
		}
		if tmpl.Thin != want {
			t.Errorf("template %q: Thin = %v, want %v", tmpl.Label, tmpl.Thin, want)
		}
	}
}

func TestStaticWebsiteStatements_ReadOnlyByDefault(t *testing.T) {
	stmts := staticWebsiteStatements(map[string]string{"bucket_arn": "arn:aws:s3:::my-site"})
	doc, _ := marshalPolicyDocument(stmts)
	if !strings.Contains(doc, "s3:GetObject") || !strings.Contains(doc, "s3:ListBucket") {
		t.Errorf("expected read-only S3 actions, got:\n%s", doc)
	}
	if strings.Contains(doc, "s3:PutObject") || strings.Contains(doc, "cloudfront:CreateInvalidation") {
		t.Errorf("did not expect publish permissions without a distribution_arn, got:\n%s", doc)
	}
}

func TestStaticWebsiteStatements_PublishModeWhenDistributionGiven(t *testing.T) {
	stmts := staticWebsiteStatements(map[string]string{
		"bucket_arn":       "arn:aws:s3:::my-site",
		"distribution_arn": "arn:aws:cloudfront::123456789012:distribution/E123",
	})
	doc, _ := marshalPolicyDocument(stmts)
	for _, want := range []string{"s3:PutObject", "s3:DeleteObject", "cloudfront:CreateInvalidation"} {
		if !strings.Contains(doc, want) {
			t.Errorf("expected publish permission %q, got:\n%s", want, doc)
		}
	}
}

func TestRDMRepositoryStatements(t *testing.T) {
	stmts := rdmRepositoryStatements(map[string]string{"backup_bucket_arn": "arn:aws:s3:::rdm-backups"})
	doc, _ := marshalPolicyDocument(stmts)
	for _, want := range []string{"s3:GetObject", "s3:PutObject", "s3:ListBucket", "rdm-backups"} {
		if !strings.Contains(doc, want) {
			t.Errorf("output missing %q:\n%s", want, doc)
		}
	}
}

func TestBridgeServiceStatements(t *testing.T) {
	stmts := bridgeServiceStatements(map[string]string{"log_group_arn": "arn:aws:logs:us-west-2:123456789012:log-group:/bridge/app"})
	doc, _ := marshalPolicyDocument(stmts)
	for _, want := range []string{"logs:CreateLogGroup", "logs:PutLogEvents", "/bridge/app"} {
		if !strings.Contains(doc, want) {
			t.Errorf("output missing %q:\n%s", want, doc)
		}
	}
}

func TestPatronFacingStatements_OptionalParamsOmittedWhenBlank(t *testing.T) {
	stmts := patronFacingStatements(map[string]string{"log_group_arn": "arn:aws:logs:us-west-2:123456789012:log-group:/patron/app"})
	doc, _ := marshalPolicyDocument(stmts)
	if strings.Contains(doc, "secretsmanager:GetSecretValue") || strings.Contains(doc, "s3:GetObject") {
		t.Errorf("did not expect secret/bucket permissions when their params are blank, got:\n%s", doc)
	}
}

func TestPatronFacingStatements_OptionalParamsIncludedWhenProvided(t *testing.T) {
	stmts := patronFacingStatements(map[string]string{
		"log_group_arn": "arn:aws:logs:us-west-2:123456789012:log-group:/patron/app",
		"secret_arn":    "arn:aws:secretsmanager:us-west-2:123456789012:secret:patron-api-key",
		"bucket_arn":    "arn:aws:s3:::patron-content",
	})
	doc, _ := marshalPolicyDocument(stmts)
	for _, want := range []string{"secretsmanager:GetSecretValue", "patron-api-key", "s3:GetObject", "patron-content"} {
		if !strings.Contains(doc, want) {
			t.Errorf("output missing %q:\n%s", want, doc)
		}
	}
}

func TestDataProcessingStatements(t *testing.T) {
	stmts := dataProcessingStatements(map[string]string{
		"log_group_arn":   "arn:aws:logs:us-west-2:123456789012:log-group:/data/app",
		"data_bucket_arn": "arn:aws:s3:::curation-data",
	})
	doc, _ := marshalPolicyDocument(stmts)
	for _, want := range []string{"logs:CreateLogGroup", "s3:GetObject", "s3:PutObject", "curation-data"} {
		if !strings.Contains(doc, want) {
			t.Errorf("output missing %q:\n%s", want, doc)
		}
	}
}

func TestS3BucketArn(t *testing.T) {
	if got := s3BucketArn("", "", "my-site"); got != "arn:aws:s3:::my-site" {
		t.Errorf("got %q, want arn:aws:s3:::my-site", got)
	}
}

func TestCloudFrontDistributionArn(t *testing.T) {
	got := cloudfrontDistributionArn("123456789012", "", "E123")
	want := "arn:aws:cloudfront::123456789012:distribution/E123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLogGroupArn(t *testing.T) {
	got := logGroupArn("123456789012", "us-west-2", "/bridge/app")
	want := "arn:aws:logs:us-west-2:123456789012:log-group:/bridge/app"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSecretArnPattern(t *testing.T) {
	// Secrets Manager appends a random 6-character suffix to every
	// secret's real ARN, which the operator can't know or type in
	// advance -- a trailing wildcard is the standard, idiomatic way to
	// scope a policy to a secret by name without hardcoding that
	// suffix.
	got := secretArnPattern("123456789012", "us-west-2", "patron-api-key")
	want := "arn:aws:secretsmanager:us-west-2:123456789012:secret:patron-api-key-*"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreateIAMRoleFromTemplate_StaticWebsiteReadOnly(t *testing.T) {
	term, menuInput, buf := newPipeEditor(
		"1\n" + // template: Static Website (S3 + CloudFront)
			"my-static-site-role\n" + // role name
			"my-site\n" + // bucket name (not an ARN)
			"\n" + // distribution ID (skip -- read-only)
			"y\n", // confirm
	)
	fake := &fakeIAMClient{}

	err := createIAMRoleFromTemplate(context.Background(), term, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fake.lastCreateRoleInput == nil {
		t.Fatal("expected CreateRole to be called")
	}
	if aws.ToString(fake.lastCreateRoleInput.RoleName) != "my-static-site-role" {
		t.Errorf("RoleName = %q, want my-static-site-role", aws.ToString(fake.lastCreateRoleInput.RoleName))
	}
	if !strings.Contains(aws.ToString(fake.lastCreateRoleInput.AssumeRolePolicyDocument), "ec2.amazonaws.com") {
		t.Errorf("expected the EC2 trust policy, got: %s", aws.ToString(fake.lastCreateRoleInput.AssumeRolePolicyDocument))
	}
	if len(fake.lastCreateRoleInput.Tags) != 0 {
		t.Errorf("expected no auto-tag when DLDValue is unset, got: %+v", fake.lastCreateRoleInput.Tags)
	}

	if fake.lastCreatePolicyInput == nil {
		t.Fatal("expected CreatePolicy to be called")
	}
	if !strings.Contains(aws.ToString(fake.lastCreatePolicyInput.PolicyDocument), "s3:GetObject") {
		t.Errorf("expected read-only S3 policy content, got: %s", aws.ToString(fake.lastCreatePolicyInput.PolicyDocument))
	}
	if !strings.Contains(aws.ToString(fake.lastCreatePolicyInput.PolicyDocument), "arn:aws:s3:::my-site") {
		t.Errorf("expected the bare bucket name to be built into a full ARN, got: %s", aws.ToString(fake.lastCreatePolicyInput.PolicyDocument))
	}
	if strings.Contains(aws.ToString(fake.lastCreatePolicyInput.PolicyDocument), "cloudfront:CreateInvalidation") {
		t.Errorf("did not expect publish permissions (distribution ID was skipped), got: %s", aws.ToString(fake.lastCreatePolicyInput.PolicyDocument))
	}

	if len(fake.lastAttachRolePolicyInputs) != 1 {
		t.Fatalf("expected exactly one AttachRolePolicy call (the custom policy), got %d", len(fake.lastAttachRolePolicyInputs))
	}
	if !strings.Contains(buf.String(), "my-static-site-role") {
		t.Errorf("expected a success message naming the new role, got:\n%s", buf.String())
	}
}

func TestCreateIAMRoleFromTemplate_AutoTagsWhenOriginConfigured(t *testing.T) {
	term, menuInput, buf := newPipeEditor(
		"1\n" +
			"my-static-site-role\n" +
			"my-site\n" +
			"\n" +
			"y\n",
	)
	fake := &fakeIAMClient{}

	err := createIAMRoleFromTemplate(context.Background(), term, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, "123456789012", "us-west-2", menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastCreateRoleInput.Tags) != 1 || aws.ToString(fake.lastCreateRoleInput.Tags[0].Key) != "Origin" || aws.ToString(fake.lastCreateRoleInput.Tags[0].Value) != "DLD" {
		t.Errorf("expected an auto-applied Origin=DLD tag, got: %+v", fake.lastCreateRoleInput.Tags)
	}
	if len(fake.lastCreatePolicyInput.Tags) != 1 || aws.ToString(fake.lastCreatePolicyInput.Tags[0].Value) != "DLD" {
		t.Errorf("expected the custom policy to be auto-tagged too, got: %+v", fake.lastCreatePolicyInput.Tags)
	}
}

func TestCreateIAMRoleFromTemplate_AttachesManagedPolicyForRDMTemplate(t *testing.T) {
	term, menuInput, buf := newPipeEditor(
		"2\n" + // template: RDM Repository Instance
			"my-rdm-role\n" +
			"rdm-backups\n" +
			"y\n",
	)
	fake := &fakeIAMClient{}

	err := createIAMRoleFromTemplate(context.Background(), term, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastAttachRolePolicyInputs) != 2 {
		t.Fatalf("expected two AttachRolePolicy calls (SSM managed + custom), got %d", len(fake.lastAttachRolePolicyInputs))
	}
	foundSSM := false
	for _, in := range fake.lastAttachRolePolicyInputs {
		if aws.ToString(in.PolicyArn) == ssmManagedInstanceCorePolicyArn {
			foundSSM = true
		}
	}
	if !foundSSM {
		t.Errorf("expected AmazonSSMManagedInstanceCore to be attached, got: %+v", fake.lastAttachRolePolicyInputs)
	}
}

func TestCreateIAMRoleFromTemplate_DeclinedConfirmationSkipsCreation(t *testing.T) {
	term, menuInput, buf := newPipeEditor(
		"1\n" +
			"my-static-site-role\n" +
			"my-site\n" +
			"\n" +
			"n\n", // decline
	)
	fake := &fakeIAMClient{}

	err := createIAMRoleFromTemplate(context.Background(), term, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateRoleInput != nil {
		t.Error("did not expect CreateRole to be called after declining confirmation")
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("expected a cancelled message, got:\n%s", buf.String())
	}
}

func TestCreateIAMRoleFromTemplate_PropagatesCreateRoleError(t *testing.T) {
	term, menuInput, buf := newPipeEditor(
		"1\n" +
			"my-static-site-role\n" +
			"my-site\n" +
			"\n" +
			"y\n",
	)
	fake := &fakeIAMClient{createRoleErr: errors.New("boom")}

	err := createIAMRoleFromTemplate(context.Background(), term, fake, config.OriginTagConfig{Key: "Origin"}, "123456789012", "us-west-2", menuInput, buf)
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
	if len(fake.lastAttachRolePolicyInputs) != 0 {
		t.Error("did not expect any AttachRolePolicy calls after CreateRole fails")
	}
}

func TestMarshalPolicyDocument_ValidJSON(t *testing.T) {
	stmts := []policyStatement{
		{Effect: "Allow", Action: []string{"s3:GetObject"}, Resource: []string{"arn:aws:s3:::my-bucket/*"}},
	}
	doc, err := marshalPolicyDocument(stmts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed policyDocument
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("marshaled document isn't valid JSON: %v\n%s", err, doc)
	}
	if parsed.Version != "2012-10-17" {
		t.Errorf("Version = %q, want 2012-10-17", parsed.Version)
	}
	if len(parsed.Statement) != 1 || parsed.Statement[0].Effect != "Allow" {
		t.Errorf("Statement = %+v, want one Allow statement", parsed.Statement)
	}
}
