package workflow

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Create Role from Template" (DR-0186). The menu flow picks
// a template, asks for a role name and then each template parameter; here the
// template and the role name are the two positional words and each parameter is
// a named option. It asks for no confirmation, like the other creating forms: a
// role costs nothing and can be removed with delete-role.

// CreateRoleFromTemplateCLISlug is the leaf's cliSlug (DR-0177's mechanical rule).
const CreateRoleFromTemplateCLISlug = "create-role-from-template"

// CreateRoleFromTemplateUsage is the form's one-line usage, repeated in every
// usage error.
const CreateRoleFromTemplateUsage = "usage: clasm iam " + CreateRoleFromTemplateCLISlug +
	" [-text|-json] [--<parameter> <value>...] <template> <role-name>"

// iamRoleTemplateSlug turns a template's label into its CLI word by the same
// mechanical rule as the leaf slugs: lower case, every run of characters that
// are not letters or digits becomes one hyphen, none at either end.
func iamRoleTemplateSlug(label string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		} else {
			hyphen = true
		}
	}
	return b.String()
}

// IAMRoleTemplateSlugs lists the template words the form accepts, in menu order.
func IAMRoleTemplateSlugs() []string {
	slugs := make([]string, len(iamRoleTemplates))
	for i, tmpl := range iamRoleTemplates {
		slugs[i] = iamRoleTemplateSlug(tmpl.Label)
	}
	return slugs
}

// iamRoleTemplateParamOption is the option name of a template parameter: its key
// without a trailing "_arn", underscores as hyphens (backup_bucket_arn becomes
// --backup-bucket).
func iamRoleTemplateParamOption(key string) string {
	return strings.ReplaceAll(strings.TrimSuffix(key, "_arn"), "_", "-")
}

// iamRoleTemplateParamOptions lists every parameter option across all templates,
// each once, in first-seen order. One flag set knows them all because Go's flag
// parsing needs the options before the template word is read; the template then
// accepts only its own.
func iamRoleTemplateParamOptions() []string {
	var names []string
	seen := map[string]bool{}
	for _, tmpl := range iamRoleTemplates {
		for _, p := range tmpl.Params {
			if n := iamRoleTemplateParamOption(p.Key); !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	return names
}

// CreateRoleFromTemplateOptions are the form's parsed arguments. Params holds the
// operator's plain values keyed by the template's parameter keys; the ARNs are
// built when the role is created, exactly as the prompts' answers are.
type CreateRoleFromTemplateOptions struct {
	Template IAMRoleTemplate
	RoleName string
	Params   map[string]string
	Format   ui.Format
}

// ParseCreateRoleFromTemplateArgs parses the form's options and its two words,
// the template and the role name. Every problem -- an unknown template, a missing
// required parameter, a parameter that belongs to another template, a stray
// word -- is a *UsageError (exit 2) raised before AWS is touched; --help is a
// *HelpRequested. Options come before the words.
func ParseCreateRoleFromTemplateArgs(args []string) (CreateRoleFromTemplateOptions, error) {
	const leaf = CreateRoleFromTemplateCLISlug
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	var asText, asJSON bool
	fs.BoolVar(&asText, "text", false, "")
	fs.BoolVar(&asJSON, "json", false, "")
	given := map[string]*string{}
	for _, name := range iamRoleTemplateParamOptions() {
		given[name] = fs.String(name, "", "")
	}
	setByUser := map[string]bool{}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return CreateRoleFromTemplateOptions{}, &HelpRequested{Usage: CreateRoleFromTemplateUsage + createRoleFromTemplateHelp()}
		}
		return CreateRoleFromTemplateOptions{}, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, CreateRoleFromTemplateUsage)}
	}
	fs.Visit(func(f *flag.Flag) { setByUser[f.Name] = true })
	usage := func(format string, a ...any) error {
		return &UsageError{Msg: fmt.Sprintf("%s: %s\n%s", leaf, fmt.Sprintf(format, a...), CreateRoleFromTemplateUsage)}
	}

	if asText && asJSON {
		return CreateRoleFromTemplateOptions{}, usage("give only one of -text, -json")
	}
	words := fs.Args()
	if len(words) != 2 {
		return CreateRoleFromTemplateOptions{}, usage("want a template and a role name, got %d arguments", len(words))
	}
	var tmpl IAMRoleTemplate
	found := false
	for i, slug := range IAMRoleTemplateSlugs() {
		if slug == words[0] {
			tmpl, found = iamRoleTemplates[i], true
		}
	}
	if !found {
		return CreateRoleFromTemplateOptions{}, usage("unknown template %q; the templates are %s", words[0], strings.Join(IAMRoleTemplateSlugs(), ", "))
	}
	roleName := strings.TrimSpace(words[1])
	if roleName == "" {
		return CreateRoleFromTemplateOptions{}, usage("the role name must not be blank")
	}

	own := map[string]bool{}
	params := make(map[string]string, len(tmpl.Params))
	for _, p := range tmpl.Params {
		name := iamRoleTemplateParamOption(p.Key)
		own[name] = true
		value := strings.TrimSpace(*given[name])
		if p.Required && value == "" {
			return CreateRoleFromTemplateOptions{}, usage("--%s is required by the %s template", name, words[0])
		}
		params[p.Key] = value
	}
	for name := range setByUser {
		if _, isParam := given[name]; isParam && !own[name] {
			return CreateRoleFromTemplateOptions{}, usage("--%s is not a parameter of the %s template", name, words[0])
		}
	}

	format := ui.FormatText
	if asJSON {
		format = ui.FormatJSON
	}
	return CreateRoleFromTemplateOptions{Template: tmpl, RoleName: roleName, Params: params, Format: format}, nil
}

