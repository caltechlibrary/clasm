package ui

import (
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// DisplayInstances/DisplayImages/DisplayKeyPairs converted to the
// shared List-tier component (tui.RunListView, DESIGN.md's full
// conversion punch list) -- like DisplayBuckets, each is now a thin
// wrapper around an interactive bubbletea loop (see
// internal/tui/listview_test.go for that component's own thorough test
// suite) and isn't itself directly unit-tested. What's specific to this
// package, and worth testing here, is each *ListViewConfig builder's
// column formatting -- a pure data transformation with no interactive
// loop to drive.

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		maxW int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello w…"},
		{"hello", 1, "…"},
		{"", 5, ""},
		{"日本語テスト", 4, "日本語…"},
	}
	for _, c := range cases {
		got := truncate(c.in, c.maxW)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.maxW, got, c.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hi", 5, "hi   "},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		{"", 3, "   "},
	}
	for _, c := range cases {
		got := padRight(c.in, c.w)
		if got != c.want {
			t.Errorf("padRight(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

func TestInstanceListViewConfig_Empty(t *testing.T) {
	cfg := instanceListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty instance list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "INSTANCE ID") || !strings.Contains(cfg.Header, "STATE") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestInstanceListViewConfig_Populated(t *testing.T) {
	instances := []inventory.Instance{
		{InstanceID: "i-012345", Name: "web-server", State: "running", ImageID: "ami-abc123", Region: "us-east-1", Project: "caltechauthors", Environment: "production", PublicIP: "203.0.113.25", PrivateIP: "10.0.1.25"},
		{InstanceID: "i-067890", Name: "db-server", State: "stopped", ImageID: "ami-def456", Region: "us-west-2"},
	}

	cfg := instanceListViewConfig(instances)
	if len(cfg.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(cfg.Rows))
	}

	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
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
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}

func TestInstanceRow_ColorEnabled_AppliesStateColor(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running"}
	row := instanceRow(inst, true)

	if !strings.Contains(row, ansiGreen) || !strings.Contains(row, ansiReset) {
		t.Errorf("expected a green/reset ANSI wrap around the running state, got:\n%q", row)
	}
}

func TestInstanceRow_ColorDisabled_NoANSICodes(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running"}
	row := instanceRow(inst, false)

	if strings.Contains(row, "\033[") {
		t.Errorf("expected no ANSI escape codes with color disabled, got:\n%q", row)
	}
}

func TestImageListViewConfig_Empty(t *testing.T) {
	cfg := imageListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty image list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "AMI ID") || !strings.Contains(cfg.Header, "REGION") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestImageListViewConfig_Populated(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-abc123", Name: "base-ubuntu-2404", CreationDate: "2026-01-15", Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{ImageID: "ami-def456", Name: "custom-ami", CreationDate: "2026-03-10", Region: "us-east-1"},
	}

	cfg := imageListViewConfig(images)
	if len(cfg.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(cfg.Rows))
	}

	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
	for _, want := range []string{"ami-abc123", "base-ubuntu-2404", "2026-01-15", "us-east-1", "caltechauthors", "production"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "custom-ami") || !strings.Contains(out, "unknown") {
		t.Errorf("output missing untagged image's %q rendering:\n%s", "unknown", out)
	}
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}

func TestKeyPairListViewConfig_Empty(t *testing.T) {
	cfg := keyPairListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty key pair list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "KEY NAME") || !strings.Contains(cfg.Header, "FINGERPRINT") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestKeyPairListViewConfig_Populated(t *testing.T) {
	keyPairs := []inventory.KeyPair{
		{KeyName: "my-laptop-key", KeyPairID: "key-0abc123", KeyFingerprint: "aa:bb:cc", KeyType: "ed25519", Region: "us-west-1"},
	}

	cfg := keyPairListViewConfig(keyPairs)
	if len(cfg.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(cfg.Rows))
	}

	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
	for _, want := range []string{"my-laptop-key", "key-0abc123", "aa:bb:cc", "ed25519", "us-west-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}

// DisplayBuckets itself is a thin wrapper around tui.RunListView (an
// interactive bubbletea loop -- see internal/tui/listview_test.go for
// that component's own thorough test suite, teatest/direct-Model-driven
// as appropriate). What's specific to this package, and worth testing
// here, is bucketListViewConfig's column formatting -- a pure data
// transformation with no interactive loop to drive.

func TestBucketListViewConfig_Empty(t *testing.T) {
	cfg := bucketListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty bucket list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "NAME") || !strings.Contains(cfg.Header, "REGION") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestBucketListViewConfig_Populated(t *testing.T) {
	buckets := []inventory.Bucket{
		{Name: "sql-backups.library.caltech.edu", Region: "us-west-2", StaticWebsite: false, Purpose: "backup"},
		{Name: "static-site", Region: "us-east-1", StaticWebsite: true, Purpose: "website"},
		{Name: "untagged-bucket", Region: "us-east-1", StaticWebsite: false, Purpose: ""},
	}

	cfg := bucketListViewConfig(buckets)
	if len(cfg.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(cfg.Rows))
	}

	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
	for _, want := range []string{"sql-backups.library.caltech.edu", "us-west-2", "backup", "static-site", "yes", "no", "website", "untagged-bucket", "NAME", "REGION", "STATIC WEBSITE", "PURPOSE"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}

func TestLaunchTemplateListViewConfig_Empty(t *testing.T) {
	cfg := launchTemplateListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty template list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "TEMPLATE ID") || !strings.Contains(cfg.Header, "REGION") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestLaunchTemplateListViewConfig_Populated(t *testing.T) {
	templates := []inventory.LaunchTemplate{
		{TemplateID: "lt-1", Name: "rdm-app", DefaultVersion: 2, LatestVersion: 3, Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{TemplateID: "lt-2", Name: "untagged", DefaultVersion: 1, LatestVersion: 1, Region: "us-west-2"},
	}

	cfg := launchTemplateListViewConfig(templates)
	if len(cfg.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(cfg.Rows))
	}

	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
	for _, want := range []string{"lt-1", "rdm-app", "us-east-1", "caltechauthors", "production", "lt-2", "untagged", "us-west-2", "unknown", "TEMPLATE ID", "DEFAULT", "LATEST", "REGION", "PROJECT", "ENVIRONMENT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}
