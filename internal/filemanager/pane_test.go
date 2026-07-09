package filemanager

import "testing"

func TestPane_TagsSurviveNavigation(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.entries = []entry{{name: "a.txt", kind: kindFile, key: "a.txt", size: 10}}
	p.toggleTag() // tags a.txt under the cursor

	// Navigate away to a different prefix -- entries change, tag set
	// should not (DESIGN.md 21.6 doesn't scope tagging to "this
	// directory only"; matches mc/WinSCP convention).
	p.enter("sub/")
	p.entries = []entry{{name: "b.txt", kind: kindFile, key: "sub/b.txt", size: 20}}

	if _, ok := p.tagged["a.txt"]; !ok {
		t.Fatalf("tag on a.txt lost after navigating away: %v", p.tagged)
	}
	if got := p.taggedSize(); got != 10 {
		t.Errorf("taggedSize() = %d, want 10 (b.txt untagged)", got)
	}
}

func TestPane_TagAllVisible_RespectsActiveFilter(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.entries = []entry{
		{name: "apple.txt", kind: kindFile, key: "apple.txt"},
		{name: "banana.txt", kind: kindFile, key: "banana.txt"},
	}
	p.filter = "apple"
	p.tagAllVisible()

	if len(p.tagged) != 1 {
		t.Fatalf("tagged = %v, want exactly 1 (apple.txt)", p.tagged)
	}
	if _, ok := p.tagged["apple.txt"]; !ok {
		t.Errorf("expected apple.txt tagged, got %v", p.tagged)
	}
}

func TestPane_TaggedOrCurrent_FallsBackToCursorRow(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.entries = []entry{{name: "only.txt", kind: kindFile, key: "only.txt"}}

	got := p.taggedOrCurrent()
	if len(got) != 1 || got[0].key != "only.txt" {
		t.Fatalf("taggedOrCurrent() with nothing tagged = %v, want [only.txt]", got)
	}

	p.toggleTag()
	got = p.taggedOrCurrent()
	if len(got) != 1 || got[0].key != "only.txt" {
		t.Fatalf("taggedOrCurrent() with a tag = %v, want [only.txt]", got)
	}
}

func TestPane_Visible_FiltersCaseInsensitively(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.entries = []entry{
		{name: "Report.txt", kind: kindFile},
		{name: "notes.md", kind: kindFile},
	}
	p.filter = "REPORT"

	v := p.visible()
	if len(v) != 1 || v[0].name != "Report.txt" {
		t.Fatalf("visible() with filter %q = %v, want [Report.txt]", p.filter, v)
	}
}

// TestPane_Visible_AnchoredFilterMatchesExactBasenameOnly is a
// regression test: an operator typing "^index.html" (or the "/" alias)
// as a filter expected the same anchored behavior Find has (DESIGN.md
// 21.7), not a plain substring match against the literal marker
// character -- which never matches any basename.
func TestPane_Visible_AnchoredFilterMatchesExactBasenameOnly(t *testing.T) {
	for _, filter := range []string{"^index.html", "/index.html"} {
		p := newPane(sideRemote, "bucket")
		p.entries = []entry{
			{name: "index.html", kind: kindFile},
			{name: "myindex.html5", kind: kindFile}, // would match a plain substring filter for "index.html"
		}
		p.filter = filter

		v := p.visible()
		if len(v) != 1 || v[0].name != "index.html" {
			t.Fatalf("visible() with anchored filter %q = %v, want exactly [index.html]", p.filter, v)
		}
	}
}

func TestPane_ClampCursor_HandlesShrinkingList(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.entries = []entry{{name: "a"}, {name: "b"}, {name: "c"}}
	p.cursor = 2
	p.entries = []entry{{name: "a"}}
	p.clampCursor()

	if p.cursor != 0 {
		t.Errorf("clampCursor() left cursor at %d, want 0", p.cursor)
	}
}
