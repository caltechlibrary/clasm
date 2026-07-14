package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrCancelled is returned by RunPicker when the operator cancels
// ('q'/ctrl+c) without choosing a row -- mirrors ui.PickList's
// ErrCancelled and huh.ErrUserAborted, so callers can use the same
// "if err != nil { return cancelledIsNil(...) }" shape already
// established for every other pick-list-shaped call site.
var ErrCancelled = errors.New("picker cancelled")

// PickerConfig configures a Picker-tier screen (DESIGN.md, "Terminal UI
// Architecture: Menus, Actions, Lists, and Managers," "Picker tier"):
// the same chrome as ListView, plus selection and filtering.
type PickerConfig struct {
	// Title is the banner text shown in the top border.
	Title string
	// Description is optional contextual/explanatory text shown as its
	// own line directly below the top border, above Header/rows -- the
	// Picker-tier equivalent of huh.Select's .Description(...) (DESIGN.
	// md, "Contextual description text on Menu/Picker-tier screens").
	// "" means no description line (most Picker-tier screens' Title
	// alone is self-explanatory).
	Description string
	// Header is an optional already-rendered column-header line, shown
	// once, frozen at the top of the scrollable body. "" means no
	// header row.
	Header string
	// Rows are the already-rendered, selectable rows (one per line, in
	// order) -- Picker only windows, filters, and marks them, it does
	// not format columns itself.
	Rows []string
	// ColorEnabled gates cursor-row reverse-video styling (this
	// project's NO_COLOR/non-TTY convention, internal/ui.ColorEnabled).
	ColorEnabled bool
	// InitialCursor positions the cursor on this row index into Rows
	// when the picker first renders, instead of the first row -- e.g.
	// pre-selecting the instance used last time (DECISIONS.md, "Recall
	// Backup Archive & Trim's instance/directory choices per-instance").
	// Out-of-range values (including the zero value when the caller has
	// no prior choice to recall) fall back to 0, matching every
	// existing caller's unchanged behavior.
	InitialCursor int
}

// PickerModel is a selectable, filterable List-tier screen's bubbletea
// Model: same chrome as ListViewModel, but Enter chooses the row under
// the cursor and returns it. Filtering is shared with ListViewModel via
// filterState, so both behave identically.
type PickerModel struct {
	cfg    PickerConfig
	filter *filterState

	width, height int
	quitting      bool
	selected      int // index into cfg.Rows; -1 until Enter chooses one
	cancelled     bool
}

// NewPickerModel builds a PickerModel for cfg.
func NewPickerModel(cfg PickerConfig) *PickerModel {
	filter := newFilterState(cfg.Rows)
	if cfg.InitialCursor > 0 && cfg.InitialCursor < len(cfg.Rows) {
		filter.cursor = cfg.InitialCursor
	}
	return &PickerModel{cfg: cfg, selected: -1, filter: filter}
}

// RunPicker runs cfg as a scoped bubbletea.Program and blocks until the
// operator either chooses a row (Enter) or cancels ('q'/ctrl+c, reported
// as ErrCancelled). Renders inline (no tea.WithAltScreen), matching
// every other screen in clasm. Returns the index of the chosen row into
// cfg.Rows -- callers map it back into their own typed slice.
func RunPicker(ctx context.Context, cfg PickerConfig) (int, error) {
	m := NewPickerModel(cfg)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return -1, err
	}
	fm := final.(*PickerModel)
	if fm.cancelled {
		return -1, ErrCancelled
	}
	return fm.selected, nil
}

// Init clears the screen and homes the cursor before the first render --
// see ListViewModel.Init's doc comment for why this matters for an
// inline (non-alt-screen), nearly-full-terminal-height box like this
// one.
func (m *PickerModel) Init() tea.Cmd { return tea.ClearScreen }

func (m *PickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.cancelled = true
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if len(m.filter.visible) > 0 {
				m.selected = m.filter.visible[m.filter.cursor]
				m.quitting = true
				return m, tea.Quit
			}
		default:
			m.filter.handleIdleKey(msg)
		}
	}
	return m, nil
}

func (m *PickerModel) windowHeight() int {
	return filterableWindowHeight(m.height, m.cfg.Header != "", m.cfg.Description != "")
}

func (m *PickerModel) View() string {
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
	if m.cfg.Description != "" {
		b.WriteString(BoxLine("  "+m.cfg.Description, inner))
		b.WriteString(Divider(inner))
	}
	if m.cfg.Header != "" {
		b.WriteString(BoxLine("  "+m.cfg.Header, inner))
		b.WriteString(Divider(inner))
	}

	// The content area's height is pinned to the *unfiltered* row count
	// (bounded by windowHeight), not however many rows the current
	// filter happens to match -- otherwise the box's rendered height
	// would shrink and grow as the operator types a filter, which (a)
	// looks jarring and (b) reproduced the same class of inline-render
	// hiccup Phase 20.6 already found with exact/changing frame heights
	// going through a live bubbletea Program.
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
	b.WriteString(BoxLine("↑/↓,k/j scroll  / filter  enter select  q Quit", inner))
	b.WriteString(BottomBorder(inner))
	return b.String()
}
