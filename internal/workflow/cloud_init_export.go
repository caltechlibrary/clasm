package workflow

import (
	"fmt"
	"io"
	"os"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// exportCloudInit offers to save the decoded cloud-init YAML to a local
// file, for manual comparison against a local clone of
// caltechlibrary/cloud-init-examples (see DESIGN.md, Feature 10, "no
// inline fetch-and-diff against the GitHub repo in v1"). A blank path
// skips the export cleanly.
func exportCloudInit(w io.Writer, userData string, input io.Reader, output io.Writer) error {
	path, err := ui.Prompt("Save to file (blank to skip)", ui.WithIO(input, output))
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	if err := os.WriteFile(path, []byte(userData), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(w, "Saved to %s\n", path)
	return nil
}
