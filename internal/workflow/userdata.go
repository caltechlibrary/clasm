// Package workflow implements the interactive, confirmation-gated
// operations awsops exposes: instance/AMI lifecycle, tag management,
// cloud-init inspection, and backup archive & trim.
package workflow

import (
	"fmt"
	"os"
	"strings"
)

// loadUserData resolves the raw text a user-data prompt collected: an
// "@"-prefixed path loads that file's contents (e.g. a file from a local
// clone of cloud-init-examples); anything else, including the empty
// string, is used as literal inline text -- see DESIGN.md, "Enhance
// Create Instance from AMI: cloud-init file input + completion check".
func loadUserData(input string) (string, error) {
	path, isFile := strings.CutPrefix(input, "@")
	if !isFile {
		return input, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading user-data file %q: %w", path, err)
	}
	return string(data), nil
}
