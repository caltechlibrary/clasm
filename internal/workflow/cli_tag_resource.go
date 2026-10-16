package workflow

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// tag-resource (DR-0192): add a tag to one resource of any of the eleven Tag Management
// kinds from a script, to backfill the Origin tag on the existing fleet. It adds a tag
// that is missing, treats the same value as done so a loop can be re-run, and will not
// silently change an existing different value: that needs --replace, and a refusal
// changes nothing at all, not even the tags that were fine. It removes nothing and has
// no confirmation: adding a tag is reversible in Tag Management.

// TagResourceCLISlug is the leaf's cliSlug. It is registered on the interactive "Manage
// tags" leaf, whose command-line counterpart it is, rather than derived from that label.
const TagResourceCLISlug = "tag-resource"

// TagResourceUsage is the form's usage, repeated in every usage error.
var TagResourceUsage = "usage: clasm tag-management " + TagResourceCLISlug +
	" [-text|-json] [--replace] <kind> <name-or-id> --tag key=value [--tag key=value]...\n  kind: " + strings.Join(TagKindSlugs(), ", ")

const tagResourceHelp = `

Adds each --tag to the named resource. Options may come before or after the kind and
the name; --tag may be repeated.

  --tag key=value   a tag to add (the first = splits key from value; the value may be empty)
  --replace         also change a tag that already has a different value

A tag that is missing is added. A tag that already has the same value is left as it is
and counts as done, so the command is safe to repeat. A tag that already has a different
value is refused (exit 1) and named, and nothing at all is changed, unless --replace is
given. Nothing is ever removed. Tags are compared against what AWS reports at the time.

<name-or-id> is a name or ID for instances, AMIs, launch templates, key pairs and S3
buckets; an exact name for an IAM role or instance profile; a name or ARN for an IAM
policy; an ID for an EBS volume, snapshot or network interface (found in whichever
region has it). The Environment tag must be production, development or test, which the
production warnings read. Keys beginning aws: are reserved by AWS.
`

// TagKV is one --tag.
type TagKV struct{ Key, Value string }

// TagResourceOptions are the form's parsed arguments.
type TagResourceOptions struct {
	Kind     string // the Tag Management kind label, "Instance"
	KindSlug string // the CLI kind word, "instance"
	Name     string
	Tags     []TagKV // in the order given
	Replace  bool
	Format   ui.Format
}

// ParseTagResourceArgs parses the form's arguments. Options and words may be mixed: Go's
// flag parsing stops at the first word, so it is restarted after each one. Every problem
// -- a malformed or reserved or repeated tag, an unknown kind, a bad Environment, the
// wrong number of words -- is a *UsageError (exit 2) raised before AWS is touched;
// --help is a *HelpRequested.
//
// @param args {[]string} the arguments after the leaf name
// @returns {TagResourceOptions, error} the options, or a usage error or help request
// @example
//
//	o, err := ParseTagResourceArgs([]string{"instance", "new-data", "--tag", "origin=dld"})
func ParseTagResourceArgs(args []string) (TagResourceOptions, error) {
	const leaf = TagResourceCLISlug
	usage := func(format string, a ...any) error {
		return &UsageError{Msg: fmt.Sprintf("%s: %s\n%s", leaf, fmt.Sprintf(format, a...), TagResourceUsage)}
	}
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	var asText, asJSON, replace bool
	var tags []TagKV
	seen := map[string]bool{}
	fs.BoolVar(&asText, "text", false, "")
	fs.BoolVar(&asJSON, "json", false, "")
	fs.BoolVar(&replace, "replace", false, "")
	fs.Func("tag", "", func(v string) error {
		key, value, ok := strings.Cut(v, "=")
		key = strings.TrimSpace(key)
		switch {
		case !ok:
			return fmt.Errorf("%q is not key=value", v)
		case key == "":
			return fmt.Errorf("%q has an empty key", v)
		case strings.HasPrefix(strings.ToLower(key), "aws:"):
			return fmt.Errorf("the key %q is reserved by AWS", key)
		case seen[key]:
			return fmt.Errorf("the key %q is given more than once", key)
		case key == "Environment" && validateEnvironment(value) != nil:
			return fmt.Errorf("Environment must be one of production, development, test, not %q", value)
		}
		seen[key] = true
		tags = append(tags, TagKV{key, value})
		return nil
	})
	var words []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return TagResourceOptions{}, &HelpRequested{Usage: TagResourceUsage + tagResourceHelp}
			}
			return TagResourceOptions{}, usage("%v", err)
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		words = append(words, rest[0])
		rest = rest[1:]
	}
	if asText && asJSON {
		return TagResourceOptions{}, usage("give only one of -text, -json")
	}
	if len(words) != 2 {
		return TagResourceOptions{}, usage("want a kind and a name, got %d words", len(words))
	}
	var kind string
	for label, slug := range tagKindSlugs {
		if slug == words[0] {
			kind = label
		}
	}
	if kind == "" {
		valid := TagKindSlugs()
		sort.Strings(valid)
		return TagResourceOptions{}, usage("unknown kind %q; the kinds are: %s", words[0], strings.Join(valid, ", "))
	}
	name := strings.TrimSpace(words[1])
	if name == "" {
		return TagResourceOptions{}, usage("the name must not be blank")
	}
	if len(tags) == 0 {
		return TagResourceOptions{}, usage("at least one --tag key=value is required")
	}
	format := ui.FormatText
	if asJSON {
		format = ui.FormatJSON
	}
	return TagResourceOptions{Kind: kind, KindSlug: words[0], Name: name, Tags: tags, Replace: replace, Format: format}, nil
}

