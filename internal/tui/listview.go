package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// defaultListViewWidth/defaultListViewHeight are used before the first
// tea.WindowSizeMsg arrives (or in a non-interactive context); real
// usage overrides both immediately with the actual terminal size --
// same convention internal/filemanager's Model uses.
const defaultListViewWidth = 96
const defaultListViewHeight = 30

// minListViewWidth is a floor so a tiny terminal can't collapse the box
// math into negative widths.
const minListViewWidth = 40

// minListViewRows floors how few rows the scrollable body ever gets,
// even in a very short terminal.
const minListViewRows = 3

// ListViewConfig configures a List-tier screen (DESIGN.md, "Terminal UI
// Architecture: Menus, Actions, Lists, and Managers"): a single bordered
// box, an optional frozen header row, and a scrollable, filterable
// body -- the read-only counterpart to internal/filemanager's
// interactive manager, built on the same shared chrome.
type ListViewConfig struct {
	// Title is the banner text shown in the top border, e.g.
	// "S3 Buckets". A scroll-position indicator ("[a-b of n]") is
	// appended automatically once Rows doesn't fit in one screenful.
	Title string
	// Header is an optional already-rendered column-header line, shown
	// once, frozen at the top of the scrollable body. "" means no
	// header row.
	Header string
	// Rows are the already-rendered data rows (one row per line, in
	// order) -- ListView only windows, filters, and marks them, it does
	// not format columns itself.
	Rows []string
	// ColorEnabled gates cursor-row reverse-video styling (this
	// project's NO_COLOR/non-TTY convention, internal/ui.ColorEnabled).
	ColorEnabled bool
}

// ListViewModel is a read-only, scrollable, filterable List-tier
// screen's bubbletea Model. Quitting ('q'/ctrl+c) is the only way out --
// there's nothing to tag or act on here, unlike internal/filemanager's
// Model. Filtering behaves identically to PickerModel (both built on
// the shared filterState) minus selection.
type ListViewModel struct {
	cfg    ListViewConfig
	filter *filterState

	width, height int
	quitting      bool
}

// NewListViewModel builds a ListViewModel for cfg.
func NewListViewModel(cfg ListViewConfig) *ListViewModel {
	return &ListViewModel{cfg: cfg, filter: newFilterState(cfg.Rows)}
}

// RunListView runs cfg as a scoped bubbletea.Program and blocks until
// the operator quits ('q' or ctrl+c). Renders inline (no
// tea.WithAltScreen), matching every other screen in clasm.
func RunListView(ctx context.Context, cfg ListViewConfig) error {
	m := NewListViewModel(cfg)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// Init clears the screen and homes the cursor before the first render
// (tea.ClearScreen -- the officially-recommended fix for inline,
// non-alt-screen programs per its own doc comment). Without this,
// windowHeight sizes the box to nearly the full terminal height, but
// rendering starts wherever the cursor already sits (e.g. below a
// previous menu's prints) -- if that box doesn't fit in the remaining
// rows below the cursor, the terminal scrolls, and bubbletea's
// redraw-in-place bookkeeping (how many lines to move the cursor up by)
// goes stale relative to what actually happened on screen, pushing the
// top of the box (or more) out of view. Clearing first guarantees
// rendering always starts at row 0, so the computed height always fits.
func (m *ListViewModel) Init() tea.Cmd { return tea.ClearScreen }

func (m *ListViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.filter.filtering {
			m.filter.handleFilterKey(msg)
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		default:
			m.filter.handleIdleKey(msg)
		}
	}
	return m, nil
}

func (m *ListViewModel) windowHeight() int {
	return filterableWindowHeight(m.height, m.cfg.Header != "")
}

func (m *ListViewModel) View() string {
	if m.quitting {
		return ""
	}

	width := m.width
	if width <= 0 {
		width = defaultListViewWidth
	}
	if width < minListViewWidth {
		width = minListViewWidth
	}
	inner := width - 2

	windowHeight := m.windowHeight()
	total := len(m.filter.visible)
	start, end := ScrollWindow(m.filter.cursor, total, windowHeight)

	title := m.cfg.Title
	if total > windowHeight {
		title = fmt.Sprintf("%s  [%d-%d of %d]", title, start+1, end, total)
	}

	var b strings.Builder
	b.WriteString(TopBorder(" "+title+" ", inner))
	if m.cfg.Header != "" {
		b.WriteString(BoxLine("  "+m.cfg.Header, inner))
		b.WriteString(Divider(inner))
	}

	// Same content-height pinning rationale as PickerModel.View (Phase
	// 20.8): pin to the *unfiltered* row count so the box doesn't
	// grow/shrink while typing a filter.
	contentHeight := max(min(len(m.cfg.Rows), windowHeight), 1)
	shown := 0
	if total == 0 {
		b.WriteString(BoxLine("  (empty)", inner))
		shown++
	}
	for i := start; i < end; i++ {
		marker := "  "
		isCursor := i == m.filter.cursor
		if isCursor {
			marker = "> "
		}
		row := m.cfg.Rows[m.filter.visible[i]]
		b.WriteString(BoxLine(StyleRow(marker+row, isCursor, false, m.cfg.ColorEnabled), inner))
		shown++
	}
	for ; shown < contentHeight; shown++ {
		b.WriteString(BoxLine("", inner))
	}

	b.WriteString(Divider(inner))
	b.WriteString(BoxLine(m.filter.statusLine(), inner))
	b.WriteString(Divider(inner))
	b.WriteString(BoxLine("↑/↓,k/j scroll  / filter  q Quit", inner))
	b.WriteString(BottomBorder(inner))
	return b.String()
}
