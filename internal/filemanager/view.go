package filemanager

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// defaultBoxWidth/defaultBoxHeight are used before the first
// tea.WindowSizeMsg arrives (or in a non-interactive context); real
// usage overrides both immediately with the actual terminal size.
const defaultBoxWidth = 96
const defaultBoxHeight = 30

// minBoxWidth is a floor so a tiny terminal can't collapse the box math
// into negative widths.
const minBoxWidth = 40

// minItemRows floors how few listing rows a pane ever gets, even in a
// very short terminal.
const minItemRows = 3

// chromeRowsDouble/chromeRowsSingle count every box row that ISN'T part
// of the scrollable pane-content area: top border, the pane-area's own
// top/bottom divider(s), status line, its divider, command line, its
// divider, hotkey legend, bottom border. Double-pane has one extra
// divider (the "┬" split above the panes and the "┴" merge below both
// count, vs. single-pane's one plain divider below the one pane).
const chromeRowsDouble = 9
const chromeRowsSingle = 8

// paneItemWindowHeight returns how many *entry* rows (not counting a
// pane's own header row or its Find-status/blank row) fit in the
// current terminal, so the listing scrolls instead of silently growing
// the box past the bottom of the screen -- the bug behind "the object
// listing doesn't page, and I can't scroll down to reach an object
// further down the list": with no cap, a bucket-root listing longer
// than one screenful pushed the status line/command line/hotkey legend
// (and the rest of the listing) off screen, with no way to bring them
// back into view.
func (m *Model) paneItemWindowHeight() int {
	height := m.height
	if height <= 0 {
		height = defaultBoxHeight
	}
	chrome := chromeRowsSingle
	if m.local != nil {
		chrome = chromeRowsDouble
	}
	// -2 for the pane's own header row and its Find-status/blank row.
	h := height - chrome - 2
	return max(h, minItemRows)
}

// View renders the screen as one bordered box, matching DESIGN.md 21.4's
// mockup exactly: a title bar, one or two panes side by side (split by a
// "┬"/"┴" divider when double-pane), a status line, the command line,
// and the hotkey legend, each separated by a "├─┤" rule -- or, when an
// overlay is active, the modal progress/confirm content in place of the
// panes.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	width := m.width
	if width <= 0 {
		width = defaultBoxWidth
	}
	if width < minBoxWidth {
		width = minBoxWidth
	}
	inner := width - 2

	var b strings.Builder
	title := fmt.Sprintf(" clasm — S3 File Manager — %s (%s) ", m.bucket, m.region)
	b.WriteString(topBorder(title, inner))

	itemWindow := m.paneItemWindowHeight()

	switch {
	case m.overlay != nil:
		for _, line := range overlayLines(m.overlay, itemWindow+2) {
			b.WriteString(boxLine(line, inner))
		}
	case m.local != nil:
		leftW, rightW := splitWidths(inner)
		b.WriteString(splitDivider(leftW, rightW))
		spin := m.spin.View()
		left := paneRows(m.local, m.focus == sideLocal, m.colorEnabled, m.isLoading(sideLocal), spin, itemWindow)
		right := paneRows(m.remote, m.focus == sideRemote, m.colorEnabled, m.isLoading(sideRemote), spin, itemWindow)
		for i := range max(len(left), len(right)) {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			b.WriteString(boxRow2(l, r, leftW, rightW))
		}
		b.WriteString(mergeDivider(leftW, rightW))
	default:
		for _, line := range paneRows(m.remote, true, m.colorEnabled, m.isLoading(sideRemote), m.spin.View(), itemWindow) {
			b.WriteString(boxLine(line, inner))
		}
		b.WriteString(divider(inner))
	}

	b.WriteString(boxLine(renderStatusLineText(m.focused()), inner))
	b.WriteString(divider(inner))
	b.WriteString(boxLine(renderCommandLineText(m), inner))
	b.WriteString(divider(inner))
	b.WriteString(boxLine(renderHotkeyLegendText(m), inner))
	b.WriteString(bottomBorder(inner))

	if m.status != "" {
		b.WriteString(m.status + "\n")
	}
	return b.String()
}

