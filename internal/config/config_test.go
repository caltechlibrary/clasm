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

func TestSave_RoundTripsThroughLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clasm-config")
	cfg := Config{
		Regions:           []string{"us-east-1", "us-east-2"},
		BackupDirectories: []BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}},
		OriginTag:         OriginTagConfig{Key: "Origin", DLDValue: "DLD"},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error loading saved config: %v", err)
	}
	if len(got.Regions) != 2 || got.Regions[0] != "us-east-1" || got.Regions[1] != "us-east-2" {
		t.Errorf("Regions = %v, want %v", got.Regions, cfg.Regions)
	}
	if len(got.BackupDirectories) != 1 || got.BackupDirectories[0] != cfg.BackupDirectories[0] {
		t.Errorf("BackupDirectories = %v, want %v", got.BackupDirectories, cfg.BackupDirectories)
	}
	if got.OriginTag != cfg.OriginTag {
		t.Errorf("OriginTag = %v, want %v", got.OriginTag, cfg.OriginTag)
	}
}

func TestSave_WritesReadablePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clasm-config")
	if err := Save(path, Config{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("unexpected error stat-ing saved file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("permissions = %v, want 0644", info.Mode().Perm())
	}
}

func TestSave_UnwritablePathErrors(t *testing.T) {
	// A path inside a nonexistent directory can never be created by
	// os.WriteFile -- confirms Save surfaces the error rather than
	// silently swallowing it.
	path := filepath.Join(t.TempDir(), "does-not-exist", "clasm-config")
	if err := Save(path, Config{}); err == nil {
		t.Fatal("expected an error for an unwritable path")
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
