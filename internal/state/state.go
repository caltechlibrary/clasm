// Package state persists clasm's own runtime memory of what an
// operator previously did -- distinct from internal/config, which loads
// ~/.clasm, a file the operator hand-edits and this program only ever
// reads. This package's file, ~/.clasm_state, is exclusively
// app-managed: written by clasm itself, safe to delete, never meant for
// hand-editing (DECISIONS.md, "Persist Backup Archive & Trim's
// instance/directory choices in a separate app-managed state file").
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// State is clasm's locally-persisted runtime memory. One field per
// workflow that recalls previous choices; a new one is added here and
// wired into whatever workflow consumes it, same low-ceremony approach
// as config.Config.
type State struct {
	BackupArchive BackupArchiveState `yaml:"backup_archive"`
}

// BackupArchiveState is Backup Archive & Trim's remembered instance and
// per-instance backup directory, recalled as the instance picker's
// initial cursor position and the directory prompt's default on the
// next run -- keyed per-instance (DECISIONS.md, "Recall Backup Archive
// & Trim's instance/directory choices per-instance") since different
// instances plausibly back up different directories.
type BackupArchiveState struct {
	LastInstanceID          string            `yaml:"last_instance_id"`
	LastDirectoryByInstance map[string]string `yaml:"last_directory_by_instance"`
}

// DefaultPath returns ~/.clasm_state, falling back to a cwd-relative
// ".clasm_state" if the home directory can't be resolved -- matching
// config.DefaultPath's own fallback.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".clasm_state"
	}
	return filepath.Join(home, ".clasm_state")
}

// Load reads and parses the YAML state file at path. A missing file is
// not an error -- there's simply no history yet (first run, or the
// operator deleted it, both fine since this file is disposable). A
// malformed file is a real error, matching config.Load's own
// reasoning: silently ignoring it could mask a real problem instead of
// just starting fresh.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return s, nil
}

// Save writes s to path as YAML, creating or truncating the file.
func Save(path string, s State) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
