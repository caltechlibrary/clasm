// Package ui provides terminal interaction: pick lists, prompts, and
// formatted resource display, built on github.com/rsdoiel/termlib.
package ui

import (
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/inventory"
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
