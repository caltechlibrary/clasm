package workflow

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// displayConfig prints the working copy's regions, backup directory
// rules, and Origin tag settings (DESIGN.md, "Configure clasm Domain",
// "Show current config").
func displayConfig(w io.Writer, cfg config.Config) {
	fmt.Fprintln(w, "\nCurrent configuration:")
	displayRegionsList(w, cfg.Regions)
	displayBackupDirectoryRulesList(w, cfg.BackupDirectories)
	fmt.Fprintf(w, "Origin tag key:             %s\n", cfg.OriginTag.Key)
	fmt.Fprintf(w, "Origin tag DLD-owned value: %s\n", displayOrNone(cfg.OriginTag.DLDValue))
}

func displayRegionsList(w io.Writer, regions []string) {
	if len(regions) == 0 {
		fmt.Fprintln(w, "No regions configured.")
		return
	}
	fmt.Fprintf(w, "Regions: %s\n", strings.Join(regions, ", "))
}

func backupDirectoryRuleLabel(r config.BackupDirectoryRule) string {
	return fmt.Sprintf("%s -> %s", r.Pattern, r.Directory)
}

func displayBackupDirectoryRulesList(w io.Writer, rules []config.BackupDirectoryRule) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No backup directory rules configured.")
		return
	}
	fmt.Fprintln(w, "Backup directory rules (first match wins):")
	for _, r := range rules {
		fmt.Fprintf(w, "  %s\n", backupDirectoryRuleLabel(r))
	}
}

// editRegionsChoices is the Edit regions sub-menu, in order. "Done"
// returns to the Configuration menu -- deliberately not 'q' (which would
// exit the whole domain) since this is a nested, bounded loop.
var editRegionsChoices = []string{"Add a region", "Remove a region", "Done"}

// editRegions lets the operator add/remove entries in cfg.Regions
// in place, looping until "Done" or a cancellation. Returns whether
// anything actually changed, so the caller can set its own dirty flag
// (DESIGN.md, "Configure clasm Domain").
func editRegions(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	changed := false
	for {
		displayRegionsList(w, cfg.Regions)
		action, err := pickString(w, "Edit regions", "Region changes take effect the next time clasm is launched.", hintGoBack, editRegionsChoices, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch action {
		case "Add a region":
			region, err := ui.Prompt("New region (e.g. us-west-1)", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			region = strings.TrimSpace(region)
			if region == "" {
				fmt.Fprintln(w, "No region entered.")
				continue
			}
			cfg.Regions = append(cfg.Regions, region)
			changed = true
		case "Remove a region":
			if len(cfg.Regions) == 0 {
				fmt.Fprintln(w, "No regions to remove.")
				continue
			}
			region, err := pickString(w, "Remove which region?", "", hintGoBack, cfg.Regions, input, output)
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			if idx := slices.Index(cfg.Regions, region); idx >= 0 {
				cfg.Regions = slices.Delete(cfg.Regions, idx, idx+1)
				changed = true
			}
		case "Done":
			return changed, nil
		}
	}
}

// editBackupDirChoices is the Edit backup directory rules sub-menu, same
// bounded-loop shape as editRegionsChoices.
var editBackupDirChoices = []string{"Add a rule", "Remove a rule", "Done"}

// editBackupDirectoryRules lets the operator add/remove entries in
// cfg.BackupDirectories in place, appending new rules to the end
// (first-match-wins order, per config.BackupDirectoryFor).
func editBackupDirectoryRules(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	changed := false
	for {
		displayBackupDirectoryRulesList(w, cfg.BackupDirectories)
		action, err := pickString(w, "Edit backup directory rules", "", hintGoBack, editBackupDirChoices, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch action {
		case "Add a rule":
			pattern, err := ui.Prompt(`Glob pattern (matched against an instance's Name tag, e.g. "rdm-*")`, ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				fmt.Fprintln(w, "No pattern entered.")
				continue
			}
			directory, err := ui.Prompt("Backup directory", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			directory = strings.TrimSpace(directory)
			if directory == "" {
				fmt.Fprintln(w, "No directory entered.")
				continue
			}
			cfg.BackupDirectories = append(cfg.BackupDirectories, config.BackupDirectoryRule{Pattern: pattern, Directory: directory})
			changed = true
		case "Remove a rule":
			if len(cfg.BackupDirectories) == 0 {
				fmt.Fprintln(w, "No rules to remove.")
				continue
			}
			labels := make([]string, len(cfg.BackupDirectories))
			for i, r := range cfg.BackupDirectories {
				labels[i] = backupDirectoryRuleLabel(r)
			}
			label, err := pickString(w, "Remove which rule?", "", hintGoBack, labels, input, output)
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			if idx := slices.Index(labels, label); idx >= 0 {
				cfg.BackupDirectories = slices.Delete(cfg.BackupDirectories, idx, idx+1)
				changed = true
			}
		case "Done":
			return changed, nil
		}
	}
}

// editOriginTag prompts for cfg.OriginTag's Key and DLDValue, pre-filled
// with the working copy's current values. A blank key falls back to
// config.DefaultOriginTagKey, matching config.Load's own per-field
// default behavior.
func editOriginTag(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	key, err := ui.Prompt("Origin tag key", ui.WithDefault(cfg.OriginTag.Key), ui.WithIO(input, output))
	if err != nil {
		return false, cancelledIsNil(w, err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = config.DefaultOriginTagKey
	}

	dldValue, err := ui.Prompt(`Origin tag value meaning "DLD-owned" (blank = none recognized yet)`, ui.WithDefault(cfg.OriginTag.DLDValue), ui.WithIO(input, output))
	if err != nil {
		return false, cancelledIsNil(w, err)
	}

	changed := key != cfg.OriginTag.Key || dldValue != cfg.OriginTag.DLDValue
	cfg.OriginTag.Key = key
	cfg.OriginTag.DLDValue = dldValue
	return changed, nil
}

// warnIfDirtyOnQuit prints an unsaved-changes warning if dirty is true --
// separated from the pipe-driven quit path itself so it's directly
// testable without needing to simulate a real accessible-mode
// cancellation (huh's accessible mode never reliably surfaces an error
// on exhausted scripted input, the same gotcha that requires every
// looping accessible-mode workflow in this codebase to be tested via
// explicit ctx cancellation instead -- see runConfigureMenu's own tests).
func warnIfDirtyOnQuit(w io.Writer, dirty bool) {
	if dirty {
		fmt.Fprintln(w, "Unsaved changes will be discarded.")
	}
}
