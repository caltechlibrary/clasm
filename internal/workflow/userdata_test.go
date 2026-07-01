package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUserData_Empty(t *testing.T) {
	got, err := loadUserData("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLoadUserData_Inline(t *testing.T) {
	got, err := loadUserData("#cloud-config\npackages: [git]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "#cloud-config\npackages: [git]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadUserData_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invenio-rdm.yaml")
	want := "#cloud-config\npackages: [docker]\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}

	got, err := loadUserData("@" + path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadUserData_FileNotFound(t *testing.T) {
	_, err := loadUserData("@/no/such/file-really-does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected an error for a missing user-data file")
	}
}
