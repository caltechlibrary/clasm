package filemanager

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStyleRow_GatedByColorEnabled(t *testing.T) {
	row := ">* logs/"

	if got := styleRow(row, true, false, false); got != row {
		t.Errorf("styleRow with colorEnabled=false = %q, want unchanged %q", got, row)
	}
	if got := styleRow(row, false, true, false); got != row {
		t.Errorf("styleRow with colorEnabled=false = %q, want unchanged %q", got, row)
	}

	if got := styleRow(row, true, false, true); got == row || !strings.Contains(got, ansiReverse) {
		t.Errorf("styleRow(cursor, colorEnabled=true) = %q, want reverse-video applied", got)
	}
	if got := styleRow(row, false, true, true); got == row || !strings.Contains(got, ansiBold) {
		t.Errorf("styleRow(tagged, colorEnabled=true) = %q, want bold applied", got)
	}
}

// TestPaneRows_ShowsLoadingIndicatorWhileFetching covers the reported
// gap: Find/directory listing can take a noticeable amount of time
// against a real bucket, and with no feedback the screen just looked
// frozen.
func TestPaneRows_ShowsLoadingIndicatorWhileFetching(t *testing.T) {
	p := newPane(sideRemote, "bucket")

	notLoading := paneRows(p, true, false, false, "⠋", 10)
	if strings.Contains(notLoading[0], "Loading") {
		t.Errorf("header row = %q, should not mention Loading when not fetching", notLoading[0])
	}

	loading := paneRows(p, true, false, true, "⠋", 10)
	if !strings.Contains(loading[0], "Loading") || !strings.Contains(loading[0], "⠋") {
		t.Errorf("header row = %q, want it to show the spinner glyph and mention Loading", loading[0])
	}
}

// TestPaneRows_ShowsSpinnerNextToActiveFindStatus covers Find's own
// progress line getting the same spinner treatment while a search is
// still running, but not once it's finished.
func TestPaneRows_ShowsSpinnerNextToActiveFindStatus(t *testing.T) {
	p := newPane(sideRemote, "bucket")
	p.find = &findState{pattern: "*.go", scanned: 3}

	running := paneRows(p, true, false, false, "⠋", 10)
	if !strings.Contains(running[1], "⠋") {
		t.Errorf("find status row = %q, want the spinner glyph while running", running[1])
	}

	p.find.done = true
	finished := paneRows(p, true, false, false, "⠋", 10)
	if strings.Contains(finished[1], "⠋") {
		t.Errorf("find status row = %q, should not show the spinner once done", finished[1])
	}
}

func TestPadOrTruncate_ANSIStyledRowMeasuresVisibleWidthOnly(t *testing.T) {
	plain := ">* logs/"
	styled := reverseVideo(plain)

	gotPlain := padOrTruncate(plain, 20)
	gotStyled := padOrTruncate(styled, 20)

	if runeLen(gotPlain) != 20 {
		t.Fatalf("padOrTruncate(plain) visible width = %d, want 20", runeLen(gotPlain))
	}
	if runeLen(gotStyled) != 20 {
		t.Fatalf("padOrTruncate(styled) visible width = %d, want 20 (ANSI codes must not count toward width)", runeLen(gotStyled))
	}
	// Same number of trailing space runes once ANSI is stripped, i.e.
	// same visible column position for whatever follows.
	if stripANSI(gotPlain) != stripANSI(gotStyled) {
		t.Errorf("stripped forms differ: %q vs %q", stripANSI(gotPlain), stripANSI(gotStyled))
	}
}

func TestBoxRow2_BothCellsAlignRegardlessOfANSIStyling(t *testing.T) {
	leftW, rightW := 20, 15
	plainRow := boxRow2("plain left", "plain right", leftW, rightW)
	styledRow := boxRow2(reverseVideo("styled left"), bold("styled right"), leftW, rightW)

	if got, want := utf8.RuneCountInString(stripANSI(plainRow)), utf8.RuneCountInString(stripANSI(styledRow)); got != want {
		t.Fatalf("stripped rendered width differs: plain=%d styled=%d", got, want)
	}
	if !strings.HasSuffix(stripANSI(plainRow), "│\n") || !strings.HasSuffix(stripANSI(styledRow), "│\n") {
		t.Fatalf("expected both rows to end with the right border: plain=%q styled=%q", plainRow, styledRow)
	}
}

func TestJoinKey_EmptyNameReturnsParentUnchanged(t *testing.T) {
	if got := joinKey("/path/on/disk", ""); got != "/path/on/disk" {
		t.Errorf("joinKey(root, \"\") = %q, want %q (no spurious trailing slash)", got, "/path/on/disk")
	}
}

func TestPaneLabel_LocalRootHasNoTrailingSlash(t *testing.T) {
	p := newPane(sideLocal, "/path/on/disk")
	if got := p.label(); got != "LOCAL: /path/on/disk" {
		t.Errorf("label() at root = %q, want %q", got, "LOCAL: /path/on/disk")
	}
	p.prefix = "sub"
	if got := p.label(); got != "LOCAL: /path/on/disk/sub" {
		t.Errorf("label() at sub = %q, want %q", got, "LOCAL: /path/on/disk/sub")
	}
}

func TestView_AllRowsBetweenBordersHaveEqualVisibleWidth(t *testing.T) {
	fake := newFakeS3("logs/a.txt", "readme.txt")
	m := New(t.Context(), fake, "bucket", "us-west-2", t.TempDir())
	m.width, m.height = 100, 30
	drainCmd(m, m.Init())

	out := m.View()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected a multi-line boxed view, got %d lines:\n%s", len(lines), out)
	}
	want := utf8.RuneCountInString(stripANSI(lines[0]))
	for i, l := range lines {
		if got := utf8.RuneCountInString(stripANSI(l)); got != want {
			t.Errorf("line %d width = %d, want %d (box misaligned):\n%q\nfull view:\n%s", i, got, want, l, out)
		}
	}
}
