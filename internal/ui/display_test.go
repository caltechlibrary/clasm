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

	DisplayInstances(term, nil, false)

	if !strings.Contains(buf.String(), "No EC2 instances found.") {
		t.Errorf("output = %q, want it to mention no instances found", buf.String())
	}
}

func TestDisplayInstances_Populated(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	instances := []inventory.Instance{
		{InstanceID: "i-012345", Name: "web-server", State: "running", ImageID: "ami-abc123", Region: "us-east-1", Project: "caltechauthors", Environment: "production", PublicIP: "203.0.113.25", PrivateIP: "10.0.1.25"},
		{InstanceID: "i-067890", Name: "db-server", State: "stopped", ImageID: "ami-def456", Region: "us-west-2"},
	}

	DisplayInstances(term, instances, false)
	out := buf.String()

	for _, want := range []string{"i-012345", "web-server", "running", "ami-abc123", "us-east-1", "caltechauthors", "production", "203.0.113.25", "10.0.1.25"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// untagged Project/Environment render as "unknown"
	if !strings.Contains(out, "db-server") || !strings.Contains(out, "unknown") {
		t.Errorf("output missing untagged instance's %q rendering:\n%s", "unknown", out)
	}
	// a stopped instance with no assigned IPs renders "none", not blank
	if !strings.Contains(out, "none") {
		t.Errorf("output missing %q rendering for an instance with no IPs:\n%s", "none", out)
	}
}

func TestDisplayInstances_ColorEnabled_AppliesStateColor(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running"}}
	DisplayInstances(term, instances, true)

	out := buf.String()
	if !strings.Contains(out, termlib.Green) || !strings.Contains(out, termlib.Reset) {
		t.Errorf("expected a green/reset ANSI wrap around the running state, got:\n%q", out)
	}
}

func TestDisplayInstances_ColorDisabled_NoANSICodes(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running"}}
	DisplayInstances(term, instances, false)

	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("expected no ANSI escape codes with color disabled, got:\n%q", buf.String())
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

func TestDisplayKeyPairs_Empty(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	DisplayKeyPairs(term, nil)

	if !strings.Contains(buf.String(), "No key pairs found.") {
		t.Errorf("output = %q, want it to mention no key pairs found", buf.String())
	}
}

func TestDisplayKeyPairs_Populated(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	keyPairs := []inventory.KeyPair{
		{KeyName: "my-laptop-key", KeyPairID: "key-0abc123", KeyFingerprint: "aa:bb:cc", KeyType: "ed25519", Region: "us-west-1"},
	}

	DisplayKeyPairs(term, keyPairs)
	out := buf.String()

	for _, want := range []string{"my-laptop-key", "key-0abc123", "aa:bb:cc", "ed25519", "us-west-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisplayBuckets_Empty(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	DisplayBuckets(term, nil)

	if !strings.Contains(buf.String(), "No buckets found.") {
		t.Errorf("output = %q, want it to mention no buckets found", buf.String())
	}
}

func TestDisplayBuckets_Populated(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	buckets := []inventory.Bucket{
		{Name: "sql-backups.library.caltech.edu", Region: "us-west-2", StaticWebsite: false, Purpose: "backup"},
		{Name: "static-site", Region: "us-east-1", StaticWebsite: true, Purpose: "website"},
		{Name: "untagged-bucket", Region: "us-east-1", StaticWebsite: false, Purpose: ""},
	}

	DisplayBuckets(term, buckets)
	out := buf.String()

	for _, want := range []string{"sql-backups.library.caltech.edu", "us-west-2", "backup", "static-site", "yes", "website", "untagged-bucket"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
