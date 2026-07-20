package workflow

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/tui"
)

// newTestGuard builds a quitKeyGuard around a small huh.Select --
// mirroring runMenuField's own construction (menuQuitKeyMap,
// tui.Theme(), SubmitCmd/CancelCmd) -- so this test exercises the real
// chrome, not a stand-in theme.
func newTestGuard(t *testing.T) *quitKeyGuard {
	t.Helper()
	var idx int
	field := huh.NewSelect[int]().
		Title("Choose an option").
		Description("A short description.").
		Options(huh.NewOption("one", 0), huh.NewOption("two", 1)).
		Value(&idx)

	keymap := menuQuitKeyMap()
	form := huh.NewForm(huh.NewGroup(field)).WithKeyMap(keymap).WithTheme(tui.Theme())
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	return &quitKeyGuard{Form: form, setQuitEnabled: keymap.Quit.SetEnabled, filtering: func() bool { return false }}
}

// TestQuitKeyGuard_WindowSizeMsgProducesFullTerminalHeight is Phase
// 20.26's own required test (PLAN.md): a WindowSizeMsg drives
// WithHeight, and the *combined* on-screen output -- runMenuField's own
// printed hint line plus the form's rendered view -- fills the real
// terminal height exactly, not one line short or one line over.
// menuHintReservedLines (2: the hint line plus huh's own trailing
// help-footer line, confirmed empirically, not assumed) is what this
// guards against regressing.
func TestQuitKeyGuard_WindowSizeMsgProducesFullTerminalHeight(t *testing.T) {
	for _, termHeight := range []int{10, 24, 40} {
		guard := newTestGuard(t)
		m, _ := guard.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
		guard = m.(*quitKeyGuard)

		formLines := strings.Count(guard.View(), "\n") + 1
		// +1 for the hint line runMenuField prints via a separate
		// fmt.Fprintln before the form ever renders -- not part of the
		// form's own View() output, so it's added here to get the true
		// total on-screen line count this guard's sizing is responsible
		// for keeping within the terminal height.
		total := formLines + 1
		if total != termHeight {
			t.Errorf("termHeight=%d: hint(1) + form view(%d lines) = %d total, want exactly %d",
				termHeight, formLines, total, termHeight)
		}
	}
}

// TestQuitKeyGuard_ShortContentStillFillsTheWindow confirms the actual
// point of this phase: a menu with far fewer options than the terminal
// is tall still renders a box that fills the terminal, not a
// content-sized one -- lipgloss.Style.Height's own blank-line padding
// (DESIGN.md, "Full-height Menu Tier": "Mechanism"), not something this
// package implements itself.
func TestQuitKeyGuard_ShortContentStillFillsTheWindow(t *testing.T) {
	guard := newTestGuard(t) // only 2 options -- far shorter than 30 rows
	m, _ := guard.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	guard = m.(*quitKeyGuard)

	lines := strings.Count(guard.View(), "\n") + 1
	if lines < 25 {
		t.Errorf("rendered %d lines for a 2-option menu at terminal height 30, want it padded close to full height, not shrunk to content", lines)
	}
}

// TestQuitKeyGuard_TinyTerminalDoesNotPanic exercises the safe-no-op
// path (menuHintReservedLines could push the requested height to zero
// or negative for a very short terminal) -- Form.WithHeight(n<=0) is a
// no-op by huh's own design, and Form.Update's own WindowSizeMsg
// shrink-to-fit fallback takes over instead; this just confirms
// nothing panics or errors.
func TestQuitKeyGuard_TinyTerminalDoesNotPanic(t *testing.T) {
	guard := newTestGuard(t)
	m, _ := guard.Update(tea.WindowSizeMsg{Width: 80, Height: 1})
	guard = m.(*quitKeyGuard)
	_ = guard.View()
}
