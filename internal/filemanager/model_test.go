package filemanager

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// key builds a tea.KeyMsg for a single printable rune, matching what a
// real terminal sends for e.g. "q" or "x".
func key(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func typeKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func waitFor(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(want))
	}, teatest.WithDuration(2*time.Second))
}

// waitForAll is like waitFor but requires every substring in a single
// drained read -- teatest's WaitFor drains tm.Output() on each poll, and
// bubbletea's renderer only retransmits screen lines that changed since
// the last frame, so two separate waitFor calls can each see a *disjoint*
// slice of a frame that (unchanged) already contained both strings.
// Checking them together against one drained read avoids that race.
func waitForAll(t *testing.T, tm *teatest.TestModel, want ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, w := range want {
			if !bytes.Contains(b, []byte(w)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(2*time.Second))
}

func quit(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestModel_SinglePane_ListsFoldersBeforeFiles(t *testing.T) {
	fake := newFakeS3("logs/2026-01-01.txt", "readme.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitForAll(t, tm, "readme.txt", "logs/")
	quit(t, tm)
}

func TestModel_SinglePane_NavigateIntoAndOutOfFolder(t *testing.T) {
	fake := newFakeS3("logs/2026-01-01.txt", "readme.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "logs/") // cursor starts on the first (dir) row

	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "2026-01-01.txt")

	tm.Send(typeKey(tea.KeyBackspace))
	waitFor(t, tm, "readme.txt")
	quit(t, tm)
}

func TestModel_Tagging_ShowsCountAndSizeInStatusLine(t *testing.T) {
	fake := newFakeS3("a.txt", "b.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(typeKey(tea.KeySpace))
	waitFor(t, tm, "1 tagged")
	quit(t, tm)
}

func TestModel_Filter_NarrowsCurrentLevel(t *testing.T) {
	fake := newFakeS3("apple.txt", "banana.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "banana.txt")
	tm.Send(key('/'))
	for _, r := range "apple" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))

	// The status line ("1 item(s) ... filter: apple") is a more reliable
	// assertion than diffing the two file rows themselves: bubbletea's
	// renderer only retransmits screen lines that changed, so an
	// unaffected row's text can be genuinely absent from a later
	// drained read even though it's still on screen.
	waitForAll(t, tm, "1 item(s)", "filter: apple")
	quit(t, tm)
}

// TestModel_Filter_EnteringShowsAnchorHelpText covers the requested
// discoverability fix: an operator shouldn't have to find the "^"/"/"
// anchor convention by trial, and shouldn't assume it means an actual
// filesystem/S3-key path.
func TestModel_Filter_EnteringShowsAnchorHelpText(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key('f'))
	waitForAll(t, tm, "not a filesystem path", "^index.html")
	tm.Send(typeKey(tea.KeyEsc)) // exit filter-edit mode before quitting, or 'q' just types a literal q
	quit(t, tm)
}

// TestModel_Find_EmptyUsageMentionsAnchorConvention covers the same
// discoverability fix for :find's usage hint.
func TestModel_Find_EmptyUsageMentionsAnchorConvention(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key(':'))
	for _, r := range "find" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitForAll(t, tm, "not a filesystem path", "^index.html")
	quit(t, tm)
}

func TestModel_Delete_RequiresExactDestructiveConfirm(t *testing.T) {
	fake := newFakeS3("doomed.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "doomed.txt")
	tm.Send(typeKey(tea.KeySpace)) // tag it
	tm.Send(key('x'))
	waitFor(t, tm, "test-bucket") // ConfirmDestructive prompt names the bucket

	// Wrong identifier cancels rather than deleting.
	for _, r := range "wrong" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "Cancelled")
	if len(fake.deleteObjectCalls) != 0 {
		t.Fatalf("DeleteObject called after a mismatched confirm: %v", fake.deleteObjectCalls)
	}
	quit(t, tm)
}

func TestModel_Delete_CorrectConfirmDeletesTaggedObject(t *testing.T) {
	fake := newFakeS3("doomed.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "doomed.txt")
	tm.Send(typeKey(tea.KeySpace))
	tm.Send(key('x'))
	waitFor(t, tm, "test-bucket")

	for _, r := range "test-bucket" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "Deleted 1 object")

	tm.Send(typeKey(tea.KeyEnter)) // dismiss the finished overlay
	waitFor(t, tm, "empty")

	if len(fake.deleteObjectCalls) != 1 || fake.deleteObjectCalls[0] != "doomed.txt" {
		t.Fatalf("deleteObjectCalls = %v, want [doomed.txt]", fake.deleteObjectCalls)
	}
	quit(t, tm)
}

// TestModel_Delete_FromFindResultsRefreshesDisplay is a regression test
// for a reported bug: deleting tagged objects found via :find/F still
// showed them present afterward. Root cause: the post-delete reload
// correctly refetched the pane's underlying listing, but pane.visible()
// shows the Find snapshot (pane.find.results) whenever a Find is
// active, so the refreshed data never made it to the screen until the
// stale Find view was also cleared.
func TestModel_Delete_FromFindResultsRefreshesDisplay(t *testing.T) {
	fake := newFakeS3("a/doomed.jsonl", "a/keep.jsonl")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	// Navigate into a/ first so the post-delete refresh (which reloads
	// whatever prefix the pane is currently sitting at) lands somewhere
	// that would visibly show keep.jsonl -- Find doesn't change
	// pane.prefix, it only searches recursively from wherever the pane
	// already is.
	waitFor(t, tm, "a/")
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "keep.jsonl")

	tm.Send(key(':'))
	for _, r := range "find *.jsonl" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitForAll(t, tm, "doomed.jsonl", "keep.jsonl")

	tm.Send(typeKey(tea.KeySpace)) // tag the first match (doomed.jsonl, alphabetically first)
	tm.Send(key('x'))
	// mustMatch is the active prefix ("a/") here, not the bucket name,
	// since navigating into a/ set pane.prefix.
	waitFor(t, tm, `"a/"`)
	for _, r := range "a/" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "Deleted 1 object")
	tm.Send(typeKey(tea.KeyEnter)) // dismiss the finished overlay
	waitFor(t, tm, "keep.jsonl")   // the reloaded a/ listing has landed

	// Inspect the Model directly (teatest shares the same pointer) --
	// more reliable than screen-text assertions for "is no longer
	// present," since bubbletea's diffed rendering can make an
	// unchanged-but-still-onscreen line genuinely absent from a later
	// drained read.
	if m.remote.find != nil {
		t.Fatal("expected the stale Find view to be cleared after the post-delete refresh")
	}
	for _, e := range m.remote.entries {
		if e.name == "doomed.jsonl" {
			t.Fatalf("doomed.jsonl still present in the refreshed listing: %+v", m.remote.entries)
		}
	}

	if len(fake.deleteObjectCalls) != 1 || fake.deleteObjectCalls[0] != "a/doomed.jsonl" {
		t.Fatalf("deleteObjectCalls = %v, want [a/doomed.jsonl]", fake.deleteObjectCalls)
	}
	quit(t, tm)
}

