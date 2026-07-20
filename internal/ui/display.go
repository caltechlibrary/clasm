// Package ui provides terminal interaction: prompts and formatted
// resource display.
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
)

// truncate shortens s to at most maxW Unicode code points, replacing the
// last one with an ellipsis when it doesn't fit -- replaces termlib's
// equivalent (DECISIONS.md, "Remove termlib entirely: input via huh,
// output via io.Writer").
func truncate(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 1 {
		return "…"
	}
	return string(runes[:maxW-1]) + "…"
}

// padRight pads s with trailing spaces to exactly w Unicode code points,
// truncating first if s is already longer than w.
func padRight(s string, w int) string {
	runes := []rune(s)
	if len(runes) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len(runes))
}

// unknown renders an untagged Project/Environment value -- see
// DECISIONS.md, "Introduce a light Project/Environment tagging
// convention". Name has no such convention and simply displays blank
// when untagged, matching ec2_ami_manager.bash's display_instances.
const unknown = "unknown"

func orUnknown(s string) string {
	if s == "" {
		return unknown
	}
	return s
}

// none renders an instance with no assigned public/private IP (e.g.
// stopped, or launched without a public IP/EIP) -- distinct from
// "unknown" above, since this isn't missing data, it's a legitimate
// state (see DECISIONS.md, "Show instance IP addresses in the main
// listing").
const none = "none"

func orNone(s string) string {
	if s == "" {
		return none
	}
	return s
}

// stateColor maps an instance state to an ANSI color constant for
// DisplayInstances, or "" for states with no specific color (PLAN.md,
// Phase 15, "Color output for state").
func stateColor(state string) string {
	switch state {
	case "running":
		return ansiGreen
	case "stopped", "terminated", "shutting-down":
		return ansiRed
	case "pending", "stopping":
		return ansiYellow
	default:
		return ""
	}
}

// instanceListViewConfig builds a tui.ListViewConfig from instances,
// reusing the same PadRight/Truncate column formatting DisplayInstances
// has always used. Extracted so the formatting itself is unit-testable
// without driving tui.RunListView's interactive loop (mirrors
// bucketListViewConfig, Phase 20.6). The STATE column is colorized
// (running=green, stopped/terminated=red, pending/stopping=yellow) when
// ColorEnabled() is true (NO_COLOR convention + non-TTY fallback,
// PLAN.md Phase 15, "Color output for state") -- embedding that color
// into the row string works safely with ListView's own cursor-row
// reverse-video highlight because reverseVideo (internal/tui/style.go)
// re-asserts itself after any inner reset a row already carries.
func instanceListViewConfig(instances []inventory.Instance) tui.ListViewConfig {
	colorEnabled := ColorEnabled()

	header := fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		padRight("INSTANCE ID", 20),
		padRight("NAME", 20),
		padRight("STATE", 12),
		padRight("AMI ID", 20),
		padRight("REGION", 10),
		padRight("PROJECT", 16),
		padRight("ENVIRONMENT", 11),
		padRight("PUBLIC IP", 15),
		"PRIVATE IP")

	rows := make([]string, len(instances))
	for i, inst := range instances {
		rows[i] = instanceRow(inst, colorEnabled)
	}

	return tui.ListViewConfig{
		Title:        "EC2 Instances",
		Header:       header,
		Rows:         rows,
		ColorEnabled: colorEnabled,
	}
}

// instanceRow formats one instance's row, taking colorEnabled as an
// explicit parameter (rather than reading ColorEnabled() itself) so the
// STATE column's color-embedding logic stays unit-testable independent
// of the real terminal/NO_COLOR environment a test runs under.
func instanceRow(inst inventory.Instance, colorEnabled bool) string {
	// Truncate/pad on the plain text first, then wrap in ANSI codes --
	// escape sequences are zero-width on screen but would otherwise be
	// counted as visible characters by PadRight/Truncate.
	state := padRight(truncate(inst.State, 12), 12)
	if colorEnabled {
		if c := stateColor(inst.State); c != "" {
			state = c + state + ansiReset
		}
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		padRight(truncate(inst.InstanceID, 20), 20),
		padRight(truncate(inst.Name, 20), 20),
		state,
		padRight(truncate(inst.ImageID, 20), 20),
		padRight(inst.Region, 10),
		padRight(truncate(orUnknown(inst.Project), 16), 16),
		padRight(orUnknown(inst.Environment), 11),
		padRight(orNone(inst.PublicIP), 15),
		orNone(inst.PrivateIP))
}

