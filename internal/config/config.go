// Package config loads awsops' own operational settings -- never AWS
// credentials or profile selection, which remain entirely the AWS SDK's
// responsibility via its standard chain (~/.aws/credentials,
// ~/.aws/config, environment variables, SSO) -- from an optional YAML
// file at ~/.awsops. See DESIGN.md, "Configuration", and DECISIONS.md,
// "Add a ~/.awsops YAML config file for awsops' own operational
// settings".
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultRegions is used when no config file exists, or one exists but
// doesn't set Regions -- see DECISIONS.md, "Narrow configured regions
// to us-west-1/us-west-2".
var DefaultRegions = []string{"us-west-1", "us-west-2"}

// Config is awsops' own settings, loaded from an optional YAML file.
// One field per setting; a new setting is added here, given a default
// below, and wired into whatever consumes it -- no versioning or
// migration machinery, appropriate for a single-operator-maintained
// local dotfile.
type Config struct {
	Regions []string `yaml:"regions"`
}

// DefaultPath returns ~/.awsops, falling back to a cwd-relative
// ".awsops" if the home directory can't be resolved, matching this
// project's existing sshKeyDir() fallback pattern (internal/workflow/
// create_key_pair.go) rather than failing the whole program over it.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".awsops"
	}
	return filepath.Join(home, ".awsops")
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
