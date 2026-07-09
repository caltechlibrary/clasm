package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func writeSyncFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
}

func TestModel_Sync_NothingToSyncShowsStatusOnly(t *testing.T) {
	dir := t.TempDir()
	writeSyncFixture(t, dir, "a.txt", "hello") // 5 bytes, matches remote

	fake := newFakeS3("a.txt")
	fake.objects["a.txt"] = []byte("hello") // match the local file's size exactly
	m := New(context.Background(), fake, "test-bucket", "us-west-2", dir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key('S'))
	waitFor(t, tm, "Nothing to sync")
	quit(t, tm)
}

func TestModel_Sync_UploadThenSeparateDeleteConfirm(t *testing.T) {
	dir := t.TempDir()
	writeSyncFixture(t, dir, "new.txt", "0123456789") // 10 bytes, new locally

	fake := newFakeS3("gone.txt") // bucket-only, no local counterpart
	m := New(context.Background(), fake, "test-bucket", "us-west-2", dir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "new.txt")
	tm.Send(key('S'))
	waitFor(t, tm, "Sync: upload new.txt")

	tm.Send(key('y'))
	waitFor(t, tm, "Uploaded 1 file")
	tm.Send(typeKey(tea.KeyEnter)) // dismiss upload progress -> advances to delete confirm

	waitFor(t, tm, "Sync: permanently delete gone.txt")
	for _, r := range "test-bucket" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "Deleted 1 object")
	tm.Send(typeKey(tea.KeyEnter)) // dismiss delete progress

	if _, ok := fake.objects["new.txt"]; !ok {
		t.Error("new.txt was not uploaded")
	}
	if _, ok := fake.objects["gone.txt"]; ok {
		t.Error("gone.txt was not deleted")
	}
	quit(t, tm)
}

func TestModel_Sync_DeleteOnlyCandidatesSkipsUploadStage(t *testing.T) {
	dir := t.TempDir()
	writeSyncFixture(t, dir, "kept.txt", "same")

	fake := newFakeS3("kept.txt", "locally-deleted.txt")
	fake.objects["kept.txt"] = []byte("same")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", dir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "kept.txt")
	tm.Send(key('S'))
	// No upload candidates (kept.txt matches by size) -- should go
	// straight to the delete confirm, no "Sync: upload" prompt shown.
	waitFor(t, tm, "Sync: permanently delete locally-deleted.txt")

	for _, r := range "test-bucket" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "Deleted 1 object")
	tm.Send(typeKey(tea.KeyEnter)) // dismiss the finished overlay before quitting

	if len(fake.putObjectCalls) != 0 {
		t.Errorf("putObjectCalls = %v, want none (no upload candidates)", fake.putObjectCalls)
	}
	if _, ok := fake.objects["locally-deleted.txt"]; ok {
		t.Error("locally-deleted.txt was not deleted")
	}
	quit(t, tm)
}

func TestModel_Sync_DeclineUploadNeverReachesDeleteConfirm(t *testing.T) {
	dir := t.TempDir()
	writeSyncFixture(t, dir, "new.txt", "new content")

	fake := newFakeS3("gone.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", dir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "new.txt")
	tm.Send(key('S'))
	waitFor(t, tm, "Sync: upload new.txt")
	tm.Send(key('n'))

	if len(fake.deleteObjectCalls) != 0 {
		t.Errorf("deleteObjectCalls = %v, want none -- declining upload must abort before any delete prompt", fake.deleteObjectCalls)
	}
	if _, ok := fake.objects["gone.txt"]; !ok {
		t.Error("gone.txt should still be present -- delete confirm was never reached")
	}
	quit(t, tm)
}

func TestModel_Sync_RequiresLinkedDirectory(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key('S'))
	waitFor(t, tm, "Sync requires a linked local directory")
	quit(t, tm)
}