// tagTarget is the one resource a tag-resource call acts on, resolved.
type tagTarget struct {
	id, name, label string
	tags            map[string]string // its live tags
	apply           tagApplyFunc
}

// isNotFound reports whether err says the resource does not exist, as AWS reports it
// (an error code containing NotFound or Malformed) or as this package's own lookups do.
func isNotFound(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		code := api.ErrorCode()
		return strings.Contains(code, "NotFound") || strings.Contains(code, "Malformed")
	}
	return strings.Contains(err.Error(), "not found")
}

// findInRegions finds a resource known only by ID in whichever region has it, trying the
// regions in name order. A not-found answer moves on; any other error stops.
func findInRegions(ctx context.Context, clients map[string]awsclient.EC2API, kindWord, id string, fetch func(ctx context.Context, client awsclient.EC2API) (map[string]string, error)) (awsclient.EC2API, map[string]string, error) {
	regions := make([]string, 0, len(clients))
	for r := range clients {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	for _, r := range regions {
		tags, err := fetch(ctx, clients[r])
		if err == nil {
			return clients[r], tags, nil
		}
		if !isNotFound(err) {
			return nil, nil, fmt.Errorf("%s: %w", r, err)
		}
	}
	return nil, nil, &UsageError{Msg: fmt.Sprintf("no %s found with ID %q in %s", kindWord, id, strings.Join(regions, ", "))}
}

// resolveTagTarget finds the resource a kind and a name or ID stand for and reads its live
// tags. A resource that does not exist, or a name that matches several, is a *UsageError.
func resolveTagTarget(ctx context.Context, src TagSources, kind, arg string) (tagTarget, error) {
	ec2Apply := func(client awsclient.EC2API) tagApplyFunc {
		return func(ctx context.Context, p TagChangeParams) error { return ApplyTagChange(ctx, client, p) }
	}
	ec2Target := func(id, name, label, region string, fetch func(ctx context.Context, c awsclient.EC2API, id string) (map[string]string, error)) (tagTarget, error) {
		client, err := resolveEC2(src.EC2Clients, region)
		if err != nil {
			return tagTarget{}, err
		}
		tags, err := fetch(ctx, client, id)
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{id, name, label, tags, ec2Apply(client)}, nil
	}
	switch kind {
	case "Instance":
		inst, err := resolveInstanceForCLI(arg, src.Instances)
		if err != nil {
			return tagTarget{}, err
		}
		return ec2Target(inst.InstanceID, inst.Name, instanceLabel(inst), inst.Region, fetchInstanceTags)
	case "AMI":
		img, err := resolveImageArg(arg, src.Images)
		if err != nil {
			return tagTarget{}, err
		}
		return ec2Target(img.ImageID, img.Name, imageLabel(img), img.Region, fetchImageTags)
	case "Launch Template":
		lt, err := resolveLaunchTemplateArg(arg, src.LaunchTemplates)
		if err != nil {
			return tagTarget{}, err
		}
		return ec2Target(lt.TemplateID, lt.Name, launchTemplateLabel(lt), lt.Region, fetchLaunchTemplateTags)
	case "Key Pair":
		kp, err := resolveOne("key pair", arg, src.KeyPairs, func(k inventory.KeyPair) string { return k.KeyName }, func(k inventory.KeyPair) string { return k.KeyPairID })
		if err != nil {
			return tagTarget{}, err
		}
		return ec2Target(kp.KeyPairID, kp.KeyName, keyPairLabel(kp), kp.Region, fetchKeyPairTags)
	case "S3 Bucket":
		b, err := resolveOne("S3 bucket", arg, src.Buckets, func(b inventory.Bucket) string { return b.Name }, func(b inventory.Bucket) string { return b.Name })
		if err != nil {
			return tagTarget{}, err
		}
		client, err := src.NewS3Client(ctx, b.Region)
		if err != nil {
			return tagTarget{}, err
		}
		tags, err := fetchBucketTags(ctx, client, b.Name)
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{b.Name, b.Name, bucketLabel(b), tags, func(ctx context.Context, p TagChangeParams) error { return applyBucketTagChange(ctx, client, p) }}, nil
	case "IAM Role":
		tags, err := fetchIAMRoleTags(ctx, src.IAMClient, arg)
		if err != nil {
			return tagTarget{}, iamLookupError("role", arg, err)
		}
		return tagTarget{arg, arg, arg, tags, func(ctx context.Context, p TagChangeParams) error {
			return applyIAMRoleTagChange(ctx, src.IAMClient, p)
		}}, nil
	case "IAM Instance Profile":
		tags, err := fetchIAMInstanceProfileTags(ctx, src.IAMClient, arg)
		if err != nil {
			return tagTarget{}, iamLookupError("instance profile", arg, err)
		}
		return tagTarget{arg, arg, arg, tags, func(ctx context.Context, p TagChangeParams) error {
			return applyIAMInstanceProfileTagChange(ctx, src.IAMClient, p)
		}}, nil
	case "IAM Policy":
		arn, name := arg, arg
		if !strings.HasPrefix(arg, "arn:") {
			policies, err := inventory.ListIAMPolicySummaries(ctx, src.IAMClient, src.OriginTag)
			if err != nil {
				return tagTarget{}, err
			}
			var matches []string
			for _, p := range policies {
				if p.Name == arg {
					matches = append(matches, p.ARN)
				}
			}
			switch len(matches) {
			case 0:
				return tagTarget{}, &UsageError{Msg: fmt.Sprintf("no customer-managed IAM policy found named %q", arg)}
			case 1:
				arn = matches[0]
			default:
				return tagTarget{}, &UsageError{Msg: fmt.Sprintf("IAM policy name %q is ambiguous: matches %s", arg, strings.Join(matches, ", "))}
			}
		}
		tags, err := fetchIAMPolicyTags(ctx, src.IAMClient, arn)
		if err != nil {
			return tagTarget{}, iamLookupError("policy", arg, err)
		}
		return tagTarget{arn, name, arn, tags, func(ctx context.Context, p TagChangeParams) error {
			return applyIAMPolicyTagChange(ctx, src.IAMClient, p)
		}}, nil
	case "EBS Volume", "EBS Snapshot", "Network Interface":
		fetch, word := fetchVolumeTags, "EBS volume"
		if kind == "EBS Snapshot" {
			fetch, word = fetchSnapshotTags, "EBS snapshot"
		} else if kind == "Network Interface" {
			fetch, word = fetchNetworkInterfaceTags, "network interface"
		}
		client, tags, err := findInRegions(ctx, src.EC2Clients, word, arg, func(ctx context.Context, c awsclient.EC2API) (map[string]string, error) { return fetch(ctx, c, arg) })
		if err != nil {
			return tagTarget{}, err
		}
		return tagTarget{arg, tags["Name"], arg, tags, ec2Apply(client)}, nil
	}
	return tagTarget{}, &UsageError{Msg: fmt.Sprintf("unknown kind %q", kind)}
}

type tagReplacedJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type tagResourceJSON struct {
	Kind      string                     `json:"kind"`
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Added     map[string]string          `json:"added"`
	Replaced  map[string]tagReplacedJSON `json:"replaced"`
	Unchanged map[string]string          `json:"unchanged"`
	Tags      map[string]string          `json:"tags"`
}

// RunTagResourceCLI is `clasm tag-management tag-resource`. It resolves the resource and
// reads its live tags first, decides what each --tag means (add, replace, already there,
// or refused), and only if nothing is refused sends the adds and replacements one by one.
//
// @param ctx {context.Context} the call's context
// @param w {io.Writer} where to report
// @param src {TagSources} the listings and clients the kinds are resolved against
// @param opts {TagResourceOptions} the parsed arguments
// @returns {error} a usage error for a resource that does not exist or is ambiguous, an exit-1 error for a refused change or a failed write
func RunTagResourceCLI(ctx context.Context, w io.Writer, src TagSources, opts TagResourceOptions) error {
	target, err := resolveTagTarget(ctx, src, opts.Kind, opts.Name)
	if err != nil {
		return err
	}

	added, unchanged := map[string]string{}, map[string]string{}
	replaced := map[string]tagReplacedJSON{}
	var refused []string
	for _, t := range opts.Tags {
		current, has := target.tags[t.Key]
		switch {
		case !has:
			added[t.Key] = t.Value
		case current == t.Value:
			unchanged[t.Key] = t.Value
		case opts.Replace:
			replaced[t.Key] = tagReplacedJSON{From: current, To: t.Value}
		default:
			refused = append(refused, fmt.Sprintf("%s is %q, not %q", t.Key, current, t.Value))
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%s already has a different value (%s); nothing was changed. Add --replace to change it", target.label, strings.Join(refused, "; "))
	}

	for _, t := range opts.Tags {
		action := "add"
		if _, ok := replaced[t.Key]; ok {
			action = "update"
		} else if _, ok := added[t.Key]; !ok {
			continue
		}
		if err := target.apply(ctx, TagChangeParams{ResourceID: target.id, Action: action, Key: t.Key, Value: t.Value}); err != nil {
			return fmt.Errorf("tagging %s: %w", target.id, err)
		}
	}

	final := map[string]string{}
	for k, v := range target.tags {
		final[k] = v
	}
	for k, v := range added {
		final[k] = v
	}
	for k, r := range replaced {
		final[k] = r.To
	}
	if opts.Format != ui.FormatText {
		return ui.WriteJSONValue(w, tagResourceJSON{opts.KindSlug, target.id, target.name, added, replaced, unchanged, final}, opts.Format)
	}
	var parts []string
	var addedList, unchangedList []string
	for _, t := range opts.Tags {
		if v, ok := added[t.Key]; ok {
			addedList = append(addedList, t.Key+"="+v)
		}
		if r, ok := replaced[t.Key]; ok {
			parts = append(parts, fmt.Sprintf("replaced %s (%s -> %s)", t.Key, r.From, r.To))
		}
		if v, ok := unchanged[t.Key]; ok {
			unchangedList = append(unchangedList, t.Key+"="+v)
		}
	}
	if len(addedList) > 0 {
		parts = append([]string{"added " + strings.Join(addedList, ", ")}, parts...)
	}
	if len(unchangedList) > 0 {
		parts = append(parts, "already had "+strings.Join(unchangedList, ", "))
	}
	_, err = fmt.Fprintf(w, "%s: %s.\n", target.label, strings.Join(parts, "; "))
	return err
}
