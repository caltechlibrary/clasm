package config

import (
	"errors"
	"fmt"
	"path"
)

// The host kinds a rule can name (DR-0206). A rule with no host is an AWS rule.
const (
	HostAWS       = "aws"
	HostMultipass = "multipass"
)

// CurrentVersion is the config_version this binary writes and understands.
// Version 1 is every file written before the field existed.
const CurrentVersion = 2

var (
	// ErrNewerConfig is returned when the file's config_version is newer than
	// this binary understands. It is a configuration error (exit 78).
	ErrNewerConfig = errors.New("config file is newer than this program")
	// ErrInvalidConfig is returned when the file is readable but wrong, such as
	// a rule naming an unknown host. It is a configuration error (exit 78).
	ErrInvalidConfig = errors.New("config file is invalid")
)

func hostMatches(ruleHost, want string) bool {
	if ruleHost == "" {
		ruleHost = HostAWS
	}
	return ruleHost == want
}

// validateHosts refuses a rule that names a host kind this program does not know.
func validateHosts(path string, cfg Config) error {
	check := func(list, host string, i int) error {
		if host != "" && host != HostAWS && host != HostMultipass {
			return fmt.Errorf("%w: %s: %s[%d] has host %q; use %q or %q", ErrInvalidConfig, path, list, i, host, HostAWS, HostMultipass)
		}
		return nil
	}
	for i, r := range cfg.BackupDirectories {
		if err := check("backup_directories", r.Host, i); err != nil {
			return err
		}
	}
	for i, r := range cfg.OpenSearchBackupDirectories {
		if err := check("opensearch_backup_directories", r.Host, i); err != nil {
			return err
		}
	}
	for i, r := range cfg.RDMPostgresConfig {
		if err := check("rdm_postgres_config", r.Host, i); err != nil {
			return err
		}
	}
	return nil
}

// BackupDirectoryForHost returns the Directory of the first rule for the given
// host kind whose Pattern matches instanceName, in list order, or "" if none
// does. A rule with no host is an AWS rule. A rule for another host is never
// considered, so a Multipass rule cannot reach clasm's restore code.
//
// @param rules {[]BackupDirectoryRule} the configured rules
// @param host {string} HostAWS or HostMultipass
// @param instanceName {string} the instance or VM name to match
// @returns {string} the directory, or "" for no match
// @example
//
//	dir := BackupDirectoryForHost(cfg.BackupDirectories, HostMultipass, "caltechauthors-rsdoiel-v14")
func BackupDirectoryForHost(rules []BackupDirectoryRule, host, instanceName string) string {
	if instanceName == "" {
		return ""
	}
	for _, rule := range rules {
		if !hostMatches(rule.Host, host) {
			continue
		}
		if ok, err := path.Match(rule.Pattern, instanceName); err == nil && ok {
			return rule.Directory
		}
	}
	return ""
}

// RDMPostgresConfigForHost is RDMPostgresConfigFor restricted to one host
// kind: the first matching rule for that host wins, and rules for other hosts
// are never considered.
//
// @param rules {[]RDMPostgresRule} the configured rules
// @param host {string} HostAWS or HostMultipass
// @param instanceName {string} the instance or VM name to match
// @param fallbackIdentifier {string} used for a blank db_name or db_user
// @returns {string} the container name, blank when unknown
// @returns {string} the database name
// @returns {string} the database user
// @example
//
//	c, db, user := RDMPostgresConfigForHost(cfg.RDMPostgresConfig, HostMultipass, "vm1", "caltechauthors")
func RDMPostgresConfigForHost(rules []RDMPostgresRule, host, instanceName, fallbackIdentifier string) (containerName, dbName, dbUser string) {
	dbName, dbUser = fallbackIdentifier, fallbackIdentifier
	for _, rule := range rules {
		if !hostMatches(rule.Host, host) {
			continue
		}
		if ok, err := path.Match(rule.Pattern, instanceName); err == nil && ok {
			containerName = rule.ContainerName
			if rule.DBName != "" {
				dbName = rule.DBName
			}
			if rule.DBUser != "" {
				dbUser = rule.DBUser
			}
			return containerName, dbName, dbUser
		}
	}
	return containerName, dbName, dbUser
}

// UpsertRDMPostgresRuleForHost is UpsertRDMPostgresRule restricted to one host
// kind: it updates only a rule of that host whose Pattern equals pattern, and
// otherwise appends a new rule carrying that host. It returns a new slice.
//
// @param rules {[]RDMPostgresRule} the configured rules
// @param host {string} HostAWS or HostMultipass
// @param pattern {string} the exact pattern to find or add
// @param containerName {string} the discovered container name
// @returns {[]RDMPostgresRule} the updated rules
// @example
//
//	rules = UpsertRDMPostgresRuleForHost(rules, HostMultipass, "vm1", "db-1")
func UpsertRDMPostgresRuleForHost(rules []RDMPostgresRule, host, pattern, containerName string) []RDMPostgresRule {
	for i, rule := range rules {
		if rule.Pattern == pattern && hostMatches(rule.Host, host) {
			if rule.ContainerName == containerName {
				return rules
			}
			updated := make([]RDMPostgresRule, len(rules))
			copy(updated, rules)
			updated[i].ContainerName = containerName
			return updated
		}
	}
	newRule := RDMPostgresRule{Pattern: pattern, ContainerName: containerName}
	if host == HostMultipass {
		newRule.Host = HostMultipass
	}
	return append(append([]RDMPostgresRule{}, rules...), newRule)
}

// ExitCode maps a Load error to the process exit code: 78 (DR-0014's "a
// configuration file is present but wrong") for a newer or invalid file, and
// 1 for anything else, which is the code clasm has always used here.
//
// @param err {error} the error from Load
// @returns {int} the exit code
// @example
//
//	os.Exit(config.ExitCode(err))
func ExitCode(err error) int {
	if errors.Is(err, ErrNewerConfig) || errors.Is(err, ErrInvalidConfig) {
		return 78
	}
	return 1
}
