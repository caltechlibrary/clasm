package filemanager

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// Model is the file manager screen's bubbletea Model (DESIGN.md 21.4-21.8):
// single-pane (bucket only) or double-pane (bucket + a linked local
// directory), sharing one Model/Update/View so linking/unlinking a local
// directory mid-session (21.3) is just a state transition, not a
// restart.
type Model struct {
	ctx    context.Context
	client awsclient.S3API
	bucket string
	region string

	remote *pane
	local  *pane // nil in single-pane mode
	focus  side

	width, height int

	// Command line -- inert until ':' or '/' takes focus (DESIGN.md
	// 21.4). cmdPrefix is 0 when inactive.
	cmdPrefix rune
	cmdBuf    string

	status  string // transient one-line status/error above the hotkey bar
	overlay *overlay

	// colorEnabled gates the cursor/tag row styling (view.go's
	// styleRow) the same way the rest of this codebase gates ANSI
	// output -- respects NO_COLOR and falls back to plain text when
	// stdout isn't a terminal (internal/ui.ColorEnabled).
	colorEnabled bool

	// spin animates next to a pane's header while its listing is
	// loading (loadingRemote/loadingLocal) and next to Find's status
	// line while a search is running -- both can take a real,
	// noticeable amount of time against a large bucket, and with no
	// feedback the screen just looked frozen. Only ticks while isBusy()
	// is true: loadRemoteCmd/loadLocalCmd/runFind each batch in a fresh
	// m.spin.Tick to (re)start the chain, and the spinner.TickMsg
	// handler drops the returned re-tick Cmd once nothing is busy,
	// stopping it rather than ticking forever -- both to avoid a
	// pointless idle redraw loop and because a test that drives the
	// Model by synchronously draining returned tea.Cmds (no real
	// tea.Program, no real timers) would otherwise never terminate.
	spin          spinner.Model
	loadingRemote bool
	loadingLocal  bool

	quitting bool
	err      error // set on a fatal error; surfaced by Run's return value
}

