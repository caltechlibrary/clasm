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

func TestLoad_ParsesBackupDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	content := "backup_directories:\n" +
		"  - pattern: \"rdm-*\"\n" +
		"    directory: /opt/rdm_sql_backups\n" +
		"  - pattern: \"newt-*\"\n" +
		"    directory: /opt/newt/backups\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
		{Pattern: "newt-*", Directory: "/opt/newt/backups"},
	}
	if len(cfg.BackupDirectories) != len(want) || cfg.BackupDirectories[0] != want[0] || cfg.BackupDirectories[1] != want[1] {
		t.Errorf("BackupDirectories = %v, want %v", cfg.BackupDirectories, want)
	}
}

func TestLoad_MissingFileReturnsDefaultOriginTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OriginTag.Key != DefaultOriginTagKey {
		t.Errorf("OriginTag.Key = %q, want default %q", cfg.OriginTag.Key, DefaultOriginTagKey)
	}
	if cfg.OriginTag.DLDValue != "" {
		t.Errorf("OriginTag.DLDValue = %q, want empty (unset) until the operator configures a real vocabulary", cfg.OriginTag.DLDValue)
	}
}

func TestLoad_ParsesOriginTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	content := "origin_tag:\n  key: \"Owner\"\n  dld_value: \"DLD\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OriginTag.Key != "Owner" {
		t.Errorf("OriginTag.Key = %q, want %q", cfg.OriginTag.Key, "Owner")
	}
	if cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag.DLDValue = %q, want %q", cfg.OriginTag.DLDValue, "DLD")
	}
}

func TestLoad_OriginTagKeyEmptyFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsops-config")
	// dld_value set, but key deliberately omitted -- each field defaults
	// independently, matching Regions/BackupDirectories' existing
	// per-field-default behavior, not all-or-nothing.
	content := "origin_tag:\n  dld_value: \"DLD\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OriginTag.Key != DefaultOriginTagKey {
		t.Errorf("OriginTag.Key = %q, want default %q", cfg.OriginTag.Key, DefaultOriginTagKey)
	}
	if cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag.DLDValue = %q, want %q", cfg.OriginTag.DLDValue, "DLD")
	}
}

func TestBackupDirectoryFor_MatchesGlobPattern(t *testing.T) {
	rules := []BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	got := BackupDirectoryFor(rules, "rdm-prod-01")
	if got != "/opt/rdm_sql_backups" {
		t.Errorf("got %q, want %q", got, "/opt/rdm_sql_backups")
	}
}

func TestBackupDirectoryFor_FirstMatchWins(t *testing.T) {
	rules := []BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
		{Pattern: "*", Directory: "/opt/catch-all"},
	}
	got := BackupDirectoryFor(rules, "rdm-prod-01")
	if got != "/opt/rdm_sql_backups" {
		t.Errorf("got %q, want the first matching rule's directory %q", got, "/opt/rdm_sql_backups")
	}
}

func TestBackupDirectoryFor_NoMatchReturnsEmpty(t *testing.T) {
	rules := []BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
	}
	got := BackupDirectoryFor(rules, "newt-machine-test")
	if got != "" {
		t.Errorf("got %q, want empty string for no match", got)
	}
}

func TestBackupDirectoryFor_EmptyNameReturnsEmpty(t *testing.T) {
	rules := []BackupDirectoryRule{
		{Pattern: "*", Directory: "/opt/catch-all"},
	}
	got := BackupDirectoryFor(rules, "")
	if got != "" {
		t.Errorf("got %q, want empty string for an untagged (nameless) instance", got)
	}
}

func TestDefaultPath_ReturnsHomeDirClasm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultPath()
	want := filepath.Join(home, ".clasm")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
