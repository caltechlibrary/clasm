package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTheme_NonNilAndCarriesSharedAccent(t *testing.T) {
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil")
	}
	if got := th.Focused.Title.GetForeground(); got != accentColor {
		t.Errorf("Focused.Title foreground = %v, want shared accentColor %v", got, accentColor)
	}
	if got, want := th.Blurred.Base.GetBorderStyle(), lipgloss.HiddenBorder(); got != want {
		t.Errorf("Blurred.Base border style = %+v, want hidden border %+v", got, want)
	}
}
