// Package ui provides terminal interaction: pick lists, prompts, and
// formatted resource display, built on github.com/rsdoiel/termlib.
package ui

import (
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/inventory"
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

// DisplayInstances prints a formatted table of instances, replacing
// ec2_ami_manager.bash's display_instances. When colorEnabled is true,
// the STATE column is colorized (running=green, stopped/terminated=red,
// pending/stopping=yellow); callers should set this from ColorEnabled
// (NO_COLOR convention + a non-TTY fallback), not unconditionally.
func DisplayInstances(t *termlib.Terminal, instances []inventory.Instance, colorEnabled bool) {
	if len(instances) == 0 {
		t.Println("No EC2 instances found.")
		t.Refresh()
		return
	}

	t.Println("===== CURRENT EC2 INSTANCES =====")
	t.Println()
	t.Printf("%s %s %s %s %s %s %s %s %s\n",
		termlib.PadRight("INSTANCE ID", 20),
		termlib.PadRight("NAME", 20),
		termlib.PadRight("STATE", 12),
		termlib.PadRight("AMI ID", 20),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("PROJECT", 16),
		termlib.PadRight("ENVIRONMENT", 11),
		termlib.PadRight("PUBLIC IP", 15),
		"PRIVATE IP")
	for _, inst := range instances {
		// Truncate/pad on the plain text first, then wrap in ANSI codes --
		// escape sequences are zero-width on screen but would otherwise
		// be counted as visible characters by PadRight/Truncate.
		state := termlib.PadRight(termlib.Truncate(inst.State, 12), 12)
		if colorEnabled {
			if c := stateColor(inst.State); c != "" {
				state = c + state + termlib.Reset
			}
		}
		t.Printf("%s %s %s %s %s %s %s %s %s\n",
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
	t.Println()
	t.Refresh()
}

// DisplayImages prints a formatted table of AMIs, replacing
// ec2_ami_manager.bash's display_amis.
func DisplayImages(t *termlib.Terminal, images []inventory.Image) {
	if len(images) == 0 {
		t.Println("No AMIs found.")
		t.Refresh()
		return
	}

	t.Println("===== AVAILABLE AMIs (owned by account) =====")
	t.Println()
	t.Printf("%s %s %s %s %s %s\n",
		termlib.PadRight("AMI ID", 20),
		termlib.PadRight("NAME", 28),
		termlib.PadRight("CREATION DATE", 20),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("PROJECT", 16),
		"ENVIRONMENT")
	for _, img := range images {
		t.Printf("%s %s %s %s %s %s\n",
			termlib.PadRight(termlib.Truncate(img.ImageID, 20), 20),
			termlib.PadRight(termlib.Truncate(img.Name, 28), 28),
			termlib.PadRight(termlib.Truncate(img.CreationDate, 19), 20),
			termlib.PadRight(img.Region, 10),
			termlib.PadRight(termlib.Truncate(orUnknown(img.Project), 16), 16),
			orUnknown(img.Environment))
	}
	t.Println()
	t.Refresh()
}

// DisplayKeyPairs prints a formatted table of EC2 key pairs (DESIGN.md,
// Feature 13: "List Key Pairs").
func DisplayKeyPairs(t *termlib.Terminal, keyPairs []inventory.KeyPair) {
	if len(keyPairs) == 0 {
		t.Println("No key pairs found.")
		t.Refresh()
		return
	}

	t.Println("===== KEY PAIRS =====")
	t.Println()
	t.Printf("%s %s %s %s %s\n",
		termlib.PadRight("KEY NAME", 24),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("TYPE", 8),
		termlib.PadRight("KEY ID", 22),
		"FINGERPRINT")
	for _, kp := range keyPairs {
		t.Printf("%s %s %s %s %s\n",
			termlib.PadRight(termlib.Truncate(kp.KeyName, 24), 24),
			termlib.PadRight(kp.Region, 10),
			termlib.PadRight(kp.KeyType, 8),
			termlib.PadRight(termlib.Truncate(kp.KeyPairID, 22), 22),
			kp.KeyFingerprint)
	}
	t.Println()
	t.Refresh()
}

// staticWebsiteLabel renders Bucket.StaticWebsite as a plain yes/no,
// matching this table's other yes/no-shaped columns.
func staticWebsiteLabel(configured bool) string {
	if configured {
		return "yes"
	}
	return "no"
}

// DisplayBuckets prints a formatted table of S3 buckets (DESIGN.md,
// Feature 17: "List Buckets").
func DisplayBuckets(t *termlib.Terminal, buckets []inventory.Bucket) {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return
	}

	t.Println("===== S3 BUCKETS =====")
	t.Println()
	t.Printf("%s %s %s %s\n",
		termlib.PadRight("NAME", 40),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("STATIC WEBSITE", 14),
		"PURPOSE")
	for _, b := range buckets {
		t.Printf("%s %s %s %s\n",
			termlib.PadRight(termlib.Truncate(b.Name, 40), 40),
			termlib.PadRight(b.Region, 10),
			termlib.PadRight(staticWebsiteLabel(b.StaticWebsite), 14),
			b.Purpose)
	}
	t.Println()
	t.Refresh()
}
