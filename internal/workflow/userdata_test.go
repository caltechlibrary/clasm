package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUserData_Empty(t *testing.T) {
	term, _, _ := newPipeEditor(t, "")
	got, err := loadUserData(term, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLoadUserData_Inline(t *testing.T) {
	term, _, _ := newPipeEditor(t, "")
	got, err := loadUserData(term, "#cloud-config\npackages: [git]")
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

	term, _, _ := newPipeEditor(t, "")
	got, err := loadUserData(term, "@"+path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadUserData_FileNotFound(t *testing.T) {
	term, _, _ := newPipeEditor(t, "")
	_, err := loadUserData(term, "@/no/such/file-really-does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected an error for a missing user-data file")
	}
}

func TestLoadUserData_BareExistingFilePathIsAutoDetected(t *testing.T) {
	// A bare filename with no "@" prefix is never valid literal
	// cloud-init YAML -- if a file actually exists at that path, load it
	// anyway rather than launching with the filename itself as garbage
	// user-data (real-world mistake: typing "newt-machine.yaml" instead
	// of "@newt-machine.yaml").
	dir := t.TempDir()
	path := filepath.Join(dir, "newt-machine.yaml")
	want := "#cloud-config\npackages: [newt]\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}

	term, _, buf := newPipeEditor(t, "")
	got, err := loadUserData(term, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(buf.String(), "looks like an existing file") {
		t.Errorf("expected an explanatory note in output, got:\n%s", buf.String())
	}
}

func TestLoadUserData_BareNonExistentPathStaysLiteral(t *testing.T) {
	term, _, _ := newPipeEditor(t, "")
	input := "definitely-does-not-exist-anywhere.yaml"
	got, err := loadUserData(term, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != input {
		t.Errorf("got %q, want the literal input %q back unchanged", got, input)
	}
}