// paneRows renders one pane's content as plain text rows (no box
// borders yet): a header row (focus marker + label, plus a "[a-b of n]"
// scroll indicator once the listing doesn't fit windowHeight), an
// optional Find status row, then up to windowHeight rows -- a
// scrolling window kept centered on the cursor (scrollWindow) so a
// listing longer than one screenful still lets the operator reach
// every entry instead of the box silently growing past the bottom of
// the terminal. Cursor/tag rows get the ">"/"*" markers DESIGN.md
// 21.4/21.6 call for, plus reverse-video/bold (styleRow) so the
// selected/tagged rows are visually unambiguous even without reading
// the markers.
func paneRows(p *pane, focused, colorEnabled, loading bool, spin string, windowHeight int) []string {
	var rows []string

	visible := p.visible()
	start, end := scrollWindow(p.cursor, len(visible), windowHeight)

	headerMarker := "  "
	if focused {
		headerMarker = "> "
	}
	header := headerMarker + p.label()
	if len(visible) > windowHeight {
		header += fmt.Sprintf("  [%d-%d of %d]", start+1, end, len(visible))
	}
	if loading {
		header += "  " + spin + " Loading..."
	}
	rows = append(rows, header)

	if p.find != nil {
		findLine := findStatusText(p.find)
		if !p.find.done {
			findLine = spin + " " + findLine
		}
		rows = append(rows, findLine)
	}

	if len(visible) == 0 {
		rows = append(rows, "(empty)")
	}
	for i := start; i < end; i++ {
		e := visible[i]
		isCursor := i == p.cursor
		_, isTagged := p.tagged[e.key]

		cursor := " "
		if isCursor {
			cursor = ">"
		}
		tag := " "
		if isTagged {
			tag = "*"
		}
		name := displayName(e)
		if p.find != nil {
			name = e.name // Find rows show the path relative to the search base already
		}

		var row string
		if e.kind == kindFile {
			row = fmt.Sprintf("%s%s %-30s %10s", cursor, tag, name, formatBytes(e.size))
		} else {
			row = fmt.Sprintf("%s%s %s", cursor, tag, name)
		}
		rows = append(rows, styleRow(row, isCursor, isTagged, colorEnabled))
	}
	return rows
}

// scrollWindow returns the [start, end) slice bounds of total items to
// show in a windowHeight-tall viewport, keeping cursor visible and
// roughly centered when there's room to do so -- the same "cursor stays
// on screen, view scrolls around it" behavior every pageable list
// (mc, ranger, less) uses.
func scrollWindow(cursor, total, windowHeight int) (start, end int) {
	if windowHeight <= 0 {
		windowHeight = 1
	}
	if total <= windowHeight {
		return 0, total
	}
	start = cursor - windowHeight/2
	start = max(start, 0)
	start = min(start, total-windowHeight)
	return start, start + windowHeight
}

// styleRow applies reverse-video to the cursor row (the unambiguous
// "this is the selected row" signal every file manager -- mc, ranger,
// WinSCP -- uses) and bold to a tagged row, on top of the ">"/"*"
// text markers already in the row. Gated by colorEnabled -- this
// project's existing NO_COLOR/non-TTY convention (internal/ui.
// ColorEnabled) -- rather than emitted unconditionally, even though
// reverse/bold are text decorations rather than colors; the ">"/"*"
// text markers alone are the plain-output fallback.
func styleRow(row string, isCursor, isTagged, colorEnabled bool) string {
	if !colorEnabled {
		return row
	}
	switch {
	case isCursor:
		return reverseVideo(row)
	case isTagged:
		return bold(row)
	default:
		return row
	}
}

const (
	ansiReverse = "\033[7m"
	ansiBold    = "\033[1m"
	ansiReset   = "\033[0m"
)

func reverseVideo(s string) string { return ansiReverse + s + ansiReset }
func bold(s string) string         { return ansiBold + s + ansiReset }

func renderStatusLineText(p *pane) string {
	filter := "none"
	if p.filter != "" {
		filter = p.filter
	}
	return fmt.Sprintf("%d item(s), %d tagged (%s)  filter: %s",
		len(p.visible()), len(p.tagged), formatBytes(p.taggedSize()), filter)
}

func renderCommandLineText(m *Model) string {
	if m.cmdPrefix == 0 {
		return ":"
	}
	return string(m.cmdPrefix) + " " + m.cmdBuf + "█"
}

func renderHotkeyLegendText(m *Model) string {
	if m.local == nil {
		return "d Download  x Delete  m Metadata  f Filter  F Find  r Refresh  l Link (2-pane)  Space Tag  * Tag All  q Quit"
	}
	return "u Upload  d Download  x Delete  m Metadata  f Filter  F Find  S Sync  r Refresh  l Unlink (1-pane)  Tab Switch  Space Tag  * Tag All  q Quit"
}

