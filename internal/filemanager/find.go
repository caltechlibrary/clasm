package filemanager

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// findProgressMsg reports Find's live "Searching... (N scanned, M
// matched)" status (DESIGN.md 21.7) and, on the final message, the
// completed flat result list.
type findProgressMsg struct {
	side    side
	ch      <-chan findUpdate
	scanned int
	matched int
	results []entry
	done    bool
	err     error
}

// findUpdate is one value sent from a Find's background goroutine.
type findUpdate struct {
	scanned int
	matched int
	results []entry // set only on the final update
	done    bool
	err     error
}

func waitForFind(s side, ch <-chan findUpdate) tea.Cmd {
	return func() tea.Msg {
		u, open := <-ch
		if !open {
			return findProgressMsg{side: s, done: true}
		}
		return findProgressMsg{side: s, ch: ch, scanned: u.scanned, matched: u.matched, results: u.results, done: u.done, err: u.err}
	}
}

func (m *Model) handleFindProgress(msg findProgressMsg) (tea.Model, tea.Cmd) {
	p := m.remote
	if msg.side == sideLocal {
		p = m.local
	}
	if p == nil || p.find == nil {
		return m, nil
	}
	p.find.scanned = msg.scanned
	if msg.err != nil {
		p.find.err = msg.err
		p.find.done = true
		return m, nil
	}
	if msg.done {
		p.find.results = msg.results
		p.find.done = true
		p.cursor = 0
		return m, nil
	}
	if msg.ch == nil {
		return m, nil
	}
	return m, waitForFind(msg.side, msg.ch)
}

// startFindPrompt opens the command line pre-armed for `:find <pattern>`
// (DESIGN.md 21.7); the hotkey and the colon-command form share one
// entry point (runFind) rather than two parallel implementations.
func (m *Model) startFindPrompt() (tea.Model, tea.Cmd) {
	m.cmdPrefix = ':'
	m.cmdBuf = "find "
	return m, nil
}

// runFind starts a cancellable recursive glob search from the focused
// pane's current position (DESIGN.md 21.7).
func (m *Model) runFind(pattern string) tea.Cmd {
	p := m.focused()
	ctx, cancel := context.WithCancel(m.ctx)
	p.find = &findState{pattern: pattern, base: p.prefix, cancel: cancel}

	ch := make(chan findUpdate)
	if p.side == sideLocal {
		go findLocal(ctx, joinKey(p.root, p.prefix), pattern, ch)
	} else {
		go findRemote(ctx, m.client, m.bucket, p.prefix, pattern, ch)
	}
	// Batch in a fresh spinner tick in case it had stopped (nothing else
	// was busy) since the last time something started running.
	return tea.Batch(waitForFind(p.side, ch), m.spin.Tick)
}

func (m *Model) cancelFind(p *pane) {
	if p.find != nil && p.find.cancel != nil {
		p.find.cancel()
	}
	p.find = nil
	p.cursor = 0
}

func findLocal(ctx context.Context, dir, pattern string, ch chan<- findUpdate) {
	defer close(ch)
	all, err := listLocalRecursive(dir)
	if err != nil {
		ch <- findUpdate{err: err, done: true}
		return
	}
	var matched []entry
	for i, e := range all {
		if ctx.Err() != nil {
			return
		}
		if globMatch(pattern, e.name) {
			matched = append(matched, e)
		}
		if i%50 == 0 {
			ch <- findUpdate{scanned: i + 1, matched: len(matched)}
		}
	}
	ch <- findUpdate{scanned: len(all), matched: len(matched), results: matched, done: true}
}

func findRemote(ctx context.Context, client awsclient.S3API, bucket, prefix, pattern string, ch chan<- findUpdate) {
	defer close(ch)
	all, err := listS3Recursive(ctx, client, bucket, prefix)
	if err != nil {
		ch <- findUpdate{err: err, done: true}
		return
	}
	var matched []entry
	for _, e := range all {
		if globMatch(pattern, e.name) {
			matched = append(matched, e)
		}
	}
	ch <- findUpdate{scanned: len(all), matched: len(matched), results: matched, done: true}
}

// findStatusText renders Find's live/finished status line.
func findStatusText(f *findState) string {
	if f.err != nil {
		return fmt.Sprintf("Find %q: error: %v", f.pattern, f.err)
	}
	if !f.done {
		return fmt.Sprintf("Searching for %q... (%d scanned)", f.pattern, f.scanned)
	}
	return fmt.Sprintf("Find %q: %d scanned, %d matched -- Enter to jump, Esc to discard", f.pattern, f.scanned, len(f.results))
}