// DisplayInstances shows EC2 instances in the shared List-tier
// component (DESIGN.md, "Terminal UI Architecture: Menus, Actions,
// Lists, and Managers"), replacing ec2_ami_manager.bash's
// display_instances. Reachable only from the Compute menu's explicit
// "Show resource lists" choice, not shown automatically after other
// Compute actions -- see workflow.MenuActions.ShowResourceLists.
func DisplayInstances(ctx context.Context, instances []inventory.Instance) error {
	return tui.RunListView(ctx, instanceListViewConfig(instances))
}

// imageListViewConfig builds a tui.ListViewConfig from images -- see
// instanceListViewConfig's doc comment for the extraction rationale.
func imageListViewConfig(images []inventory.Image) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s %s %s",
		padRight("AMI ID", 20),
		padRight("NAME", 28),
		padRight("CREATION DATE", 20),
		padRight("REGION", 10),
		padRight("PROJECT", 16),
		"ENVIRONMENT")

	rows := make([]string, len(images))
	for i, img := range images {
		rows[i] = fmt.Sprintf("%s %s %s %s %s %s",
			padRight(truncate(img.ImageID, 20), 20),
			padRight(truncate(img.Name, 28), 28),
			padRight(truncate(img.CreationDate, 19), 20),
			padRight(img.Region, 10),
			padRight(truncate(orUnknown(img.Project), 16), 16),
			orUnknown(img.Environment))
	}

	return tui.ListViewConfig{
		Title:        "AMIs (owned by account)",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayImages shows AMIs in the shared List-tier component, replacing
// ec2_ami_manager.bash's display_amis -- same reachability convention as
// DisplayInstances.
func DisplayImages(ctx context.Context, images []inventory.Image) error {
	return tui.RunListView(ctx, imageListViewConfig(images))
}

// keyPairListViewConfig builds a tui.ListViewConfig from keyPairs -- see
// instanceListViewConfig's doc comment for the extraction rationale.
func keyPairListViewConfig(keyPairs []inventory.KeyPair) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s %s",
		padRight("KEY NAME", 24),
		padRight("REGION", 10),
		padRight("TYPE", 8),
		padRight("KEY ID", 22),
		"FINGERPRINT")

	rows := make([]string, len(keyPairs))
	for i, kp := range keyPairs {
		rows[i] = fmt.Sprintf("%s %s %s %s %s",
			padRight(truncate(kp.KeyName, 24), 24),
			padRight(kp.Region, 10),
			padRight(kp.KeyType, 8),
			padRight(truncate(kp.KeyPairID, 22), 22),
			kp.KeyFingerprint)
	}

	return tui.ListViewConfig{
		Title:        "Key Pairs",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayKeyPairs shows EC2 key pairs in the shared List-tier component
// (DESIGN.md, Feature 13: "List Key Pairs") -- same reachability
// convention as DisplayInstances.
func DisplayKeyPairs(ctx context.Context, keyPairs []inventory.KeyPair) error {
	return tui.RunListView(ctx, keyPairListViewConfig(keyPairs))
}

// staticWebsiteLabel renders Bucket.StaticWebsite as a plain yes/no,
// matching this table's other yes/no-shaped columns.
func staticWebsiteLabel(configured bool) string {
	if configured {
		return "yes"
	}
	return "no"
}

// bucketListViewConfig builds a tui.ListViewConfig from buckets, reusing
// the same PadRight/Truncate column formatting this table has always
// used. Extracted from DisplayBuckets so the formatting itself is
// unit-testable without driving tui.RunListView's interactive loop.
func bucketListViewConfig(buckets []inventory.Bucket) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s",
		padRight("NAME", 40),
		padRight("REGION", 10),
		padRight("STATIC WEBSITE", 14),
		"PURPOSE")

	rows := make([]string, len(buckets))
	for i, b := range buckets {
		rows[i] = fmt.Sprintf("%s %s %s %s",
			padRight(truncate(b.Name, 40), 40),
			padRight(b.Region, 10),
			padRight(staticWebsiteLabel(b.StaticWebsite), 14),
			b.Purpose)
	}

	return tui.ListViewConfig{
		Title:        "S3 Buckets",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// launchTemplateListViewConfig builds a tui.ListViewConfig from
// templates -- see instanceListViewConfig's doc comment for the
// extraction rationale.
func launchTemplateListViewConfig(templates []inventory.LaunchTemplate) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s %s %s %s %s",
		padRight("TEMPLATE ID", 22),
		padRight("NAME", 24),
		padRight("DEFAULT", 8),
		padRight("LATEST", 8),
		padRight("REGION", 10),
		padRight("PROJECT", 16),
		"ENVIRONMENT")

	rows := make([]string, len(templates))
	for i, lt := range templates {
		rows[i] = fmt.Sprintf("%s %s %s %s %s %s %s",
			padRight(truncate(lt.TemplateID, 22), 22),
			padRight(truncate(lt.Name, 24), 24),
			padRight(fmt.Sprintf("%d", lt.DefaultVersion), 8),
			padRight(fmt.Sprintf("%d", lt.LatestVersion), 8),
			padRight(lt.Region, 10),
			padRight(truncate(orUnknown(lt.Project), 16), 16),
			orUnknown(lt.Environment))
	}

	return tui.ListViewConfig{
		Title:        "Launch Templates",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayLaunchTemplates shows launch templates in the shared List-tier
// component (DESIGN.md, "Launch Templates") -- same reachability
// convention as DisplayInstances: folded into the Compute domain's
// "Show resource lists" choice, not a separate top-level action.
func DisplayLaunchTemplates(ctx context.Context, templates []inventory.LaunchTemplate) error {
	return tui.RunListView(ctx, launchTemplateListViewConfig(templates))
}

// DisplayBuckets shows S3 buckets in the shared List-tier component
// (DESIGN.md, "Terminal UI Architecture: Menus, Actions, Lists, and
// Managers," revising Feature 17: "List Buckets"). Reachable only from
// the S3 menu's explicit "Show resource lists" choice, not shown
// automatically after other S3 actions -- see
// workflow.S3Actions.ShowResourceLists.
func DisplayBuckets(ctx context.Context, buckets []inventory.Bucket) error {
	return tui.RunListView(ctx, bucketListViewConfig(buckets))
}

// TaggedResource is one row for the Tag Management domain's "Show all
// tags" listing (DESIGN.md, "Tag Management Domain"): ID/Label
// identify the resource (matching that resource kind's own Picker-tier
// label), Tags is its full tag set. One shared shape across every
// resource kind (Instance/AMI/Launch Template/Key Pair, and eventually
// S3 Bucket) -- deliberately not a combined table spanning every kind
// at once (DECISIONS.md, "Tag Management: a fourth domain...",
// rejected alternatives: no shared row shape, and tag key sets vary
// per resource regardless), just one shared row shape reused per
// kind-scoped listing.
type TaggedResource struct {
	ID    string
	Label string
	Tags  map[string]string
}

// flattenTags renders a tag map as one "key=value, key2=value2" string,
// sorted by key for stable output -- the TAGS column shows every tag
// key (DECISIONS.md, "Show all tags" design), not just the Project/
// Environment convention the other List-tier tables restrict
// themselves to.
func flattenTags(tags map[string]string) string {
	if len(tags) == 0 {
		return "(no tags)"
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = fmt.Sprintf("%s=%s", k, tags[k])
	}
	return strings.Join(pairs, ", ")
}

// tagsListViewConfig builds a tui.ListViewConfig from a flattened list
// of tagged resources -- title is the resource kind's own display name
// (e.g. "EC2 Instances -- All Tags"), set by the caller since this
// function is shared across every kind. See instanceListViewConfig's
// doc comment for the extraction rationale.
func tagsListViewConfig(title string, resources []TaggedResource) tui.ListViewConfig {
	header := fmt.Sprintf("%s %s %s",
		padRight("ID", 22),
		padRight("LABEL", 40),
		"TAGS")

	rows := make([]string, len(resources))
	for i, r := range resources {
		rows[i] = fmt.Sprintf("%s %s %s",
			padRight(truncate(r.ID, 22), 22),
			padRight(truncate(r.Label, 40), 40),
			flattenTags(r.Tags))
	}

	return tui.ListViewConfig{
		Title:        title,
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
}

// DisplayAllTags shows every resource of one kind with its full tag set
// in the shared List-tier component -- the Tag Management domain's
// "Show all tags" action (DESIGN.md, "Tag Management Domain"),
// reachable only via that domain's explicit menu choice, same
// reachability convention as DisplayInstances.
func DisplayAllTags(ctx context.Context, title string, resources []TaggedResource) error {
	return tui.RunListView(ctx, tagsListViewConfig(title, resources))
}