// overlayLines renders the modal progress/confirm content (DESIGN.md
// 21.4) as plain text rows, boxed the same way as everything else.
// overlayLines renders overlay content, tail-windowed to at most
// maxLines -- a long-running Upload/Download/Delete/Sync against many
// objects can produce more progress lines than fit on screen, the same
// class of "grows the box past the bottom of the terminal" bug the
// pane listing had (scrollWindow above); a progress log's natural
// reading position is its most recent lines, not a scroll-to-cursor
// window, so this always keeps the tail rather than centering on
// anything.
func overlayLines(o *overlay, maxLines int) []string {
	var lines []string
	switch o.kind {
	case overlayConfirm:
		lines = append(lines, o.title+" [y/N]")
	case overlayConfirmDestructive:
		lines = append(lines, o.title)
		lines = append(lines, "> "+o.destructiveInput+"█")
	case overlayProgress:
		lines = append(lines, o.title)
		lines = append(lines, o.lines...)
		if o.done {
			lines = append(lines, "(press any key to continue)")
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		skipped := len(lines) - maxLines
		lines = append([]string{fmt.Sprintf("... (%d earlier line(s) not shown)", skipped)}, lines[skipped+1:]...)
	}
	return lines
}

// --- box drawing -----------------------------------------------------
//
// All width math is in runes, not bytes: the title/content can contain
// multi-byte-but-single-column characters (the em dash in "clasm — S3
// File Manager", the box-drawing characters themselves).

func runeLen(s string) int { return utf8.RuneCountInString(stripANSI(s)) }

// stripANSI removes the SGR escape sequences styleRow adds, so width
// math (padding/truncation) is computed against the *visible* text --
// otherwise a styled row would be padded short by len(ansiReverse)+
// len(ansiReset) worth of invisible bytes.
func stripANSI(s string) string {
	if !strings.Contains(s, "\033[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// padOrTruncate pads s with spaces to exactly width visible runes, or
// truncates it, without corrupting any ANSI styling s already carries.
func padOrTruncate(s string, width int) string {
	if width < 0 {
		width = 0
	}
	visible := runeLen(s)
	if visible == width {
		return s
	}
	if visible < width {
		return s + strings.Repeat(" ", width-visible)
	}
	// Truncate by visible rune count; if s carries ANSI styling, close
	// it out so the escape doesn't bleed into the following box border.
	truncated := truncateVisible(s, width)
	if strings.Contains(s, "\033[") {
		truncated += ansiReset
	}
	return truncated
}

func truncateVisible(s string, width int) string {
	if !strings.Contains(s, "\033[") {
		runes := []rune(s)
		if len(runes) <= width {
			return s
		}
		return string(runes[:width])
	}
	var b strings.Builder
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			b.WriteString(s[i : j+1])
			i = j
			continue
		}
		if count >= width {
			continue
		}
		b.WriteByte(s[i])
		count++
	}
	return b.String()
}

func topBorder(title string, inner int) string {
	fill := inner - runeLen(title)
	if fill < 1 {
		return "┌" + padOrTruncate(title, inner) + "┐\n"
	}
	return "┌" + title + strings.Repeat("─", fill) + "┐\n"
}

func bottomBorder(inner int) string {
	return "└" + strings.Repeat("─", inner) + "┘\n"
}

func divider(inner int) string {
	return "├" + strings.Repeat("─", inner) + "┤\n"
}

// splitWidths divides inner between the two panes, reserving 5 columns
// for "│ " + " │ " + " │"'s non-content characters around the two cells
// plus the middle divider.
func splitWidths(inner int) (left, right int) {
	content := max(inner-5, 2)
	left = content / 2
	right = content - left
	return left, right
}

func splitDivider(leftW, rightW int) string {
	return "├" + strings.Repeat("─", leftW+2) + "┬" + strings.Repeat("─", rightW+2) + "┤\n"
}

func mergeDivider(leftW, rightW int) string {
	return "├" + strings.Repeat("─", leftW+2) + "┴" + strings.Repeat("─", rightW+2) + "┤\n"
}

func boxLine(content string, inner int) string {
	return "│ " + padOrTruncate(content, inner-2) + " │\n"
}

func boxRow2(left, right string, leftW, rightW int) string {
	return "│ " + padOrTruncate(left, leftW) + " │ " + padOrTruncate(right, rightW) + " │\n"
}
