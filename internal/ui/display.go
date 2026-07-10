// Package ui provides terminal interaction: pick lists, prompts, and
// formatted resource display, built on github.com/rsdoiel/termlib.
package ui

import (
	"context"
	"fmt"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
)

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

// stateColor maps an instance state to a termlib color constant for
// DisplayInstances, or "" for states with no specific color (PLAN.md,
// Phase 15, "Color output for state").
func stateColor(state string) string {
	switch state {
	case "running":
		return termlib.Green
	case "stopped", "terminated", "shutting-down":
		return termlib.Red
	case "pending", "stopping":
		return termlib.Yellow
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
		termlib.PadRight("INSTANCE ID", 20),
		termlib.PadRight("NAME", 20),
		termlib.PadRight("STATE", 12),
		termlib.PadRight("AMI ID", 20),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("PROJECT", 16),
		termlib.PadRight("ENVIRONMENT", 11),
		termlib.PadRight("PUBLIC IP", 15),
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
	state := termlib.PadRight(termlib.Truncate(inst.State, 12), 12)
	if colorEnabled {
		if c := stateColor(inst.State); c != "" {
			state = c + state + termlib.Reset
		}
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		termlib.PadRight(termlib.Truncate(inst.InstanceID, 20), 20),
		termlib.PadRight(termlib.Truncate(inst.Name, 20), 20),
		state,
		termlib.PadRight(termlib.Truncate(inst.ImageID, 20), 20),
		termlib.PadRight(inst.Region, 10),
		termlib.PadRight(termlib.Truncate(orUnknown(inst.Project), 16), 16),
		termlib.PadRight(orUnknown(inst.Environment), 11),
		termlib.PadRight(orNone(inst.PublicIP), 15),
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
		termlib.PadRight("AMI ID", 20),
		termlib.PadRight("NAME", 28),
		termlib.PadRight("CREATION DATE", 20),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("PROJECT", 16),
		"ENVIRONMENT")

	rows := make([]string, len(images))
	for i, img := range images {
		rows[i] = fmt.Sprintf("%s %s %s %s %s %s",
			termlib.PadRight(termlib.Truncate(img.ImageID, 20), 20),
			termlib.PadRight(termlib.Truncate(img.Name, 28), 28),
			termlib.PadRight(termlib.Truncate(img.CreationDate, 19), 20),
			termlib.PadRight(img.Region, 10),
			termlib.PadRight(termlib.Truncate(orUnknown(img.Project), 16), 16),
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
		termlib.PadRight("KEY NAME", 24),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("TYPE", 8),
		termlib.PadRight("KEY ID", 22),
		"FINGERPRINT")

	rows := make([]string, len(keyPairs))
	for i, kp := range keyPairs {
		rows[i] = fmt.Sprintf("%s %s %s %s %s",
			termlib.PadRight(termlib.Truncate(kp.KeyName, 24), 24),
			termlib.PadRight(kp.Region, 10),
			termlib.PadRight(kp.KeyType, 8),
			termlib.PadRight(termlib.Truncate(kp.KeyPairID, 22), 22),
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
		termlib.PadRight("NAME", 40),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("STATIC WEBSITE", 14),
		"PURPOSE")

	rows := make([]string, len(buckets))
	for i, b := range buckets {
		rows[i] = fmt.Sprintf("%s %s %s %s",
			termlib.PadRight(termlib.Truncate(b.Name, 40), 40),
			termlib.PadRight(b.Region, 10),
			termlib.PadRight(staticWebsiteLabel(b.StaticWebsite), 14),
			b.Purpose)
	}

	return tui.ListViewConfig{
		Title:        "S3 Buckets",
		Header:       header,
		Rows:         rows,
		ColorEnabled: ColorEnabled(),
	}
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