// createRoleFromTemplateHelp lists each template with its parameters, built from
// the registry so it cannot go stale.
func createRoleFromTemplateHelp() string {
	var b strings.Builder
	b.WriteString("\n\nCreates an IAM role (EC2 trust principal) from a curated template, exactly as the\n" +
		"interactive \"Create Role from Template\" does, with each prompt an option. The role\n" +
		"and its policy are tagged as DLD-owned. Options come before the two words.\n\nTemplates:\n")
	for i, tmpl := range iamRoleTemplates {
		fmt.Fprintf(&b, "\n  %s    %s\n", IAMRoleTemplateSlugs()[i], iamRoleTemplateLabel(tmpl))
		for _, p := range tmpl.Params {
			req := "optional"
			if p.Required {
				req = "required"
			}
			fmt.Fprintf(&b, "      --%s <value>  (%s) %s\n", iamRoleTemplateParamOption(p.Key), req, p.Prompt)
		}
	}
	return b.String()
}

type createdRoleJSON struct {
	RoleName        string   `json:"role_name"`
	RoleARN         string   `json:"role_arn"`
	Template        string   `json:"template"`
	ManagedPolicies []string `json:"managed_policies"`
	PolicyName      string   `json:"policy_name"`
	PolicyARN       string   `json:"policy_arn"`
}

// RunCreateRoleFromTemplateCLI is `clasm iam create-role-from-template`: build
// each parameter's ARN from its plain value (as the prompts' answers are), then
// create the role through the same path as the menu flow. accountID and region
// are the ones the menu flow uses.
func RunCreateRoleFromTemplateCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, accountID, region string, opts CreateRoleFromTemplateOptions) error {
	params := make(map[string]string, len(opts.Template.Params))
	for _, p := range opts.Template.Params {
		val := opts.Params[p.Key]
		if val != "" && p.BuildARN != nil {
			val = p.BuildARN(accountID, region, val)
		}
		params[p.Key] = val
	}
	created, err := createRoleFromTemplateParams(ctx, client, originTag, opts.Template, opts.RoleName, params)
	if err != nil {
		return err
	}
	if opts.Format == ui.FormatText {
		fmt.Fprintf(w, "Created role %s (%s)\n", created.RoleName, created.RoleARN)
		if created.PolicyName != "" {
			fmt.Fprintf(w, "Created and attached policy %s\n", created.PolicyName)
		}
		return nil
	}
	return ui.WriteJSONValue(w, createdRoleJSON{
		created.RoleName, created.RoleARN, iamRoleTemplateSlug(opts.Template.Label),
		nonNilStrings(created.ManagedPolicies), created.PolicyName, created.PolicyARN,
	}, opts.Format)
}
