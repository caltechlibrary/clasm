package workflow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportCloudInit_SkipsOnBlankPath(t *testing.T) {
	var buf bytes.Buffer
	if err := exportCloudInit(&buf, "#cloud-config", newHuhAccessibleInput("\n"), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportCloudInit_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exported.yaml")
	var buf bytes.Buffer

	if err := exportCloudInit(&buf, "#cloud-config\npackages: [docker]", newHuhAccessibleInput(path+"\n"), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if string(data) != "#cloud-config\npackages: [docker]" {
		t.Errorf("file content = %q, want the cloud-init text", string(data))
	}
	if !strings.Contains(buf.String(), "Saved") {
		t.Errorf("expected a saved confirmation in output, got:\n%s", buf.String())
	}
}

func TestExportCloudInit_ReportsWriteError(t *testing.T) {
	// A path inside a non-existent directory should fail to write.
	path := filepath.Join(t.TempDir(), "no-such-dir", "exported.yaml")
	var buf bytes.Buffer

	err := exportCloudInit(&buf, "#cloud-config", newHuhAccessibleInput(path+"\n"), &buf)
	if err == nil {
		t.Fatal("expected a write error for a path in a non-existent directory")
	}
}
