package workflow

import (
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/config"
)

func TestEditRegions_AddsARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1"}}
	_, input, buf := newPipeEditor("1\nus-east-1\n3\n") // Add a region, "us-east-1", Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.Regions) != 2 || cfg.Regions[1] != "us-east-1" {
		t.Errorf("Regions = %v, want [us-west-1 us-east-1]", cfg.Regions)
	}
	_ = buf
}

func TestEditRegions_RemovesARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1", "us-west-2"}}
	_, input, buf := newPipeEditor("2\n2\n3\n") // Remove a region, pick "us-west-2", Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.Regions) != 1 || cfg.Regions[0] != "us-west-1" {
		t.Errorf("Regions = %v, want [us-west-1]", cfg.Regions)
	}
}

func TestEditRegions_RemoveWithNoRegionsMessageThenDone(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("2\n3\n") // Remove a region (none), Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if !strings.Contains(buf.String(), "No regions to remove") {
		t.Errorf("expected a no-regions-to-remove message, got:\n%s", buf.String())
	}
}

func TestEditRegions_BlankRegionNotAdded(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\n\n3\n") // Add a region, blank, Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if len(cfg.Regions) != 0 {
		t.Errorf("Regions = %v, want empty", cfg.Regions)
	}
}

func TestEditBackupDirectoryRules_AddsARule(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\nrdm-*\n/opt/rdm_sql_backups\n3\n") // Add, pattern, directory, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.BackupDirectories) != 1 || cfg.BackupDirectories[0].Pattern != "rdm-*" || cfg.BackupDirectories[0].Directory != "/opt/rdm_sql_backups" {
		t.Errorf("BackupDirectories = %+v", cfg.BackupDirectories)
	}
}

func TestEditBackupDirectoryRules_RemovesARule(t *testing.T) {
	cfg := config.Config{BackupDirectories: []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
		{Pattern: "newt-*", Directory: "/opt/newt/backups"},
	}}
	_, input, buf := newPipeEditor("2\n1\n3\n") // Remove, pick first rule, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.BackupDirectories) != 1 || cfg.BackupDirectories[0].Pattern != "newt-*" {
		t.Errorf("BackupDirectories = %+v, want only the newt-* rule left", cfg.BackupDirectories)
	}
}

func TestEditBackupDirectoryRules_BlankPatternNotAdded(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\n\n3\n") // Add, blank pattern, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if len(cfg.BackupDirectories) != 0 {
		t.Errorf("BackupDirectories = %+v, want empty", cfg.BackupDirectories)
	}
}

func TestEditOriginTag_UpdatesKeyAndValue(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: ""}}
	_, input, buf := newPipeEditor("Owner\nDLD\n")

	changed, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if cfg.OriginTag.Key != "Owner" || cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag = %+v, want {Owner DLD}", cfg.OriginTag)
	}
}

func TestEditOriginTag_BlankKeepsDefaults(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}}
	_, input, buf := newPipeEditor("\n\n") // accept both pre-filled defaults

	changed, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false when both values are unchanged")
	}
	if cfg.OriginTag.Key != "Origin" || cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag = %+v, want unchanged", cfg.OriginTag)
	}
}

func TestEditOriginTag_BlankKeyFallsBackToDefault(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "", DLDValue: ""}}
	_, input, buf := newPipeEditor("\n\n")

	_, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OriginTag.Key != config.DefaultOriginTagKey {
		t.Errorf("OriginTag.Key = %q, want default %q", cfg.OriginTag.Key, config.DefaultOriginTagKey)
	}
}

func TestDisplayConfig_PrintsAllFields(t *testing.T) {
	cfg := config.Config{
		Regions:           []string{"us-west-1", "us-west-2"},
		BackupDirectories: []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}},
		OriginTag:         config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"},
	}
	_, _, buf := newPipeEditor("")
	displayConfig(buf, cfg)

	out := buf.String()
	for _, want := range []string{"us-west-1", "us-west-2", "rdm-*", "/opt/rdm_sql_backups", "Origin", "DLD"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisplayConfig_UnsetOriginValueShowsPlaceholder(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: ""}}
	_, _, buf := newPipeEditor("")
	displayConfig(buf, cfg)

	if !strings.Contains(buf.String(), "none") {
		t.Errorf("expected a placeholder for an unset DLD value, got:\n%s", buf.String())
	}
}

func TestWarnIfDirtyOnQuit_WarnsWhenDirty(t *testing.T) {
	_, _, buf := newPipeEditor("")
	warnIfDirtyOnQuit(buf, true)
	if !strings.Contains(buf.String(), "discarded") {
		t.Errorf("expected an unsaved-changes warning, got:\n%s", buf.String())
	}
}

func TestWarnIfDirtyOnQuit_SilentWhenClean(t *testing.T) {
	_, _, buf := newPipeEditor("")
	warnIfDirtyOnQuit(buf, false)
	if buf.String() != "" {
		t.Errorf("expected no output when there are no unsaved changes, got:\n%s", buf.String())
	}
}
