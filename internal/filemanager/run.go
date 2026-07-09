package filemanager

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Config configures one "Browse & Manage Objects" session (DESIGN.md
// 21.3): Bucket/Region come from the huh pre-flight's bucket selection,
// LocalDir is "" for single-pane mode or a validated directory for
// double-pane mode.
type Config struct {
	Client   awsclient.S3API
	Bucket   string
	Region   string
	LocalDir string
}

// Run launches the file manager screen as a scoped bubbletea.Program
// (DESIGN.md 21.8) and blocks until the operator quits (`q`,
// :quit, or Ctrl+C). This is the file manager's only entry point --
// internal/workflow's huh pre-flight (object_browser.go) calls this
// after collecting cfg.
func Run(ctx context.Context, cfg Config) error {
	m := New(ctx, cfg.Client, cfg.Bucket, cfg.Region, cfg.LocalDir)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
