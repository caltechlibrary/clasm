package tui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func typeKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func waitFor(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(want))
	}, teatest.WithDuration(2*time.Second))
}

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

func testConfig(rows []string) ListViewConfig {
	return ListViewConfig{
		Title:  "Test List",
		Header: "NAME  VALUE",
		Rows:   rows,
	}
}

// Init clears the screen and homes the cursor before the first render
// (DECISIONS.md, "Clear the screen on entry for every inline bubbletea
// screen") -- without it, a box sized to nearly the full terminal
// height can scroll content out of view if rendering starts wherever
// the cursor already sits (e.g. below a previous menu's prints).
func TestListView_InitClearsScreen(t *testing.T) {
	m := NewListViewModel(testConfig(nil))
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() = nil, want a command that clears the screen")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Errorf("Init()() = %#v, want tea.ClearScreen()", got)
	}
}

func TestListView_ShowsTitleHeaderAndRows(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"alpha  1", "beta   2"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitForAll(t, tm, "Test List", "NAME  VALUE", "alpha  1", "beta   2")

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_EmptyRowsShowsPlaceholder(t *testing.T) {
	m := NewListViewModel(testConfig(nil))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "(empty)")

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_QuitsOnQOrCtrlC(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"row1"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	waitFor(t, tm, "row1")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	m2 := NewListViewModel(testConfig([]string{"row1"}))
	tm2 := teatest.NewTestModel(t, m2, teatest.WithInitialTermSize(80, 24))
	waitFor(t, tm2, "row1")
	tm2.Send(typeKey(tea.KeyCtrlC))
	tm2.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_LegendShowsScrollFilterAndQuit(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"row1"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	waitForAll(t, tm, "scroll", "filter", "Quit")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

// Filtering is shared with PickerModel via filterState (internal/tui's
// own DESIGN.md-documented gap: "/  Filter  Menus, pickers, lists,
// managers" listed filtering for lists too, but ListViewModel had none
// until now) -- these tests mirror picker_test.go's filter tests, minus
// selection, since List has nothing to choose.

func TestListView_SlashEntersFilterModeAndNarrowsRows(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"apple", "banana", "cherry"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitForAll(t, tm, "apple", "banana", "cherry")

	tm.Send(key('/'))
	for _, r := range "ban" {
		tm.Send(key(r))
	}
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "banana") && !strings.Contains(string(b), "apple")
	}, teatest.WithDuration(2*time.Second))

	tm.Send(typeKey(tea.KeyEnter)) // commit filter, keep navigating the narrowed list
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_FilterIsCaseInsensitive(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"Apple", "Banana"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "Apple")
	tm.Send(key('/'))
	for _, r := range "BAN" {
		tm.Send(key(r))
	}
	waitFor(t, tm, "Banana")
	tm.Send(typeKey(tea.KeyEnter))
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_EscClearsFilter(t *testing.T) {
	// Checks are combined into single WaitFor calls rather than several
	// separate ones -- see picker_test.go's TestPicker_EscClearsFilter
	// comment for why (bubbletea only retransmits screen lines that
	// changed since the immediately preceding frame).
	m := NewListViewModel(testConfig([]string{"apple", "banana"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Send(key('/'))
	for _, r := range "ban" {
		tm.Send(key(r))
	}
	waitForAll(t, tm, "banana", "/ban")

	tm.Send(typeKey(tea.KeyEsc))
	waitForAll(t, tm, "apple", "filter: none")

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestListView_LettersDuringFilterModeAreTextNotCommands(t *testing.T) {
	// 'q' and 'j'/'k' would normally quit/navigate -- while filtering
	// they must be treated as literal filter text instead, matching
	// picker_test.go's TestPicker_LettersDuringFilterModeAreTextNotCommands.
	m := NewListViewModel(testConfig([]string{"quick", "jump", "kite"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Send(key('/'))
	tm.Send(key('q')) // would quit if not in filter mode
	waitForAll(t, tm, "quick", "/q")

	tm.Send(typeKey(tea.KeyEnter)) // commit the filter, leaving filter-typing mode
	tm.Send(key('q'))              // now outside filter mode -- should quit
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

// manyRows returns n uniquely-named rows ("row-000", "row-001", ...).
func manyRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("row-%03d", i)
	}
	return rows
}

// The two tests below drive ListViewModel directly (set width/height,
// call Update/View synchronously) instead of through a real teatest
// Program, matching internal/filemanager's own convention for anything
// that depends on exact scroll-window/height math
// (TestModel_LargeListing_*): when rendered content height exactly
// matches the declared terminal height (this component's own "fill the
// screen" design, by construction: windowHeight = height - chrome), a
// real inline (non-altscreen) bubbletea Program run through a real
// terminal emulator can lose its own top line to the terminal's own
// scrolling -- a known bubbletea inline-rendering edge case, not
// specific to this component. teatest remains the right tool for
// key-driven behavior with content comfortably smaller than the
// terminal (the other tests in this file).

func TestListView_ScrollsPastAWindowfulWithArrowsOrJK(t *testing.T) {
	rows := manyRows(100)
	m := NewListViewModel(testConfig(rows))
	m.width, m.height = 80, 40 // windowHeight well under 100 rows either way

	out := m.View()
	if !strings.Contains(out, "row-000") {
		t.Fatalf("expected the first row visible initially, got:\n%s", out)
	}

	for range 99 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	out = m.View()
	if !strings.Contains(out, "row-099") {
		t.Errorf("expected the last row visible after scrolling all the way down, got:\n%s", out)
	}

	for range 99 {
		m.Update(key('k'))
	}
	out = m.View()
	if !strings.Contains(out, "row-000") {
		t.Errorf("expected the first row visible again after scrolling all the way back up, got:\n%s", out)
	}
}

func TestListView_ShowsScrollPositionIndicatorWhenRowsExceedWindow(t *testing.T) {
	rows := manyRows(100)
	m := NewListViewModel(testConfig(rows))
	m.width, m.height = 80, 40

	out := m.View()
	if !strings.Contains(out, "of 100") {
		t.Errorf("expected a scroll-position indicator mentioning the total row count, got:\n%s", out)
	}
}

func TestListView_NoScrollPositionIndicatorWhenEverythingFits(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"row1", "row2"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "row1")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final, err := io.ReadAll(tm.FinalOutput(t))
	if err != nil {
		t.Fatalf("reading final output: %v", err)
	}
	out := string(final)
	if strings.Contains(out, "of 2") {
		t.Errorf("did not expect a scroll-position indicator when everything fits, got:\n%s", out)
	}
}

func TestListView_CursorRowIsMarked(t *testing.T) {
	m := NewListViewModel(testConfig([]string{"alpha", "beta"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "> alpha")

	tm.Send(typeKey(tea.KeyDown))
	waitFor(t, tm, "> beta")

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}
