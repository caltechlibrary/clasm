// Package tui provides shared terminal-UI chrome -- box drawing, text
// measurement/truncation, cursor-following scroll windows, and
// cursor/tag row styling -- extracted from internal/filemanager so
// every bubbletea-based screen in clasm shares one implementation
// instead of each maintaining its own copy (DESIGN.md, "Terminal UI
// Architecture: Menus, Actions, Lists, and Managers").
//
// All width math is in runes, not bytes: content can contain
// multi-byte-but-single-column characters (an em dash in a title, the
// box-drawing characters themselves).
package tui

import (
	"strings"
	"unicode/utf8"
)

// RuneLen returns s's visible width in runes, ignoring any ANSI SGR
// styling it carries.
func RuneLen(s string) int { return utf8.RuneCountInString(StripANSI(s)) }

// StripANSI removes SGR escape sequences (as added by StyleRow), so
// width math (padding/truncation) is computed against the *visible*
// text -- otherwise a styled row would be padded short by the escape
// sequences' own byte length.
func StripANSI(s string) string {
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

// PadOrTruncate pads s with spaces to exactly width visible runes, or
// truncates it, without corrupting any ANSI styling s already carries.
func PadOrTruncate(s string, width int) string {
	if width < 0 {
		width = 0
	}
	visible := RuneLen(s)
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

// TopBorder renders a box's top border with title embedded, e.g.
// "┌ my title ────┐". If title is wider than inner, it's truncated to
// fit rather than widening the box.
func TopBorder(title string, inner int) string {
	fill := inner - RuneLen(title)
	if fill < 1 {
		return "┌" + PadOrTruncate(title, inner) + "┐\n"
	}
	return "┌" + title + strings.Repeat("─", fill) + "┐\n"
}

// BottomBorder renders a box's bottom border.
func BottomBorder(inner int) string {
	return "└" + strings.Repeat("─", inner) + "┘\n"
}

// Divider renders a horizontal rule between two box sections.
func Divider(inner int) string {
	return "├" + strings.Repeat("─", inner) + "┤\n"
}

// SplitDivider renders the divider used above a two-column split (a
// "┬" where the columns begin).
func SplitDivider(leftW, rightW int) string {
	return "├" + strings.Repeat("─", leftW+2) + "┬" + strings.Repeat("─", rightW+2) + "┤\n"
}

// MergeDivider renders the divider used below a two-column split (a
// "┴" where the columns end).
func MergeDivider(leftW, rightW int) string {
	return "├" + strings.Repeat("─", leftW+2) + "┴" + strings.Repeat("─", rightW+2) + "┤\n"
}

// BoxLine renders one full-width row of box content.
func BoxLine(content string, inner int) string {
	return "│ " + PadOrTruncate(content, inner-2) + " │\n"
}

// BoxRow2 renders one row split into two side-by-side cells.
func BoxRow2(left, right string, leftW, rightW int) string {
	return "│ " + PadOrTruncate(left, leftW) + " │ " + PadOrTruncate(right, rightW) + " │\n"
}
