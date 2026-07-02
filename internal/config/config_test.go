package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Regions) != len(DefaultRegions) || cfg.Regions[0] != DefaultRegions[0] || cfg.Regions[1] != DefaultRegions[1] {
		t.Errorf("Regions = %v, want %v", cfg.Regions, DefaultRegions)
	}
}

func TestLoad_ValidFileSetsRegions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	content := "regions:\n  - us-east-1\n  - us-east-2\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"us-east-1", "us-east-2"}
	if len(cfg.Regions) != len(want) || cfg.Regions[0] != want[0] || cfg.Regions[1] != want[1] {
		t.Errorf("Regions = %v, want %v", cfg.Regions, want)
	}
}

func TestLoad_EmptyRegionsFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	// Valid YAML, but doesn't mention regions at all -- a config file
	// only needs to state what it's actually overriding.
	content := "# no settings yet\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Regions) != len(DefaultRegions) || cfg.Regions[0] != DefaultRegions[0] {
		t.Errorf("Regions = %v, want default %v", cfg.Regions, DefaultRegions)
	}
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	content := "regions: [this is not valid yaml\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestDefaultPath_ReturnsHomeDirAwsops(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultPath()
	want := filepath.Join(home, ".awsops")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
