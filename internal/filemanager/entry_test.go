package filemanager

import "testing"

func TestSortEntries_FoldersBeforeFilesThenAlphabetical(t *testing.T) {
	entries := []entry{
		{name: "zeta.txt", kind: kindFile},
		{name: "logs", kind: kindDir},
		{name: "alpha.txt", kind: kindFile},
		{name: "backups", kind: kindDir},
	}
	sortEntries(entries)

	want := []string{"backups", "logs", "alpha.txt", "zeta.txt"}
	for i, w := range want {
		if entries[i].name != w {
			t.Fatalf("entries[%d].name = %q, want %q (full: %v)", i, entries[i].name, w, entries)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestJoinKeyParentOfBaseOf(t *testing.T) {
	if got := joinKey("", "a"); got != "a" {
		t.Errorf("joinKey(%q, %q) = %q, want %q", "", "a", got, "a")
	}
	if got := joinKey("a/b", "c"); got != "a/b/c" {
		t.Errorf("joinKey(a/b, c) = %q, want a/b/c", got)
	}
	if got := parentOfLocal("a/b/c"); got != "a/b" {
		t.Errorf("parentOfLocal(a/b/c) = %q, want a/b", got)
	}
	if got := parentOfLocal("a"); got != "" {
		t.Errorf("parentOfLocal(a) = %q, want empty", got)
	}
	if got := parentOfS3Prefix("logs/sub/file.txt"); got != "logs/sub/" {
		t.Errorf("parentOfS3Prefix(logs/sub/file.txt) = %q, want logs/sub/", got)
	}
	if got := parentOfS3Prefix("logs/sub/"); got != "logs/" {
		t.Errorf("parentOfS3Prefix(logs/sub/) = %q, want logs/", got)
	}
	if got := parentOfS3Prefix("logs/a.txt"); got != "logs/" {
		t.Errorf("parentOfS3Prefix(logs/a.txt) = %q, want logs/", got)
	}
	if got := parentOfS3Prefix("a.txt"); got != "" {
		t.Errorf("parentOfS3Prefix(a.txt) = %q, want empty", got)
	}
	if got := baseOf("a/b/c"); got != "c" {
		t.Errorf("baseOf(a/b/c) = %q, want c", got)
	}
	if got := baseOf("a/b/"); got != "b" {
		t.Errorf("baseOf(a/b/) = %q, want b", got)
	}
}

func TestGlobMatch_BasenameShellGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "a/b/main.go", true},
		{"*.go", "main.go.bak", false},
		{`\.git`, ".git", true},
		{`\.git`, "gitignore", false},
		{"db0*", "a/b/db0-backup.sql", true},
		{"db0*", "a/b/other.sql", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// TestGlobMatch_AnchoredPattern covers the "^" (primary, regex-
// convention) and "/" (alias) prefixes that anchor a pattern to the
// search's root and match the whole path instead of just the
// basename -- added so a root-level file (e.g. index.html) can be
// found without also matching same-named files in subdirectories.
func TestGlobMatch_AnchoredPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"^index.html", "index.html", true},
		{"^index.html", "sub/index.html", false},
		{"^sub/index.html", "sub/index.html", true},
		{"^sub/index.html", "other/index.html", false},
		{"^*.html", "index.html", true},
		{"^*.html", "sub/index.html", false},
		// "/" is kept working as an alias for "^".
		{"/index.html", "index.html", true},
		{"/index.html", "sub/index.html", false},
		{"/sub/index.html", "sub/index.html", true},
		{"/sub/index.html", "other/index.html", false},
		{"/*.html", "index.html", true},
		{"/*.html", "sub/index.html", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(entry{name: "a", kind: kindFile}); got != "a" {
		t.Errorf("displayName(file) = %q, want %q", got, "a")
	}
	if got := displayName(entry{name: "a", kind: kindDir}); got != "a/" {
		t.Errorf("displayName(dir) = %q, want %q", got, "a/")
	}
}
