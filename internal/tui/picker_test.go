package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func testPickerConfig(rows []string) PickerConfig {
	return PickerConfig{
		Title:  "Test Picker",
		Header: "NAME  VALUE",
		Rows:   rows,
	}
}

// Init clears the screen and homes the cursor before the first render --
// see ListViewModel's own TestListView_InitClearsScreen and
// DECISIONS.md, "Clear the screen on entry for every inline bubbletea
// screen".
func TestPicker_InitClearsScreen(t *testing.T) {
	m := NewPickerModel(testPickerConfig(nil))
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() = nil, want a command that clears the screen")
	}
	if got := cmd(); got != tea.ClearScreen() {
		t.Errorf("Init()() = %#v, want tea.ClearScreen()", got)
	}
}

func TestPicker_EnterSelectsTheCursorRow(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"alpha", "beta", "gamma"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "beta")
	tm.Send(typeKey(tea.KeyDown))
	tm.Send(typeKey(tea.KeyEnter))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if final.cancelled {
		t.Fatal("expected a selection, got cancelled")
	}
	if final.selected != 1 {
		t.Errorf("selected = %d, want 1 (beta)", final.selected)
	}
}

func TestPicker_QCancelsWithoutSelecting(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"alpha", "beta"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "alpha")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if !final.cancelled {
		t.Error("expected cancelled=true after 'q'")
	}
}

func TestPicker_CtrlCCancelsWithoutSelecting(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"alpha", "beta"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "alpha")
	tm.Send(typeKey(tea.KeyCtrlC))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if !final.cancelled {
		t.Error("expected cancelled=true after ctrl+c")
	}
}

func TestPicker_LegendShowsFilterScrollAndQuit(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"row1"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	waitForAll(t, tm, "scroll", "filter", "Quit")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestPicker_SlashEntersFilterModeAndNarrowsRows(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"apple", "banana", "cherry"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitForAll(t, tm, "apple", "banana", "cherry")

	tm.Send(key('/'))
	for _, r := range "ban" {
		tm.Send(key(r))
	}
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "banana") && !strings.Contains(string(b), "apple")
	}, teatest.WithDuration(2*time.Second))

	tm.Send(typeKey(tea.KeyEnter)) // commit filter, then Enter again selects
	tm.Send(typeKey(tea.KeyEnter))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if final.cancelled {
		t.Fatal("expected a selection, got cancelled")
	}
	if final.selected != 1 {
		t.Errorf("selected = %d, want 1 (banana)", final.selected)
	}
}

func TestPicker_FilterIsCaseInsensitive(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"Apple", "Banana"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "Apple")
	tm.Send(key('/'))
	for _, r := range "BAN" {
		tm.Send(key(r))
	}
	waitFor(t, tm, "Banana")
	tm.Send(typeKey(tea.KeyEnter))
	tm.Send(typeKey(tea.KeyEnter))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if final.selected != 1 {
		t.Errorf("selected = %d, want 1 (Banana)", final.selected)
	}
}

func TestPicker_EscClearsFilter(t *testing.T) {
	// Checks are combined into single WaitFor calls rather than several
	// separate ones -- bubbletea only retransmits screen lines that
	// changed since the *immediately preceding* frame, so asserting on
	// text from an earlier, already-drained frame reappearing later can
	// race if a separate WaitFor call already consumed it (the same
	// gotcha internal/filemanager's own tests document).
	m := NewPickerModel(testPickerConfig([]string{"apple", "banana"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Send(key('/'))
	for _, r := range "ban" {
		tm.Send(key(r))
	}
	waitForAll(t, tm, "banana", "/ban") // filtered view + typed filter text together

	tm.Send(typeKey(tea.KeyEsc))
	waitForAll(t, tm, "apple", "filter: none") // cleared: both rows back, status reset

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestPicker_LettersDuringFilterModeAreTextNotCommands(t *testing.T) {
	// 'q' and 'j'/'k' would normally quit/navigate -- while filtering
	// they must be treated as literal filter text instead, matching
	// internal/filemanager's own command-line precedent
	// (handleCommandLineKey never special-cases ctrl+c/q while typing).
	m := NewPickerModel(testPickerConfig([]string{"quick", "jump", "kite"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Send(key('/'))
	tm.Send(key('q')) // would quit if not in filter mode
	// Checked together, not as a separate later wait -- see
	// TestPicker_EscClearsFilter's comment on why.
	waitForAll(t, tm, "quick", "/q")

	tm.Send(typeKey(tea.KeyEnter)) // commit the filter, leaving filter-typing mode
	tm.Send(key('q'))              // now outside filter mode -- should cancel
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if !final.cancelled {
		t.Error("expected the second 'q' (outside filter mode) to cancel")
	}
}

func TestPicker_EmptyRowsShowsPlaceholder(t *testing.T) {
	m := NewPickerModel(testPickerConfig(nil))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "(empty)")

	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestPicker_EnterWithNoVisibleRowsIsANoOp(t *testing.T) {
	m := NewPickerModel(testPickerConfig([]string{"apple"}))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	waitFor(t, tm, "apple")
	tm.Send(key('/'))
	for _, r := range "zzz" {
		tm.Send(key(r))
	}
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "(empty)")
	}, teatest.WithDuration(2*time.Second))
	tm.Send(typeKey(tea.KeyEnter)) // commit empty-filter match
	tm.Send(typeKey(tea.KeyEnter)) // Enter with nothing visible: no-op

	tm.Send(typeKey(tea.KeyEsc))
	waitFor(t, tm, "apple")
	tm.Send(key('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	final := tm.FinalModel(t).(*PickerModel)
	if !final.cancelled {
		t.Error("expected cancellation, not a stray selection from the no-op Enter")
	}
}

// The two tests below drive PickerModel directly, matching
// listview_test.go's established split: teatest for key-driven behavior
// with content comfortably smaller than the terminal, direct
// Model-driving for anything that depends on exact scroll-window/height
// math (see listview_test.go's comment for the full rationale).

func TestPicker_ScrollsPastAWindowfulWithArrowsOrJK(t *testing.T) {
	rows := manyRows(100)
	m := NewPickerModel(testPickerConfig(rows))
	m.width, m.height = 80, 40

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
}

func TestPicker_ShowsScrollPositionIndicatorWhenRowsExceedWindow(t *testing.T) {
	rows := manyRows(100)
	m := NewPickerModel(testPickerConfig(rows))
	m.width, m.height = 80, 40

	out := m.View()
	if !strings.Contains(out, "of 100") {
		t.Errorf("expected a scroll-position indicator mentioning the total row count, got:\n%s", out)
	}
}
