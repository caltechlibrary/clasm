package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.BackupArchive.LastInstanceID != "" || len(s.BackupArchive.LastDirectoryByInstance) != 0 {
		t.Errorf("got %+v, want a zero-value State", s)
	}
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clasm-state")
	if err := os.WriteFile(path, []byte("backup_archive: [this is not valid yaml\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestSaveThenLoad_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clasm-state")
	want := State{
		BackupArchive: BackupArchiveState{
			LastInstanceID: "i-1",
			LastDirectoryByInstance: map[string]string{
				"i-1": "/opt/rdm_sql_backups",
				"i-2": "/opt/newt/backups",
			},
		},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.BackupArchive.LastInstanceID != want.BackupArchive.LastInstanceID {
		t.Errorf("LastInstanceID = %q, want %q", got.BackupArchive.LastInstanceID, want.BackupArchive.LastInstanceID)
	}
	if len(got.BackupArchive.LastDirectoryByInstance) != len(want.BackupArchive.LastDirectoryByInstance) {
		t.Fatalf("LastDirectoryByInstance = %v, want %v", got.BackupArchive.LastDirectoryByInstance, want.BackupArchive.LastDirectoryByInstance)
	}
	for k, v := range want.BackupArchive.LastDirectoryByInstance {
		if got.BackupArchive.LastDirectoryByInstance[k] != v {
			t.Errorf("LastDirectoryByInstance[%q] = %q, want %q", k, got.BackupArchive.LastDirectoryByInstance[k], v)
		}
	}
}

func TestSave_OverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clasm-state")
	first := State{BackupArchive: BackupArchiveState{LastInstanceID: "i-1"}}
	second := State{BackupArchive: BackupArchiveState{LastInstanceID: "i-2"}}

	if err := Save(path, first); err != nil {
		t.Fatalf("Save (first): %v", err)
	}
	if err := Save(path, second); err != nil {
		t.Fatalf("Save (second): %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.BackupArchive.LastInstanceID != "i-2" {
		t.Errorf("LastInstanceID = %q, want %q (overwritten)", got.BackupArchive.LastInstanceID, "i-2")
	}
}

func TestDefaultPath_ReturnsHomeDirClasmState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultPath()
	want := filepath.Join(home, ".clasm_state")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
