package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModel_Remote_UpFromNestedPrefixReachesRootWithSiblingsVisible is a
// regression test for parentOfS3Prefix: navigating up from a nested
// prefix used to strip the trailing slash ("logs/sub/" -> "logs"
// instead of "logs/"), which turned the next level's s3:ListObjectsV2
// call into a bare string-prefix match instead of a directory-boundary
// one -- silently hiding every bucket-root object that didn't happen to
// start with the literal string "logs".
func TestModel_Remote_UpFromNestedPrefixReachesRootWithSiblingsVisible(t *testing.T) {
	fake := newFakeS3("logs/sub/deep.txt", "logs/a.txt", "readme.txt")
	m := New(context.Background(), fake, "bucket", "us-west-2", "")
	drainCmd(m, m.Init())

	descend := func(name string) {
		for i, e := range m.remote.visible() {
			if e.name == name {
				m.remote.cursor = i
				break
			}
		}
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		drainCmd(m, cmd)
	}
	descend("logs")
	descend("sub")
	if m.remote.prefix != "logs/sub/" {
		t.Fatalf("prefix after descending = %q, want %q", m.remote.prefix, "logs/sub/")
	}

	// Up twice: logs/sub/ -> logs/ -> "" (bucket root).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	drainCmd(m, cmd)
	if m.remote.prefix != "logs/" {
		t.Fatalf("prefix after one Up = %q, want %q", m.remote.prefix, "logs/")
	}
	names := map[string]bool{}
	for _, e := range m.remote.visible() {
		names[e.name] = true
	}
	if !names["a.txt"] {
		t.Errorf("logs/ listing = %v, want a.txt visible", names)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	drainCmd(m, cmd)
	if m.remote.prefix != "" {
		t.Fatalf("prefix after two Ups = %q, want bucket root (\"\")", m.remote.prefix)
	}
	names = map[string]bool{}
	for _, e := range m.remote.visible() {
		names[e.name] = true
	}
	if !names["readme.txt"] || !names["logs"] {
		t.Errorf("bucket-root listing = %v, want readme.txt and logs/ both visible", names)
	}
}

// TestModel_Local_DescendTwoLevelsThenBackToRoot is a regression test
// for the local pane's prefix bug: entering a subdirectory used to
// assign the entry's *absolute* filesystem path directly to
// pane.prefix, which is documented (and used by loadLocalCmd via
// joinKey(root, prefix)) as root-relative -- breaking any navigation
// beyond one level deep, and specifically getting back to the linked
// root.
func TestModel_Local_DescendTwoLevelsThenBackToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "deep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := newFakeS3()
	m := New(context.Background(), fake, "bucket", "us-west-2", root)
	drainCmd(m, m.Init())
	m.focus = sideLocal

	descend := func(name string) {
		for i, e := range m.local.visible() {
			if e.name == name {
				m.local.cursor = i
				break
			}
		}
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		drainCmd(m, cmd)
	}
	descend("a")
	if m.local.prefix != "a" {
		t.Fatalf("prefix after descending into a = %q, want %q (root-relative, no leading slash)", m.local.prefix, "a")
	}
	descend("b")
	if m.local.prefix != "a/b" {
		t.Fatalf("prefix after descending into a/b = %q, want %q", m.local.prefix, "a/b")
	}
	names := map[string]bool{}
	for _, e := range m.local.visible() {
		names[e.name] = true
	}
	if !names["deep.txt"] {
		t.Fatalf("a/b listing = %v, want deep.txt visible", names)
	}

	// Up twice: a/b -> a -> "" (linked root).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	drainCmd(m, cmd)
	if m.local.prefix != "a" {
		t.Fatalf("prefix after one Up = %q, want %q", m.local.prefix, "a")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	drainCmd(m, cmd)
	if m.local.prefix != "" {
		t.Fatalf("prefix after two Ups = %q, want the linked root (\"\")", m.local.prefix)
	}
	names = map[string]bool{}
	for _, e := range m.local.visible() {
		names[e.name] = true
	}
	if !names["top.txt"] || !names["a"] {
		t.Errorf("root listing = %v, want top.txt and a/ both visible", names)
	}
}
