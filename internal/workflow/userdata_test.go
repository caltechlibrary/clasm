package workflow

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/ui"
)

func TestLoadUserData_Empty(t *testing.T) {
	var buf bytes.Buffer
	got, err := loadUserData(&buf, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLoadUserData_Inline(t *testing.T) {
	var buf bytes.Buffer
	got, err := loadUserData(&buf, "#cloud-config\npackages: [git]")
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

	var buf bytes.Buffer
	got, err := loadUserData(&buf, "@"+path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadUserData_FileNotFound(t *testing.T) {
	var buf bytes.Buffer
	_, err := loadUserData(&buf, "@/no/such/file-really-does-not-exist.yaml")
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

	var buf bytes.Buffer
	got, err := loadUserData(&buf, path)
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
	var buf bytes.Buffer
	input := "definitely-does-not-exist-anywhere.yaml"
	got, err := loadUserData(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != input {
		t.Errorf("got %q, want the literal input %q back unchanged", got, input)
	}
}

// promptCloudInitYAMLFile's own coverage -- moved here from
// launch_from_cloud_init_test.go when CollectLaunchInstanceParamsFrom
// CloudInit's AMI picker converted to tui.RunPicker (Picker tier,
// DESIGN.md's full conversion punch list): the cloud-init YAML prompt
// now runs entirely in the exported wrapper, before the (untestable)
// AMI pick, so it's simplest to test standalone via its own
// accessible-mode pipe rather than through the whole launch-params
// pipeline.

func TestPromptCloudInitYAMLFile_RequiresNonEmpty(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	var buf bytes.Buffer

	got, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput("\n"+path+"\n"), &buf) // blank -- rejected, retry accepted
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "#cloud-config" {
		t.Errorf("got %q, want %q", got, "#cloud-config")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the blank input, got:\n%s", buf.String())
	}
}

func TestPromptCloudInitYAMLFile_ReadsFromFile(t *testing.T) {
	want := "#cloud-config\npackages: [docker]\n"
	path := writeCloudInitFixture(t, want)
	var buf bytes.Buffer

	got, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput(path+"\n"), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPromptCloudInitYAMLFile_ToleratesLeadingAtSign(t *testing.T) {
	// Backward-compat: an operator used to Feature 2's "@file path"
	// convention shouldn't be broken by typing "@" out of habit here,
	// even though this prompt no longer requires (or supports inline
	// text as an alternative to) it.
	want := "#cloud-config\n"
	path := writeCloudInitFixture(t, want)
	var buf bytes.Buffer

	got, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput("@"+path+"\n"), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPromptCloudInitYAMLFile_BareQCancels(t *testing.T) {
	// Phase 20.45: a bare "q" is the same discoverable single-step
	// cancel every Select-based picker in this app already offers --
	// unlike those, a free-text huh.Input has no built-in Quit key, so
	// this has to be handled explicitly (see DESIGN.md, "Cloud-Init
	// Picker: Discoverable Cancel + a Second, Related Bug").
	var buf bytes.Buffer
	_, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput("q\n"), &buf)
	if !errors.Is(err, ui.ErrCancelled) {
		t.Fatalf("got error %v, want ui.ErrCancelled", err)
	}
}

func TestPromptCloudInitYAMLFile_AtQStillReadsAFileNamedQ(t *testing.T) {
	// The cancel check must fire on a bare "q" only -- an explicit "@q"
	// still means "read a file literally named q", same as any other
	// "@"-prefixed path.
	want := "#cloud-config\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "q")
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	var buf bytes.Buffer
	got, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput("@q\n"), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPromptCloudInitYAMLFile_RetriesOnUnreadableFile(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config")
	input := "/no/such/file-really-does-not-exist.yaml\n" + // rejected -- cannot read
		path + "\n" // retry, accepted
	var buf bytes.Buffer

	got, err := promptCloudInitYAMLFile(&buf, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "#cloud-config" {
		t.Errorf("got %q, want %q", got, "#cloud-config")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the unreadable file, got:\n%s", buf.String())
	}
}