// New builds the Model for one session. localDir is "" for single-pane
// mode (DESIGN.md 21.3).
func New(ctx context.Context, client awsclient.S3API, bucket, region, localDir string) *Model {
	m := &Model{
		ctx:          ctx,
		client:       client,
		bucket:       bucket,
		region:       region,
		remote:       newPane(sideRemote, bucket),
		focus:        sideRemote,
		colorEnabled: ui.ColorEnabled(),
		spin:         spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	if localDir != "" {
		m.local = newPane(sideLocal, localDir)
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadRemoteCmd(m.remote.prefix)}
	if m.local != nil {
		cmds = append(cmds, m.loadLocalCmd(m.local.prefix))
	}
	return tea.Batch(cmds...)
}

// setLoading updates the busy flag the header spinner (view.go) checks
// for the given side.
func (m *Model) setLoading(s side, loading bool) {
	if s == sideLocal {
		m.loadingLocal = loading
		return
	}
	m.loadingRemote = loading
}

// isLoading reports whether s's pane has a listing fetch in flight.
func (m *Model) isLoading(s side) bool {
	if s == sideLocal {
		return m.loadingLocal
	}
	return m.loadingRemote
}

// isBusy reports whether any operation the spinner should animate for
// is currently running: a listing fetch on either side, or a Find
// that hasn't finished yet.
func (m *Model) isBusy() bool {
	if m.loadingRemote || m.loadingLocal {
		return true
	}
	if m.remote.find != nil && !m.remote.find.done {
		return true
	}
	if m.local != nil && m.local.find != nil && !m.local.find.done {
		return true
	}
	return false
}

// focused returns the currently-focused pane.
func (m *Model) focused() *pane {
	if m.focus == sideLocal && m.local != nil {
		return m.local
	}
	return m.remote
}

// listLoadedMsg reports a completed listing fetch. prefix guards against
// applying a stale result if the operator navigated again before a
// slower fetch returned.
type listLoadedMsg struct {
	side    side
	prefix  string
	entries []entry
	err     error
}

func (m *Model) loadRemoteCmd(prefix string) tea.Cmd {
	m.loadingRemote = true
	client, bucket := m.client, m.bucket
	ctx := m.ctx
	fetch := func() tea.Msg {
		entries, err := listS3Level(ctx, client, bucket, prefix)
		return listLoadedMsg{side: sideRemote, prefix: prefix, entries: entries, err: err}
	}
	return tea.Batch(fetch, m.spin.Tick)
}

func (m *Model) loadLocalCmd(dir string) tea.Cmd {
	m.loadingLocal = true
	full := joinKey(m.local.root, dir)
	fetch := func() tea.Msg {
		entries, err := listLocalLevel(full)
		return listLoadedMsg{side: sideLocal, prefix: dir, entries: entries, err: err}
	}
	return tea.Batch(fetch, m.spin.Tick)
}

func (m *Model) reloadCmd(s side) tea.Cmd {
	if s == sideLocal {
		return m.loadLocalCmd(m.local.prefix)
	}
	return m.loadRemoteCmd(m.remote.prefix)
}

// refreshFocused explicitly reloads the focused pane's current level --
// `r` / `:refresh`. Actions already refresh the pane(s) they touch on
// their own (refreshAfterAction), but a manual, always-available
// refresh is a reasonable direct answer to "how do I get the window to
// update" regardless of the reason it might look stale (e.g. a change
// made outside this session, in another terminal or the AWS console).
func (m *Model) refreshFocused() tea.Cmd {
	m.focused().find = nil
	return m.reloadCmd(m.focus)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case listLoadedMsg:
		p := m.remote
		if msg.side == sideLocal {
			p = m.local
		}
		if p == nil {
			return m, nil
		}
		// Guard against a stale response landing after further
		// navigation moved the pane on to a different prefix/dir --
		// checked before clearing the loading flag, since a genuinely
		// stale response doesn't mean the *current* navigation's
		// request has come back yet.
		if msg.prefix != p.prefix {
			return m, nil
		}
		m.setLoading(msg.side, false)
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
			return m, nil
		}
		p.entries = msg.entries
		// A fresh, successfully-applied listing supersedes any active
		// Find snapshot for this pane -- otherwise pane.visible() keeps
		// showing find.results (a point-in-time flat list) instead of
		// the just-reloaded p.entries, so e.g. deleting tagged items
		// found via Find still showed them as present after the
		// post-delete refresh landed. Safe to always clear here: this
		// only fires when a load for the pane's *current* prefix just
		// landed (the staleness guard above), and the only loads that
		// ever reach a pane with find still active are refreshes after
		// an action -- ordinary navigation already clears find itself
		// (pane.enter).
		p.find = nil
		p.clampCursor()
		return m, nil

	case opProgressMsg:
		return m.handleOpProgress(msg)

	case findProgressMsg:
		return m.handleFindProgress(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.isBusy() {
			return m, nil
		}
		return m, cmd

	case metadataMsg:
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
		} else {
			m.status = msg.text
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay != nil {
		return m.handleOverlayKey(msg)
	}
	if m.cmdPrefix != 0 {
		return m.handleCommandLineKey(msg)
	}

	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit

	case "up":
		if p := m.focused(); p.cursor > 0 {
			p.cursor--
		}
	case "down":
		if p := m.focused(); p.cursor < len(p.visible())-1 {
			p.cursor++
		}
	case "left", "backspace":
		return m.navigateUp()
	case "right", "enter":
		return m.navigateEnterOrJump()

	case "tab":
		if m.local != nil {
			if m.focus == sideRemote {
				m.focus = sideLocal
			} else {
				m.focus = sideRemote
			}
		}

	case " ":
		m.focused().toggleTag()
	case "*":
		m.focused().tagAllVisible()

	case "/":
		m.cmdPrefix = '/'
		m.cmdBuf = ""
		m.status = filterHelpText
	case ":":
		m.cmdPrefix = ':'
		m.cmdBuf = ""

	case "f":
		m.cmdPrefix = '/'
		m.cmdBuf = m.focused().filter
		m.status = filterHelpText

	case "esc":
		if p := m.focused(); p.find != nil {
			m.cancelFind(p)
		} else if p.filter != "" {
			p.filter = ""
			p.clampCursor()
		}

	case "F":
		return m.startFindPrompt()

	case "d":
		return m.startDownload()
	case "u":
		return m.startUpload()
	case "x":
		return m.startDelete()
	case "m":
		return m.startShowMetadata()
	case "l":
		return m.startLinkPrompt()
	case "S":
		return m.startSync()
	case "r":
		return m, m.refreshFocused()
	}
	return m, nil
}

// navigateUp handles left/h/backspace: exit an active Find back to
// normal browsing, or go to the parent directory.
func (m *Model) navigateUp() (tea.Model, tea.Cmd) {
	p := m.focused()
	if p.find != nil {
		m.cancelFind(p)
		return m, nil
	}
	if p.prefix == "" {
		return m, nil
	}
	p.up()
	return m, m.reloadCmd(p.side)
}

// navigateEnterOrJump handles right/l/enter: inside a Find, Enter jumps
// to the match's parent directory (DESIGN.md 21.7); otherwise it
// descends into the directory row under the cursor.
func (m *Model) navigateEnterOrJump() (tea.Model, tea.Cmd) {
	p := m.focused()
	e, ok := p.current()
	if !ok {
		return m, nil
	}
	if p.find != nil {
		dest := p.parentPrefixOf(e.key)
		m.cancelFind(p)
		p.enter(dest)
		return m, m.reloadCmd(p.side)
	}
	if e.kind != kindDir {
		return m, nil
	}
	p.enter(p.toPrefix(e.key))
	return m, m.reloadCmd(p.side)
}
