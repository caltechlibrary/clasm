// Package workflow implements the interactive, confirmation-gated
// operations awsops exposes: instance/AMI lifecycle, tag management,
// cloud-init inspection, and backup archive & trim.
package workflow

import (
	"fmt"
	"os"
	"strings"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// loadUserData resolves the raw text a user-data prompt collected: an
// "@"-prefixed path loads that file's contents (e.g. a file from a local
// clone of cloud-init-examples); anything else, including the empty
// string, is used as literal inline text -- see DESIGN.md, "Enhance
// Create Instance from AMI: cloud-init file input + completion check".
//
// If input has no "@" prefix but names a file that actually exists
// (relative to the current directory, or absolute), it's loaded anyway
// with an on-screen note. A bare filename is never valid literal
// cloud-init YAML/user-data, so treating "newt-machine.yaml" (forgotten
// "@") as literal inline text launches the instance with that filename
// string as its user-data instead of the file's actual contents -- a
// silent, confusing failure mode found in real use. See DECISIONS.md,
// "Auto-detect a bare existing-file path in User data / Cloud-init
// YAML input".
func loadUserData(t *termlib.Terminal, input string) (string, error) {
	path, isFile := strings.CutPrefix(input, "@")
	if !isFile {
		info, err := os.Stat(input)
		if err != nil || info.IsDir() {
			return input, nil
		}
		path = input
		t.Printf("%q looks like an existing file -- loading its contents (prefix with \"@\" to make this explicit next time)\n", input)
		t.Refresh()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading user-data file %q: %w", path, err)
	}
	return string(data), nil
}

// promptCloudInitYAMLFile prompts for a cloud-init YAML file and returns
// its contents. Unlike Feature 2's optional "User data" field (which
// shares loadUserData above and supports genuine inline text), this
// prompt is Feature 3's whole reason for existing -- real cloud-init
// YAML is realistically always a file, never something typed inline at
// a terminal -- so it always reads from disk instead of also supporting
// inline text, and re-prompts on a missing/unreadable file instead of
// silently treating the value as literal text (see DECISIONS.md,
// "Create EC2 Instance from Cloud-Init YAML always reads from a file").
// A leading "@" is tolerated (muscle memory from Feature 2's prompt) but
// not required.
func promptCloudInitYAMLFile(t *termlib.Terminal, le *termlib.LineEditor) (string, error) {
	for {
		raw, err := ui.Prompt(t, le, "Cloud-init YAML file path", ui.WithValidator(requireNonEmpty))
		if err != nil {
			return "", err
		}
		path := strings.TrimPrefix(raw, "@")

		data, err := os.ReadFile(path)
		if err != nil {
			t.Printf("invalid input: cannot read %q: %v\n", path, err)
			t.Refresh()
			continue
		}
		return string(data), nil
	}
}
