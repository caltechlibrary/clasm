package workflow

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/caltechlibrary/clasm/internal/tui"
)

// DefaultSpinnerInterval is how often startProgressTicker's inline
// spinner advances a frame and refreshes its elapsed-time label.
const DefaultSpinnerInterval = 120 * time.Millisecond

// formatDuration formats d as "m:ss", or "h:mm:ss" for durations of one
// hour or more, rounded to the nearest second -- replaces termlib's
// equivalent (DECISIONS.md, "Remove termlib entirely: input via huh,
// output via io.Writer").
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// progressTickMsg drives progressModel's own frame/elapsed-time refresh
// cadence -- deliberately not spinner.Model's built-in FPS-based
// tick(), so the interval stays caller-controlled (production wants a
// smooth animation rate; tests want a fast one to observe multiple
// frames without a real wait).
type progressTickMsg struct{}

// progressStopMsg asks progressModel to render one final, blank frame
// (clearing the spinner line rather than leaving it printed) and then
// quit.
type progressStopMsg struct{}

// progressModel is the bubbletea model behind startProgressTicker: an
// animated spinner glyph plus an elapsed-time label, redrawn in place
// for the duration of an unbounded wait (AMI creation, cloud-init AMI
// extraction, backup upload/verify -- PLAN.md, Phase 15, "loading
// indicators"), clearing itself when the wait ends (DESIGN.md,
// "Progress ticker becomes a real spinner").
type progressModel struct {
	sp       spinner.Model
	label    string
	start    time.Time
	interval time.Duration
	stopped  bool
}

func newProgressModel(label string, interval time.Duration) *progressModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(tui.SpinnerStyle()))
	return &progressModel{sp: sp, label: label, start: time.Now(), interval: interval}
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return progressTickMsg{} })
}

func (m *progressModel) Init() tea.Cmd {
	return tickCmd(m.interval)
}

func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case progressStopMsg:
		m.stopped = true
		return m, tea.Quit
	case progressTickMsg:
		if m.stopped {
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(spinner.TickMsg{})
		_ = cmd // spinner.Update's own re-tick cmd is unused -- tickCmd drives our cadence instead
		return m, tickCmd(m.interval)
	default:
		return m, nil
	}
}

func (m *progressModel) View() string {
	if m.stopped {
		return ""
	}
	return fmt.Sprintf("%s %s (elapsed %s)\n", m.sp.View(), m.label, formatDuration(time.Since(m.start)))
}

// plainProgressInterval is how often the plain (non-terminal) ticker writes a
// line: sparse enough for a cron log, frequent enough to show a long wait is
// alive. A variable so tests can shorten it.
var plainProgressInterval = 30 * time.Second

// writerIsTerminal reports whether w is a terminal, where the animated spinner
// belongs. Anything else -- a file, a pipe, a buffer -- gets plain lines. A
// variable so tests can exercise the animated path into a buffer.
var writerIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// startProgressTicker gives feedback during a long unbounded wait (AMI creation,
// cloud-init AMI extraction, backup upload/verify -- PLAN.md, Phase 15,
// "loading indicators"). On a terminal it renders an inline, animated status
// line. Redirected to a file or pipe (a cron log, `2> log`) the animation would
// write a cursor-escape redraw every 120 ms -- 1,553 of them in one three-minute
// wait -- so it writes plain lines instead: "<label> ..." at once, then
// "<label> (elapsed M:SS)" every plainProgressInterval. The returned stop
// function blocks until the ticker has finished, so no further output can race
// with whatever the caller writes immediately after stopping.
func startProgressTicker(w io.Writer, label string) (stop func()) {
	if !writerIsTerminal(w) {
		return startPlainProgress(w, label)
	}
	p := tea.NewProgram(newProgressModel(label, DefaultSpinnerInterval), tea.WithOutput(w), tea.WithInput(nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run()
	}()

	return func() {
		p.Send(progressStopMsg{})
		<-done
	}
}

// startPlainProgress is the non-terminal ticker: one line per interval, no
// escape codes, no carriage returns.
func startPlainProgress(w io.Writer, label string) (stop func()) {
	start := time.Now()
	interval := plainProgressInterval
	fmt.Fprintf(w, "%s ...\n", label)

	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				fmt.Fprintf(w, "%s (elapsed %s)\n", label, formatDuration(time.Since(start)))
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}