// TestModel_Refresh_HotkeyReloadsFocusedPane covers the explicit manual
// refresh added directly in response to "how do I get the window to
// update" -- reloads even if nothing about navigation changed.
func TestModel_Refresh_HotkeyReloadsFocusedPane(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a.txt")
	fake.objects["b.txt"] = []byte("new, added after the initial listing")
	tm.Send(key('r'))
	// "a.txt" is deliberately not re-asserted: it's an unchanged row,
	// and bubbletea's diffed renderer may not retransmit it in this
	// frame even though it's still correctly on screen (DECISIONS.md).
	waitForAll(t, tm, "b.txt", "2 item(s)")
	quit(t, tm)
}

// TestModel_Refresh_ColonCommand covers the ':refresh' alias.
func TestModel_Refresh_ColonCommand(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a.txt")
	fake.objects["b.txt"] = []byte("new, added after the initial listing")
	tm.Send(key(':'))
	for _, r := range "refresh" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitForAll(t, tm, "b.txt", "2 item(s)")
	quit(t, tm)
}

func TestModel_DoublePane_Download(t *testing.T) {
	fake := newFakeS3("report.txt")
	destDir := t.TempDir()
	m := New(context.Background(), fake, "test-bucket", "us-west-2", destDir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "report.txt")
	// Focus starts on the remote pane (New sets focus: sideRemote), which
	// is exactly where Download's source objects live.
	tm.Send(typeKey(tea.KeySpace))
	tm.Send(key('d'))
	waitFor(t, tm, "Download report.txt")
	tm.Send(key('y'))
	waitFor(t, tm, "Downloaded 1 object")
	tm.Send(typeKey(tea.KeyEnter))

	content, err := os.ReadFile(filepath.Join(destDir, "report.txt"))
	if err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	if string(content) != "content of report.txt" {
		t.Fatalf("downloaded content = %q", content)
	}
	quit(t, tm)
}

