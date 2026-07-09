package filemanager

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// linkPromptMarker is a sentinel prefix distinguishing the ':link
// <path>' colon-command from an ordinary Find/other verb, entered via
// the `l` hotkey the same way `F` pre-arms ':find ' (DESIGN.md 21.3).
const linkPromptMarker = "link "

// filterHelpText is shown (as a transient status line) the moment the
// operator enters filter-edit mode (`f` or `/`) -- added after an
// operator reasonably expected a leading "/" to mean an actual
// filesystem/S3-key path (it doesn't; it's an anchor to this level's
// root, same as Find's "^"/"/" convention, DESIGN.md 21.5/21.7) and had
// to discover the anchor syntax by trial.
const filterHelpText = "Filter: substring match; prefix with ^ to match only this level's root exactly (e.g. ^index.html), not a filesystem path"

// startLinkPrompt is the `l` hotkey. When nothing is linked, it opens
// the command line pre-armed for ':link <path>'. When a directory IS
// already linked, it goes straight to a direct Confirm instead --
// "clear the line and submit empty to unlink" was reachable but not
// discoverable as *the* way back to single-pane (reported directly: "I
// need a way to go from two panels back to displaying only the S3
// bucket").
func (m *Model) startLinkPrompt() (tea.Model, tea.Cmd) {
	if m.local != nil {
		return m.startUnlinkConfirm()
	}
	m.cmdPrefix = ':'
	m.cmdBuf = linkPromptMarker
	return m, nil
}

// startUnlinkConfirm shows the direct "unlink and return to single-pane"
// gate (`l` while linked, or ':unlink'). Unlinking is an instant state
// change, not a background operation, so acceptance applies it directly
// rather than going through beginAction's progress-overlay machinery.
func (m *Model) startUnlinkConfirm() (tea.Model, tea.Cmd) {
	m.overlay = &overlay{
		kind:   overlayConfirm,
		title:  fmt.Sprintf("Unlink %s and return to single-pane view?", m.local.root),
		action: actionUnlink,
	}
	return m, nil
}

// handleCommandLineKey edits the command-line buffer while it has focus
// ('/' filter mode or ':' verb mode), dispatching on Enter and
// discarding on Esc (DESIGN.md 21.4).
func (m *Model) handleCommandLineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		prefix, buf := m.cmdPrefix, m.cmdBuf
		m.cmdPrefix, m.cmdBuf = 0, ""
		if prefix == '/' {
			m.focused().filter = buf
			m.focused().clampCursor()
			return m, nil
		}
		return m.dispatchCommand(buf)
	case "esc":
		m.cmdPrefix, m.cmdBuf = 0, ""
	case "backspace":
		if len(m.cmdBuf) > 0 {
			m.cmdBuf = m.cmdBuf[:len(m.cmdBuf)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.cmdBuf += string(msg.Runes)
		}
	}
	return m, nil
}

// dispatchCommand parses one ':'-prefixed command line and routes it to
// the same action handlers the hotkeys use -- not a second, parallel
// implementation of each action (DESIGN.md 21.6).
func (m *Model) dispatchCommand(line string) (tea.Model, tea.Cmd) {
	line = strings.TrimSpace(line)
	if line == "" {
		return m, nil
	}
	verb, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)

	switch verb {
	case "download":
		return m.startDownload()
	case "upload":
		return m.startUpload()
	case "delete":
		return m.startDelete()
	case "metadata":
		return m.startShowMetadata()
	case "find":
		if arg == "" {
			m.status = "usage: :find <pattern> -- prefix with ^ to match only this level's root (e.g. ^index.html), not a filesystem path"
			return m, nil
		}
		return m, m.runFind(arg)
	case "link":
		return m.applyLink(arg)
	case "unlink":
		if m.local == nil {
			m.status = "Nothing linked."
			return m, nil
		}
		return m.startUnlinkConfirm()
	case "sync":
		return m.startSync()
	case "refresh":
		return m, m.refreshFocused()
	case "quit", "q":
		m.quitting = true
		return m, tea.Quit
	default:
		m.status = "unknown command: " + verb
	}
	return m, nil
}

// applyLink links (arg non-empty, validated) or unlinks (arg empty) a
// local directory mid-session, splitting single-pane into double-pane or
// collapsing back without restarting the screen (DESIGN.md 21.3).
func (m *Model) applyLink(dir string) (tea.Model, tea.Cmd) {
	if dir == "" {
		m.local = nil
		if m.focus == sideLocal {
			m.focus = sideRemote
		}
		m.status = "Unlinked local directory."
		return m, nil
	}
	if err := validateLocalDir(dir); err != nil {
		m.status = "Error: " + err.Error()
		return m, nil
	}
	m.local = newPane(sideLocal, dir)
	m.status = "Linked " + dir
	return m, m.loadLocalCmd(m.local.prefix)
}
