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

// DisplayInstances prints a formatted table of instances, replacing
// ec2_ami_manager.bash's display_instances.
func DisplayInstances(t *termlib.Terminal, instances []inventory.Instance) {
	if len(instances) == 0 {
		t.Println("No EC2 instances found.")
		t.Refresh()
		return
	}

	t.Println("===== CURRENT EC2 INSTANCES =====")
	t.Println()
	t.Printf("%s %s %s %s %s %s %s\n",
		termlib.PadRight("INSTANCE ID", 20),
		termlib.PadRight("NAME", 20),
		termlib.PadRight("STATE", 12),
		termlib.PadRight("AMI ID", 20),
		termlib.PadRight("REGION", 10),
		termlib.PadRight("PROJECT", 16),
		"ENVIRONMENT")
	for _, inst := range instances {
		t.Printf("%s %s %s %s %s %s %s\n",
			termlib.PadRight(termlib.Truncate(inst.InstanceID, 20), 20),
			termlib.PadRight(termlib.Truncate(inst.Name, 20), 20),
			termlib.PadRight(termlib.Truncate(inst.State, 12), 12),
			termlib.PadRight(termlib.Truncate(inst.ImageID, 20), 20),
			termlib.PadRight(inst.Region, 10),
			termlib.PadRight(termlib.Truncate(orUnknown(inst.Project), 16), 16),
			orUnknown(inst.Environment))
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