func TestModel_DoublePane_Upload(t *testing.T) {
	fake := newFakeS3()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "local.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), fake, "test-bucket", "us-west-2", srcDir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "local.txt")
	tm.Send(typeKey(tea.KeyTab)) // focus starts on remote; Upload's source is the local pane
	tm.Send(typeKey(tea.KeySpace))
	tm.Send(key('u'))
	waitFor(t, tm, "local.txt to test-bucket") // names the file directly now (single item)
	tm.Send(key('y'))
	waitFor(t, tm, "Uploaded 1 file")
	tm.Send(typeKey(tea.KeyEnter)) // dismiss the finished overlay before quitting

	if len(fake.putObjectCalls) != 1 || fake.putObjectCalls[0] != "local.txt" {
		t.Fatalf("putObjectCalls = %v, want [local.txt]", fake.putObjectCalls)
	}
	quit(t, tm)
}

// TestModel_Unlink_LHotkeyGoesStraightToConfirm covers the requested
// discoverability fix: `l` while a directory is linked should offer a
// direct, obvious way back to single-pane, not require clearing an
// edit field and submitting it empty.
func TestModel_Unlink_LHotkeyGoesStraightToConfirm(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", t.TempDir())
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key('l'))
	waitForAll(t, tm, "Unlink", "return to single-pane")
	tm.Send(key('y'))
	waitFor(t, tm, "a.txt") // back to single-pane, remote listing still shown
	quit(t, tm)
}

// TestModel_Unlink_DeclineStaysLinked covers declining the confirm.
func TestModel_Unlink_DeclineStaysLinked(t *testing.T) {
	fake := newFakeS3("a.txt")
	dir := t.TempDir()
	m := New(context.Background(), fake, "test-bucket", "us-west-2", dir)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key('l'))
	waitForAll(t, tm, "Unlink", "return to single-pane")
	tm.Send(key('n'))
	waitFor(t, tm, "LOCAL:") // still double-pane -- local header still shown
	quit(t, tm)
}

// TestModel_Unlink_ColonCommand covers the ':unlink' alias.
func TestModel_Unlink_ColonCommand(t *testing.T) {
	fake := newFakeS3("a.txt")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", t.TempDir())
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 40))

	waitFor(t, tm, "a.txt")
	tm.Send(key(':'))
	for _, r := range "unlink" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitForAll(t, tm, "Unlink", "return to single-pane")
	tm.Send(key('y'))
	waitFor(t, tm, "a.txt")
	quit(t, tm)
}

func TestModel_Find_MatchesGlobRecursively(t *testing.T) {
	fake := newFakeS3("a/b/target.go", "a/other.txt", "target.go")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "a/")
	tm.Send(key(':'))
	for _, r := range "find *.go" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "2 matched")
	quit(t, tm)
}

// TestModel_Find_AnchoredPatternMatchesOnlyBucketRoot exercises the
// "/pattern" anchored form end to end: an operator looking for the
// root-level index.html shouldn't also get every nested index.html.
func TestModel_Find_AnchoredPatternMatchesOnlyBucketRoot(t *testing.T) {
	fake := newFakeS3("index.html", "sub/index.html")
	m := New(context.Background(), fake, "test-bucket", "us-west-2", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	waitFor(t, tm, "index.html")
	tm.Send(key(':'))
	for _, r := range "find /index.html" {
		tm.Send(key(r))
	}
	tm.Send(typeKey(tea.KeyEnter))
	waitFor(t, tm, "1 matched")
	quit(t, tm)
}
