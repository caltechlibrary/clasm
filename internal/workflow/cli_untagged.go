package workflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The untagged-resource report (DR-0192): which resources lack the configured Origin
// tag, so the author can tag the existing fleet (clasm tags what it creates, DR-0189 to
// DR-0191, but what was there before is untagged). Read-only; it says only that the tag
// is missing, never whose the resource is.

// UntaggedItem is one resource that lacks the Origin tag, or has it with a value other
// than the DLD-owned one.
type UntaggedItem struct {
	Kind        string // the CLI kind slug
	ID          string
	Name        string
	Region      string // "" for IAM, which is global
	Created     string // YYYY-MM-DD, "" when AWS does not report one cheaply
	OriginValue string // the value the tag has; "" when it is missing
	Tags        map[string]string
}

// UntaggedReport is what BuildUntaggedReport found.
type UntaggedReport struct {
	// Missing lacks the tag; OtherValue has the tag with a value other than the DLD-owned
	// one, which is a decision for the author, not a gap. Both are in picker order of
	// kind, then by name.
	Missing, OtherValue []UntaggedItem
	// ServiceLinkedRoles is how many AWS service-linked IAM roles were left out: they
	// are not the account's to tag.
	ServiceLinkedRoles int
}

// serviceLinkedRolePath is the IAM path of every AWS service-linked role.
const serviceLinkedRolePath = "/aws-service-role/"

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// candidatesForKind lists one kind's resources with their tags, fetching what that
// kind needs, and counts the service-linked roles it left out.
func candidatesForKind(ctx context.Context, kind string, src TagSources) (items []UntaggedItem, serviceLinked int, err error) {
	slug := tagKindSlugs[kind]
	add := func(id, name, region, created string, tags map[string]string) {
		items = append(items, UntaggedItem{Kind: slug, ID: id, Name: name, Region: region, Created: created, Tags: tags})
	}
	date := func(t interface {
		IsZero() bool
		Format(string) string
	}) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02")
	}
	switch kind {
	case "Instance":
		for _, i := range src.Instances {
			add(i.InstanceID, i.Name, i.Region, i.Created, i.Tags)
		}
	case "AMI":
		for _, i := range src.Images {
			add(i.ImageID, i.Name, i.Region, dateOnly(i.CreationDate), i.Tags)
		}
	case "Launch Template":
		for _, l := range src.LaunchTemplates {
			add(l.TemplateID, l.Name, l.Region, l.Created, l.Tags)
		}
	case "Key Pair":
		for _, k := range src.KeyPairs {
			add(k.KeyPairID, k.KeyName, k.Region, k.Created, k.Tags)
		}
	case "S3 Bucket":
		for _, b := range src.Buckets {
			client, err := src.NewS3Client(ctx, b.Region)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: building client for region %s: %w", b.Name, b.Region, err)
			}
			tags, err := fetchBucketTags(ctx, client, b.Name)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: fetching tags: %w", b.Name, err)
			}
			add(b.Name, b.Name, b.Region, b.Created, tags)
		}
	case "IAM Role":
		roles, err := inventory.ListIAMRoleSummaries(ctx, src.IAMClient, src.OriginTag)
		if err != nil {
			return nil, 0, err
		}
		for _, r := range roles {
			if strings.HasPrefix(r.Path, serviceLinkedRolePath) {
				serviceLinked++
				continue
			}
			add(r.Name, r.Name, "", date(r.CreateDate), r.Tags)
		}
	case "IAM Instance Profile":
		profiles, err := inventory.ListIAMInstanceProfileSummaries(ctx, src.IAMClient, src.OriginTag)
		if err != nil {
			return nil, 0, err
		}
		for _, p := range profiles {
			add(p.Name, p.Name, "", date(p.CreateDate), p.Tags)
		}
	case "IAM Policy":
		policies, err := inventory.ListIAMPolicySummaries(ctx, src.IAMClient, src.OriginTag)
		if err != nil {
			return nil, 0, err
		}
		for _, p := range policies {
			add(p.ARN, p.Name, "", date(p.CreateDate), p.Tags)
		}
	case "EBS Volume":
		volumes, err := inventory.ListVolumes(ctx, src.EC2Clients)
		if err != nil {
			return nil, 0, err
		}
		for _, v := range volumes {
			add(v.VolumeID, v.Tags["Name"], v.Region, v.Created, v.Tags)
		}
	case "EBS Snapshot":
		snapshots, err := inventory.ListSnapshots(ctx, src.EC2Clients)
		if err != nil {
			return nil, 0, err
		}
		for _, s := range snapshots {
			add(s.SnapshotID, s.Tags["Name"], s.Region, s.Started, s.Tags)
		}
	case "Network Interface":
		interfaces, err := inventory.ListNetworkInterfaces(ctx, src.EC2Clients)
		if err != nil {
			return nil, 0, err
		}
		for _, n := range interfaces {
			add(n.ID, n.Tags["Name"], n.Region, "", n.Tags)
		}
	}
	return items, serviceLinked, nil
}

