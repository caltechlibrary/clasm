// Package config loads clasm' own operational settings -- never AWS
// credentials or profile selection, which remain entirely the AWS SDK's
// responsibility via its standard chain (~/.aws/credentials,
// ~/.aws/config, environment variables, SSO) -- from an optional YAML
// file at ~/.clasm. See DESIGN.md, "Configuration", and DECISIONS.md,
// "Add a ~/.awsops YAML config file for awsops' own operational
// settings".
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultRegions is used when no config file exists, or one exists but
// doesn't set Regions -- see DECISIONS.md, "Narrow configured regions
// to us-west-1/us-west-2".
var DefaultRegions = []string{"us-west-1", "us-west-2"}

// Config is clasm' own settings, loaded from an optional YAML file.
// One field per setting; a new setting is added here, given a default
// below, and wired into whatever consumes it -- no versioning or
// migration machinery, appropriate for a single-operator-maintained
// local dotfile.
type Config struct {
	Regions           []string              `yaml:"regions"`
	BackupDirectories []BackupDirectoryRule `yaml:"backup_directories"`
}

// BackupDirectoryRule maps a glob-style pattern (path.Match syntax: *,
// ?, [...]), matched against an instance's Name tag, to the backup
// directory Backup Archive & Trim (DESIGN.md, Feature 11) should
// default to for matching instances -- e.g. RDM instances and some
// other service's instances keep their backups in different
// directories, and typing the right path from memory every run invites
// mistakes.
type BackupDirectoryRule struct {
	Pattern   string `yaml:"pattern"`
	Directory string `yaml:"directory"`
}

// BackupDirectoryFor returns the Directory of the first rule in rules
// whose Pattern matches instanceName, checked in list order -- or "" if
// none match, instanceName is empty (an untagged instance has nothing
// to match), or a malformed pattern errors out of path.Match (treated
// the same as no match, not a fatal error, since a typo'd pattern
// should degrade to the ordinary prompt, not crash the workflow).
func BackupDirectoryFor(rules []BackupDirectoryRule, instanceName string) string {
	if instanceName == "" {
		return ""
	}
	for _, rule := range rules {
		if ok, err := path.Match(rule.Pattern, instanceName); err == nil && ok {
			return rule.Directory
		}
	}
	return ""
}

// DefaultPath returns ~/.clasm, falling back to a cwd-relative
// ".clasm" if the home directory can't be resolved, matching this
// project's existing sshKeyDir() fallback pattern (internal/workflow/
// create_key_pair.go) rather than failing the whole program over it.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".clasm"
	}
	return filepath.Join(home, ".clasm")
}

// Load reads and parses the YAML config file at path. A missing file is
// not an error -- the config file is entirely optional, and built-in
// defaults apply. A malformed file is a real error: a botched config
// silently ignored could mask a real mistake (e.g. a typo'd region)
// behind confusing "why isn't my region showing up" behavior. A field
// left unset in an otherwise-valid file falls back to its own default
// independently, not all-or-nothing.
func Load(path string) (Config, error) {
	cfg := Config{Regions: DefaultRegions}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cfg.Regions) == 0 {
		cfg.Regions = DefaultRegions
	}
	return cfg, nil
}
