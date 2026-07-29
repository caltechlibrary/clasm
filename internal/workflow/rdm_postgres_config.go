package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
)

// dockerPSCommand lists every running container's image and name,
// tab-separated -- filtering for a Postgres image happens client-side
// (isPostgresImage), not via Docker's own `--filter ancestor=`, which
// was confirmed against real CaltechAUTHORS production (2026-07-29) to
// NOT match a bare repository name (e.g. "postgres") against a specific
// tag (e.g. "postgres:14.13") -- it returned zero results even with
// caltechauthors-db-1 confirmed running. Matches ListBackupFiles' own
// precedent of filtering locally rather than trusting a remote filter to
// do semantic work. See DECISIONS.md, "docker ps --filter
// ancestor=postgres doesn't match a tagged image -- filter client-side
// instead."
const dockerPSCommand = `docker ps --format '{{.Image}}\t{{.Names}}'`

// isPostgresImage reports whether image (docker ps's own {{.Image}}
// column) is the official Postgres image, any tag -- "postgres",
// "postgres:<tag>", "postgres@<digest>", or any of those prefixed with a
// registry/path (e.g. "docker.io/library/postgres:14.13") -- but not an
// image that merely contains "postgres" as a substring of a different
// repository name (e.g. a hypothetical "my-postgres-exporter").
func isPostgresImage(image string) bool {
	if idx := strings.LastIndex(image, "/"); idx != -1 {
		image = image[idx+1:]
	}
	return image == "postgres" || strings.HasPrefix(image, "postgres:") || strings.HasPrefix(image, "postgres@")
}

// DefaultRDMPostgresDiscoveryTimeout bounds discoverPostgresContainer's
// SSM round trip -- docker ps is fast, matching ListBackupFiles' own
// DefaultBackupListTimeout-scale bound.
const DefaultRDMPostgresDiscoveryTimeout = 2 * time.Minute

// discoverPostgresContainer runs dockerPSCommand via SSM on instanceID
// and returns the single running Postgres container's name (isPostgresImage).
// Zero or more than one result is a hard error, not a guess -- the raw
// command is included in the error text so the operator can investigate
// by hand (matching ResizeInstanceRootVolume's own manual-fallback
// precedent for an unrecognized layout).
func discoverPostgresContainer(ctx context.Context, client awsclient.SSMAPI, instanceID string, timeout, pollInterval time.Duration) (string, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, dockerPSCommand, timeout, pollInterval)
	if err != nil {
		return "", err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return "", fmt.Errorf("discovering the Postgres container on %s failed (status: %s) -- checked via: %s", instanceID, status, dockerPSCommand)
	}

	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		image, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if isPostgresImage(image) {
			names = append(names, name)
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no running Postgres container found on %s -- checked via: %s", instanceID, dockerPSCommand)
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("more than one running Postgres container found on %s (%s) -- resolve manually, checked via: %s", instanceID, strings.Join(names, ", "), dockerPSCommand)
	}
}

// resolveRDMPostgresConfig discovers instanceID's live Postgres
// container (discoverPostgresContainer), reconciles it against rules
// (config.UpsertRDMPostgresRule) and reports any new-or-changed
// container name via w, then resolves dbName/dbUser
// (config.RDMPostgresConfigFor) from the reconciled rules. instanceName
// is the Pattern-matching key (an EC2 Name tag, same convention as
// config.BackupDirectoryFor); fallbackIdentifier is what dbName/dbUser
// actually default to when no override matches -- deliberately a
// separate value, since a real 2026-07-29 incident confirmed an
// instance's Name tag can be a legacy label unrelated to its actual RDM
// project shortname (its Project tag, in that case) -- see DECISIONS.md,
// "Default db_name/db_user to the instance's Project tag, not its Name
// tag." Persistence itself (config.Save) is the caller's job -- both Run
// SQL Backup and Restore SQL Backup own their own ~/.clasm path and
// existing config-loading pattern; a config-write failure is a
// convenience lost, not a reason to abort the backup/restore itself
// (same spirit as BackupHistory.Save).
func resolveRDMPostgresConfig(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID, instanceName, fallbackIdentifier string, rules []config.RDMPostgresRule, timeout, pollInterval time.Duration) (containerName, dbName, dbUser string, updatedRules []config.RDMPostgresRule, err error) {
	containerName, err = discoverPostgresContainer(ctx, client, instanceID, timeout, pollInterval)
	if err != nil {
		return "", "", "", nil, err
	}

	oldContainerName, _, _ := config.RDMPostgresConfigFor(rules, instanceName, fallbackIdentifier)
	updatedRules = config.UpsertRDMPostgresRule(rules, instanceName, containerName)
	switch {
	case oldContainerName == "":
		fmt.Fprintf(w, "Discovered and saved Postgres container name for %s: %s\n", instanceName, containerName)
	case oldContainerName != containerName:
		fmt.Fprintf(w, "Postgres container name for %s changed: %s -> %s (updating rdm_postgres_config)\n", instanceName, oldContainerName, containerName)
	}

	_, dbName, dbUser = config.RDMPostgresConfigFor(updatedRules, instanceName, fallbackIdentifier)
	return containerName, dbName, dbUser, updatedRules, nil
}