// BuildUntaggedReport lists the resources of the given kinds (labels as the Tag
// Management picker shows them; none means all eleven) that lack the configured Origin
// tag, or have it with another value. Only the data the chosen kinds need is fetched,
// and only reads are sent.
//
// @param ctx {context.Context} the call's context
// @param src {TagSources} the listings and clients to read from, and the configured Origin tag
// @param kinds {[]string} the kinds to report, empty for all
// @returns {UntaggedReport, error} the resources found, or the first listing's error
// @example
//
//	report, err := BuildUntaggedReport(ctx, src, []string{"Instance", "AMI"})
func BuildUntaggedReport(ctx context.Context, src TagSources, kinds []string) (UntaggedReport, error) {
	chosen := map[string]bool{}
	for _, k := range kinds {
		chosen[k] = true
	}
	var report UntaggedReport
	for _, kind := range tagManagementKinds {
		if len(kinds) > 0 && !chosen[kind] {
			continue
		}
		items, serviceLinked, err := candidatesForKind(ctx, kind, src)
		if err != nil {
			return UntaggedReport{}, err
		}
		report.ServiceLinkedRoles += serviceLinked
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].Name < items[j].Name || (items[i].Name == items[j].Name && items[i].ID < items[j].ID)
		})
		for _, it := range items {
			value, has := it.Tags[src.OriginTag.Key]
			switch {
			case !has:
				report.Missing = append(report.Missing, it)
			case value != src.OriginTag.DLDValue || src.OriginTag.DLDValue == "":
				it.OriginValue = value
				report.OtherValue = append(report.OtherValue, it)
			}
		}
	}
	return report, nil
}

// RunShowUntaggedCLI is `clasm tag-management show-untagged [<kind>...]`: the report for
// the named kinds (all eleven if none), missing first, then those with another value.
// An unknown kind is a usage error listing the valid ones, before any call.
//
// @param ctx {context.Context} the call's context
// @param w {io.Writer} where to write the report
// @param src {TagSources} the listings and clients to read from
// @param kindSlugs {[]string} the kind slugs to report, empty for all
// @param format {ui.Format} text, JSON or JSON Lines
// @returns {error} a usage error for an unknown kind, or a listing or write failure
func RunShowUntaggedCLI(ctx context.Context, w io.Writer, src TagSources, kindSlugs []string, format ui.Format) error {
	var kinds []string
	for _, slug := range kindSlugs {
		found := false
		for label, s := range tagKindSlugs {
			if s == slug {
				kinds, found = append(kinds, label), true
			}
		}
		if !found {
			valid := TagKindSlugs()
			sort.Strings(valid)
			return &UsageError{Msg: fmt.Sprintf("unknown kind %q; the kinds are: %s", slug, strings.Join(valid, ", "))}
		}
	}
	report, err := BuildUntaggedReport(ctx, src, kinds)
	if err != nil {
		return err
	}
	rows := make([]ui.UntaggedRow, 0, len(report.Missing)+len(report.OtherValue))
	for _, it := range report.Missing {
		rows = append(rows, ui.UntaggedRow{Kind: it.Kind, ID: it.ID, Name: it.Name, Region: it.Region, Created: it.Created, Status: "missing", Tags: it.Tags})
	}
	for _, it := range report.OtherValue {
		rows = append(rows, ui.UntaggedRow{Kind: it.Kind, ID: it.ID, Name: it.Name, Region: it.Region, Created: it.Created, Status: "other-value", OriginValue: it.OriginValue, Tags: it.Tags})
	}
	return ui.WriteUntagged(w, rows, untaggedNote(report), format)
}

