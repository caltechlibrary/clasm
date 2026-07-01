package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestDisplayInstances_Empty(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	DisplayInstances(term, nil)

	if !strings.Contains(buf.String(), "No EC2 instances found.") {
		t.Errorf("output = %q, want it to mention no instances found", buf.String())
	}
}

func TestDisplayInstances_Populated(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	instances := []inventory.Instance{
		{InstanceID: "i-012345", Name: "web-server", State: "running", ImageID: "ami-abc123", Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{InstanceID: "i-067890", Name: "db-server", State: "stopped", ImageID: "ami-def456", Region: "us-west-2"},
	}

	DisplayInstances(term, instances)
	out := buf.String()

	for _, want := range []string{"i-012345", "web-server", "running", "ami-abc123", "us-east-1", "caltechauthors", "production"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// untagged Project/Environment render as "unknown"
	if !strings.Contains(out, "db-server") || !strings.Contains(out, "unknown") {
		t.Errorf("output missing untagged instance's %q rendering:\n%s", "unknown", out)
	}
}

func TestDisplayImages_Empty(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	DisplayImages(term, nil)

	if !strings.Contains(buf.String(), "No AMIs found.") {
		t.Errorf("output = %q, want it to mention no AMIs found", buf.String())
	}
}

func TestDisplayImages_Populated(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	images := []inventory.Image{
		{ImageID: "ami-abc123", Name: "base-ubuntu-2404", CreationDate: "2026-01-15", Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{ImageID: "ami-def456", Name: "custom-ami", CreationDate: "2026-03-10", Region: "us-east-1"},
	}

	DisplayImages(term, images)
	out := buf.String()

	for _, want := range []string{"ami-abc123", "base-ubuntu-2404", "2026-01-15", "us-east-1", "caltechauthors", "production"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "custom-ami") || !strings.Contains(out, "unknown") {
		t.Errorf("output missing untagged image's %q rendering:\n%s", "unknown", out)
	}
}
