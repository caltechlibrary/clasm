package filemanager

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// drainCmd synchronously executes cmd and every tea.Cmd it (recursively,
// via tea.Batch) triggers, feeding each resulting tea.Msg back into
// m.Update -- useful for driving a Model in a plain unit test without a
// running tea.Program.
//
// spinner.TickMsg is a deliberate exception: it's processed once (so
// Update's bookkeeping runs), but its own re-tick Cmd is never chased
// further. A real tea.Program runs every in-flight Cmd concurrently, so
// by the time a real spinner tick fires, other work (e.g. the sibling
// pane's own load) has typically already resolved and isBusy() has
// already gone false. This synchronous drain has no such guarantee: it
// processes one nested tea.Batch branch all the way through before
// moving to the next, so isBusy() can see stale state (a sibling load
// that hasn't been processed yet) for the entire depth of one branch --
// chasing the tick chain in that state loops forever. Since ticks are
// purely cosmetic (they don't affect anything a test asserts on), not
// chasing them is safe.
func drainCmd(m *Model, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				drainCmd(m, c)
			}
			return
		}
		var next tea.Cmd
		_, next = m.Update(msg)
		if _, isTick := msg.(spinner.TickMsg); isTick {
			return
		}
		cmd = next
	}
}