// untaggedNote says what the report leaves out, so a short list is not mistaken for a
// complete account.
func untaggedNote(r UntaggedReport) string {
	note := "AWS-managed IAM policies, network interfaces an AWS service manages for you, and snapshots and AMIs other accounts own are never listed."
	if r.ServiceLinkedRoles > 0 {
		note = fmt.Sprintf("Left out: %d AWS service-linked IAM role(s), which are not the account's to tag. ", r.ServiceLinkedRoles) + note
	}
	return note
}

// untaggedRowsForDisplay turns the report into the rows of the shared list view: those
// missing the tag first, then those with another value, each labelled with what the
// author needs to decide.
func untaggedRowsForDisplay(r UntaggedReport) []ui.TaggedResource {
	label := func(it UntaggedItem, reason string) string {
		return strings.Join(nonEmpty(it.Kind, it.Name, it.Region, it.Created, reason), "  ")
	}
	rows := make([]ui.TaggedResource, 0, len(r.Missing)+len(r.OtherValue))
	for _, it := range r.Missing {
		rows = append(rows, ui.TaggedResource{ID: it.ID, Label: label(it, "missing"), Tags: it.Tags})
	}
	for _, it := range r.OtherValue {
		rows = append(rows, ui.TaggedResource{ID: it.ID, Label: label(it, "other: "+it.OriginValue), Tags: it.Tags})
	}
	return rows
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allKindsChoice is the kind picker's extra first entry.
const allKindsChoice = "All kinds"

// kindsForChoice is the kinds the picker's choice stands for: none (meaning all) for "All
// kinds", else that one kind.
func kindsForChoice(choice string) []string {
	if choice == allKindsChoice {
		return nil
	}
	return []string{choice}
}

// ShowUntagged runs the Tag Management domain's "Show untagged resources" action: pick a
// kind, or all of them, then list the resources that lack the Origin tag in the shared
// list view. The same report `show-untagged` prints.
//
// @param ctx {context.Context} the call's context
// @param w {io.Writer} where prompts and messages go
// @param src {TagSources} the listings and clients to read from
// @returns {error} a listing failure
func ShowUntagged(ctx context.Context, w io.Writer, src TagSources) error {
	return showUntagged(ctx, w, src, nil, nil)
}

// showUntagged is ShowUntagged's testable core: menuInput and menuOutput are nil in
// production and drive the kind picker through its accessible-mode pipe path in tests.
func showUntagged(ctx context.Context, w io.Writer, src TagSources, menuInput io.Reader, menuOutput io.Writer) error {
	choice, err := pickString(w, "Show untagged resources for", "Lists the resources that lack the Origin tag, and those that have it with another value.",
		hintCancel, append([]string{allKindsChoice}, tagManagementKinds...), menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	report, err := BuildUntaggedReport(ctx, src, kindsForChoice(choice))
	if err != nil {
		return err
	}
	rows := untaggedRowsForDisplay(report)
	if len(rows) == 0 {
		fmt.Fprintln(w, "Nothing is missing the Origin tag.")
		return nil
	}
	return ui.DisplayAllTags(ctx, "Resources lacking the Origin tag", rows)
}
