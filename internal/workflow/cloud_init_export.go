package workflow

import (
	"fmt"
	"os"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// exportCloudInit offers to save the decoded cloud-init YAML to a local
// file, for manual comparison against a local clone of
// caltechlibrary/cloud-init-examples (see DESIGN.md, Feature 10, "no
// inline fetch-and-diff against the GitHub repo in v1"). A blank path
// skips the export cleanly.
func exportCloudInit(t *termlib.Terminal, le *termlib.LineEditor, userData string) error {
	path, err := ui.Prompt(t, le, "Save to file (blank to skip)")
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	if err := os.WriteFile(path, []byte(userData), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	t.Printf("Saved to %s\n", path)
	t.Refresh()
	return nil
}
