package tui

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// accentColor is the one color every screen in clasm shares -- the
// adaptive indigo huh's own default ThemeCharm uses for focused
// titles/borders, reused here (not reinvented) so every screen traces
// back to the same accent regardless of which UI tier it's built on
// (DECISIONS.md, "Chrome standardization: one shared indigo accent via
// lipgloss").
var accentColor = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7571F9"}

// borderStyle renders box.go's box-drawing characters in the shared
// accent.
var borderStyle = lipgloss.NewStyle().Foreground(accentColor)

// titleStyle renders a box's title text (TopBorder) in the shared
// accent, bold -- the same treatment Theme gives a focused huh field's
// title, so a List/Picker/Manager screen's banner and a Menu-tier
// huh.Select's title read as the same visual element.
var titleStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)

// Theme returns the huh.Theme every huh.NewForm(...) in clasm uses
// (DESIGN.md, "Chrome Standardization: A Shared lipgloss Palette"): a
// restrained subset of huh.ThemeCharm carrying only the shared indigo
// accent -- applied to focused titles, borders, the selection
// indicator/marker, and the confirm/submit button -- deliberately
// omitting ThemeCharm's fuchsia highlight, cream backgrounds, and
// green/red confirm-button colors, which suit a colorful consumer CLI
// better than an internal ops tool. Every clasm form is a single field
// in a single group (no multi-field forms exist in this codebase), so
// the blurred-state styling below is mostly theoretical, but is set to
// the same conventional shape huh's own themes use (hide the border)
// for correctness.
//
// Focused.Base gets a full four-sided lipgloss.NormalBorder() -- the
// same ┌─┐│ │└─┘ characters box.go draws -- replacing huh.ThemeBase's
// default left-only ThickBorder bar, so a huh field and a Picker/List/
// Manager screen read as the same kind of window, not two different
// visual languages (DECISIONS.md, "huh fields get a full box border to
// match tui's chrome"). Padding(0, 1) replaces ThemeBase's PaddingLeft
// (1) (which existed only to clear that one left bar) with balanced
// left/right breathing room, matching box.go's BoxLine's own "│
// content │" convention.
func Theme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = t.Focused.Base.
		Border(lipgloss.NormalBorder()).
		BorderForeground(accentColor).
		Padding(0, 1)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(accentColor).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(accentColor).Bold(true)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accentColor)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(accentColor)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(accentColor)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("255")).Background(accentColor)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accentColor)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(accentColor)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base

	t.Group.Title = t.Focused.Title
	return t
}

// SpinnerStyle returns the style a bubbles/spinner.Model should render
// its glyph in, so the one animated indicator in clasm (the progress
// ticker's inline spinner) uses the same shared accent as every box
// border and huh field (DESIGN.md, "Progress ticker becomes a real
// spinner").
func SpinnerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(accentColor)
}
