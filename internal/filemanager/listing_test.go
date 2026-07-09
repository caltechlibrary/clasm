package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListS3Level_GroupsCommonPrefixesAndSkipsSelf(t *testing.T) {
	fake := newFakeS3("logs/", "logs/a.txt", "logs/sub/b.txt", "readme.txt")
	got, err := listS3Level(context.Background(), fake, "bucket", "")
	if err != nil {
		t.Fatalf("listS3Level: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (logs/, readme.txt): %v", len(got), got)
	}
	if got[0].kind != kindDir || got[0].key != "logs/" {
		t.Errorf("entries[0] = %+v, want dir logs/", got[0])
	}
	if got[1].kind != kindFile || got[1].key != "readme.txt" {
		t.Errorf("entries[1] = %+v, want file readme.txt", got[1])
	}
}

func TestListS3Level_DescendsIntoPrefix(t *testing.T) {
	fake := newFakeS3("logs/a.txt", "logs/sub/b.txt")
	got, err := listS3Level(context.Background(), fake, "bucket", "logs/")
	if err != nil {
		t.Fatalf("listS3Level: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (a.txt, sub/): %v", len(got), got)
	}
}

func TestListS3Recursive_ListsEveryObjectUnderPrefix(t *testing.T) {
	fake := newFakeS3("logs/a.txt", "logs/sub/b.txt", "readme.txt")
	got, err := listS3Recursive(context.Background(), fake, "bucket", "logs/")
	if err != nil {
		t.Fatalf("listS3Recursive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (readme.txt excluded): %v", len(got), got)
	}
}

func TestListLocalLevel_GroupsDirsBeforeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := listLocalLevel(dir)
	if err != nil {
		t.Fatalf("listLocalLevel: %v", err)
	}
	if len(got) != 2 || got[0].kind != kindDir || got[0].name != "sub" {
		t.Fatalf("got %v, want [sub(dir), a.txt(file)]", got)
	}
}

func TestListLocalRecursive_WalksSubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := listLocalRecursive(dir)
	if err != nil {
		t.Fatalf("listLocalRecursive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
}

func TestValidateLocalDir(t *testing.T) {
	dir := t.TempDir()
	if err := validateLocalDir(dir); err != nil {
		t.Errorf("validateLocalDir(%q) = %v, want nil", dir, err)
	}

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalDir(file); err == nil {
		t.Error("validateLocalDir on a plain file: want error, got nil")
	}
	if err := validateLocalDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("validateLocalDir on a missing path: want error, got nil")
	}
}
