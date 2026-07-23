package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// TrustPrincipal names which AWS service can assume a role created via
// the curated creation templates (DESIGN.md, "IAM Profile & Role
// Management Domain"; PLAN.md Phase 20.39). Modeled as its own type from
// the start, even though only EC2 is wired up in this phase, so
// Lambda/ECS-task principals can be added later without reshaping the
// creation flow -- this team isn't making heavy use of either today, but
// some of the use cases below are plausible future serverless
// candidates.
type TrustPrincipal string

// TrustPrincipalEC2 is the only trust principal offered by this phase.
const TrustPrincipalEC2 TrustPrincipal = "ec2.amazonaws.com"

// trustPolicyDocument builds the AssumeRolePolicyDocument JSON for
// principal -- plain, un-encoded JSON, since CreateRole's
// AssumeRolePolicyDocument accepts a plain policy document on input
// (only the Get*/List* read paths return policy documents URL-encoded,
// confirmed live in Phase 20.38 -- see decodePolicyDocument).
func trustPolicyDocument(principal TrustPrincipal) string {
	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"%s"},"Action":"sts:AssumeRole"}]}`, principal)
}

// policyStatement is one statement in a policyDocument -- deliberately
// minimal (Effect/Action/Resource only), matching the curated
// templates' own scope: no Condition blocks, no NotAction/NotResource,
// nothing beyond what the five templates below actually need.
type policyStatement struct {
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource []string `json:"Resource"`
}

// policyDocument is a minimal IAM policy document.
type policyDocument struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

// marshalPolicyDocument renders statements as a plain (not URL-encoded)
// JSON policy document, for CreatePolicy's PolicyDocument input.
func marshalPolicyDocument(statements []policyStatement) (string, error) {
	doc := policyDocument{Version: "2012-10-17", Statement: statements}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// IAMRoleTemplateParam is one ARN (or similar identifier) a template
// needs from the operator at creation time -- DESIGN.md's "parametrized
// statement set": the operator supplies specific ARNs rather than clasm
// guessing account specifics.
type IAMRoleTemplateParam struct {
	Key      string // internal key BuildPolicy reads from its params map
	Prompt   string // shown to the operator
	Required bool   // if false, an empty answer means "skip this capability"
}

// IAMRoleTemplate is one of the five curated per-use-case templates
// (DESIGN.md, "IAM Profile & Role Management Domain"; PLAN.md Phase
// 20.39). Label is the clean base name -- the "starting point, review
// before use" annotation for Thin templates is applied only at display
// time (pickIAMRoleTemplate), not baked into this data.
type IAMRoleTemplate struct {
	Label             string
	Thin              bool
	ManagedPolicyARNs []string
	Params            []IAMRoleTemplateParam
	BuildPolicy       func(params map[string]string) []policyStatement
}

// cloudWatchLogsStatements is shared by the three Thin templates below
// (Bridge Service, Patron-Facing Service, Data Processing), all of
// which need baseline CloudWatch Logs write access scoped to one log
// group ARN the operator supplies -- there's no way to know a future
// service's log group naming convention in advance, so this is always a
// required parameter for templates that include it, not guessed.
func cloudWatchLogsStatements(logGroupArn string) []policyStatement {
	return []policyStatement{
		{
			Effect:   "Allow",
			Action:   []string{"logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"},
			Resource: []string{logGroupArn, logGroupArn + ":*"},
		},
	}
}

// staticWebsiteStatements: read-only S3 access on bucket_arn by
// default; if distribution_arn is also supplied, this is instead a
// publish role -- write access to the bucket plus CloudFront
// invalidation (DESIGN.md's draft shape). Whether a distribution ARN is
// supplied is what decides read-only vs. publish, rather than a
// separate yes/no prompt -- one fewer question to answer.
func staticWebsiteStatements(p map[string]string) []policyStatement {
	bucket := p["bucket_arn"]
	stmts := []policyStatement{
		{Effect: "Allow", Action: []string{"s3:ListBucket"}, Resource: []string{bucket}},
		{Effect: "Allow", Action: []string{"s3:GetObject"}, Resource: []string{bucket + "/*"}},
	}
	if dist := p["distribution_arn"]; dist != "" {
		stmts = append(stmts,
			policyStatement{Effect: "Allow", Action: []string{"s3:PutObject", "s3:DeleteObject"}, Resource: []string{bucket + "/*"}},
			policyStatement{Effect: "Allow", Action: []string{"cloudfront:CreateInvalidation"}, Resource: []string{dist}},
		)
	}
	return stmts
}

// rdmRepositoryStatements: scoped S3 read/write on one backup bucket --
// AmazonSSMManagedInstanceCore is attached separately, via the
// template's ManagedPolicyARNs, matching Phase 20.33's own launch-time
// enforcement rather than duplicating it in a custom policy.
func rdmRepositoryStatements(p map[string]string) []policyStatement {
	bucket := p["backup_bucket_arn"]
	return []policyStatement{
		{Effect: "Allow", Action: []string{"s3:ListBucket"}, Resource: []string{bucket}},
		{Effect: "Allow", Action: []string{"s3:GetObject", "s3:PutObject"}, Resource: []string{bucket + "/*"}},
	}
}

// bridgeServiceStatements: CloudWatch Logs only -- the thinnest
// template, flagged Thin in DESIGN.md as "too varied across actual
// services to template further."
func bridgeServiceStatements(p map[string]string) []policyStatement {
	return cloudWatchLogsStatements(p["log_group_arn"])
}

// patronFacingStatements: CloudWatch Logs (required) plus optional
// Secrets Manager read and S3 read, each included only if its param is
// non-blank.
func patronFacingStatements(p map[string]string) []policyStatement {
	stmts := cloudWatchLogsStatements(p["log_group_arn"])
	if secret := p["secret_arn"]; secret != "" {
		stmts = append(stmts, policyStatement{Effect: "Allow", Action: []string{"secretsmanager:GetSecretValue"}, Resource: []string{secret}})
	}
	if bucket := p["bucket_arn"]; bucket != "" {
		stmts = append(stmts, policyStatement{Effect: "Allow", Action: []string{"s3:GetObject"}, Resource: []string{bucket + "/*"}})
	}
	return stmts
}

// dataProcessingStatements: CloudWatch Logs plus scoped S3 read/write
// on one data bucket.
func dataProcessingStatements(p map[string]string) []policyStatement {
	stmts := cloudWatchLogsStatements(p["log_group_arn"])
	bucket := p["data_bucket_arn"]
	stmts = append(stmts,
		policyStatement{Effect: "Allow", Action: []string{"s3:ListBucket"}, Resource: []string{bucket}},
		policyStatement{Effect: "Allow", Action: []string{"s3:GetObject", "s3:PutObject"}, Resource: []string{bucket + "/*"}},
	)
	return stmts
}

// iamRoleTemplates is DESIGN.md's five curated templates, in the same
// order as its draft table. All flagged as needing review before
// implementation -- Static Website and RDM Repository Instance are more
// fully scoped; Bridge Service, Patron-Facing Service, and Data
// Processing are Thin starting points, drafted from the motivating use
// cases rather than existing policy documents (none were available).
var iamRoleTemplates = []IAMRoleTemplate{
	{
		Label: "Static Website (S3 + CloudFront)",
		Params: []IAMRoleTemplateParam{
			{Key: "bucket_arn", Prompt: "S3 bucket ARN this role serves content from", Required: true},
			{Key: "distribution_arn", Prompt: "CloudFront distribution ARN (leave blank for read-only serving; fill in to also grant publish + cache invalidation)", Required: false},
		},
		BuildPolicy: staticWebsiteStatements,
	},
	{
		Label:             "RDM Repository Instance",
		ManagedPolicyARNs: []string{ssmManagedInstanceCorePolicyArn},
		Params: []IAMRoleTemplateParam{
			{Key: "backup_bucket_arn", Prompt: "S3 backup bucket ARN this role reads/writes", Required: true},
		},
		BuildPolicy: rdmRepositoryStatements,
	},
	{
		Label:             "Bridge Service",
		Thin:              true,
		ManagedPolicyARNs: []string{ssmManagedInstanceCorePolicyArn},
		Params: []IAMRoleTemplateParam{
			{Key: "log_group_arn", Prompt: "CloudWatch log group ARN this role writes to", Required: true},
		},
		BuildPolicy: bridgeServiceStatements,
	},
	{
		Label:             "Patron-Facing Service",
		Thin:              true,
		ManagedPolicyARNs: []string{ssmManagedInstanceCorePolicyArn},
		Params: []IAMRoleTemplateParam{
			{Key: "log_group_arn", Prompt: "CloudWatch log group ARN this role writes to", Required: true},
			{Key: "secret_arn", Prompt: "Secrets Manager secret ARN this role can read (optional, leave blank to skip)", Required: false},
			{Key: "bucket_arn", Prompt: "S3 bucket ARN this role can read from (optional, leave blank to skip)", Required: false},
		},
		BuildPolicy: patronFacingStatements,
	},
	{
		Label:             "Data Processing",
		Thin:              true,
		ManagedPolicyARNs: []string{ssmManagedInstanceCorePolicyArn},
		Params: []IAMRoleTemplateParam{
			{Key: "log_group_arn", Prompt: "CloudWatch log group ARN this role writes to", Required: true},
			{Key: "data_bucket_arn", Prompt: "S3 data bucket ARN this role reads/writes", Required: true},
		},
		BuildPolicy: dataProcessingStatements,
	},
}

// iamRoleTemplateLabel appends a "starting point, review before use"
// annotation to Thin templates' display label -- the underlying Label
// field stays the clean base name (IAMRoleTemplate's own doc comment),
// so this formatting lives only here, at display time.
func iamRoleTemplateLabel(tmpl IAMRoleTemplate) string {
	if tmpl.Thin {
		return tmpl.Label + " (starting point, review before use)"
	}
	return tmpl.Label
}

// pickIAMRoleTemplate runs the template picker as a Menu-tier
// huh.Select (unlike pickIAMRole/pickIAMInstanceProfile's Picker-tier
// tui.RunPicker) -- there's no existing-resource list to browse here,
// just a small, fixed choice of five templates, so the same
// pipe-testable shape as pickDomainItem/pickIAMItem applies, and the
// whole guided creation flow below stays fully pipe-testable as a
// result (no untestable Picker-tier step at all, unlike this domain's
// other actions).
func pickIAMRoleTemplate(w io.Writer, input io.Reader, output io.Writer) (IAMRoleTemplate, error) {
	opts := make([]huh.Option[int], len(iamRoleTemplates))
	for i, tmpl := range iamRoleTemplates {
		opts[i] = huh.NewOption(iamRoleTemplateLabel(tmpl), i)
	}

	var idx int
	field := huh.NewSelect[int]().
		Title("Create IAM role from template").
		Description("EC2 trust principal only. Each template needs specific resource ARNs, collected next.").
		Options(opts...).
		Value(&idx)

	if err := runMenuField(w, hintCancel, field, input, output); err != nil {
		return IAMRoleTemplate{}, err
	}
	return iamRoleTemplates[idx], nil
}

// CreateIAMRoleFromTemplate runs the IAM domain's "Create Role from
// Template" action (DESIGN.md, "IAM Profile & Role Management Domain";
// PLAN.md Phase 20.39): pick a template, collect its required/optional
// ARN parameters, confirm, then create the role (+policy, +attachments).
func CreateIAMRoleFromTemplate(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig) error {
	return createIAMRoleFromTemplate(ctx, w, client, originTag, nil, nil)
}

// createIAMRoleFromTemplate is CreateIAMRoleFromTemplate's testable
// core: menuInput/menuOutput are nil in production and supplied by
// tests to drive the whole flow through its accessible-mode pipe path
// -- every step here is a Menu-tier huh.Select, a ui.Prompt, or Confirm,
// none of them Picker-tier, so unlike this domain's other actions the
// entire flow (not just an early-return slice of it) is pipe-tested
// end to end.
func createIAMRoleFromTemplate(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, menuInput io.Reader, menuOutput io.Writer) error {
	tmpl, err := pickIAMRoleTemplate(w, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	roleName, err := ui.Prompt("Role name", ui.WithValidator(requireNonEmpty), ui.WithIO(menuInput, menuOutput))
	if err != nil {
		return cancelledIsNil(w, err)
	}

	params := make(map[string]string, len(tmpl.Params))
	for _, p := range tmpl.Params {
		opts := []ui.PromptOption{ui.WithIO(menuInput, menuOutput)}
		if p.Required {
			opts = append(opts, ui.WithValidator(requireNonEmpty))
		}
		val, err := ui.Prompt(p.Prompt, opts...)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		params[p.Key] = val
	}

	statements := tmpl.BuildPolicy(params)
	policyDoc, err := marshalPolicyDocument(statements)
	if err != nil {
		return err
	}

	ok, err := Confirm(fmt.Sprintf("Create role %q from template %q?", roleName, iamRoleTemplateLabel(tmpl)), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	var tags []iamtypes.Tag
	if originTag.DLDValue != "" {
		tags = []iamtypes.Tag{{Key: aws.String(originTag.Key), Value: aws.String(originTag.DLDValue)}}
	}

	roleOut, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(trustPolicyDocument(TrustPrincipalEC2)),
		Tags:                     tags,
	})
	if err != nil {
		return fmt.Errorf("creating role: %w", err)
	}

	for _, arn := range tmpl.ManagedPolicyARNs {
		if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: aws.String(roleName), PolicyArn: aws.String(arn)}); err != nil {
			return fmt.Errorf("attaching managed policy %s: %w", arn, err)
		}
	}

	if len(statements) > 0 {
		policyOut, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
			PolicyName:     aws.String(roleName + "-policy"),
			PolicyDocument: aws.String(policyDoc),
			Tags:           tags,
		})
		if err != nil {
			return fmt.Errorf("creating policy: %w", err)
		}
		if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: aws.String(roleName), PolicyArn: policyOut.Policy.Arn}); err != nil {
			return fmt.Errorf("attaching custom policy: %w", err)
		}
	}

	fmt.Fprintf(w, "Created role %s (%s)\n", roleName, aws.ToString(roleOut.Role.Arn))
	return nil
}
