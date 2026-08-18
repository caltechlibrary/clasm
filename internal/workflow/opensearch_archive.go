package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// DefaultOpenSearchRepoName is the OpenSearch snapshot repository name
// Archive OpenSearch Snapshot to S3 registers/reuses on every instance --
// one fixed name, not per-instance-configurable, matching this project's
// existing "no unnecessary knobs" bias (DESIGN.md, "Archive OpenSearch
// Snapshot to S3").
const DefaultOpenSearchRepoName = "rdm_backup_repo"

// DefaultOpenSearchContainerRepoPath is the path OpenSearch's own
// `path.repo` setting allow-lists *inside the search container* --
// distinct from the operator-typed `directory` prompt below, which is a
// *host* path (used by `aws s3 sync`, which runs directly on the EC2
// host, outside any container). `rdm-opensearch-path-repo-retrofit.md`
// establishes this exact container-side path as the fixed convention for
// every retrofitted production instance (bind-mounting the operator's
// chosen host directory to this one, fixed, container path) -- a real
// bug, caught live 2026-07-29 against CaltechAUTHORS
// (i-0c4c81336aea33d27): registering the snapshot repo with the *host*
// directory as `location` fails OpenSearch's own path.repo check even
// once path.repo is correctly configured, since OpenSearch (running
// inside the container) has no visibility into host paths at all.
const DefaultOpenSearchContainerRepoPath = "/usr/share/opensearch/backups"

// DefaultOpenSearchSyncTimeout bounds the `aws s3 sync` SSM command --
// an ~8GB snapshot can legitimately take a while, so this gets a much
// longer bound than the quick repo/snapshot REST calls.
const DefaultOpenSearchSyncTimeout = 1 * time.Hour

// ArchiveOpenSearchSnapshot runs the full Archive OpenSearch Snapshot to
// S3 workflow (DESIGN.md, "Archive OpenSearch Snapshot to S3"; PLAN.md
// Phase 20.49): pick an instance, then delegate to the testable core.
func ArchiveOpenSearchSnapshot(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance, openSearchBackupDirRules []config.BackupDirectoryRule, hist BackupHistory) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstanceDefaulted(ctx, "Select an instance", "Connects to this instance via SSM to snapshot its OpenSearch indices and archive them to S3.", instances, hist.LastInstanceID)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return archiveOpenSearchSnapshot(ctx, w, ssmClients, s3Client, newS3Client, inst, openSearchBackupDirRules, hist, nil, nil)
}

// archiveOpenSearchSnapshot is ArchiveOpenSearchSnapshot's testable core,
// once an instance is resolved. input/output are nil in production and
// supplied by tests to drive every prompt/confirm in this function
// through its accessible-mode pipe path instead.
func archiveOpenSearchSnapshot(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, openSearchBackupDirRules []config.BackupDirectoryRule, hist BackupHistory, input io.Reader, output io.Writer) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	dirPromptOpts := []ui.PromptOption{ui.WithValidator(requireNonEmpty)}
	if def := hist.LastDirectoryByInstance[inst.InstanceID]; def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	} else if def := config.BackupDirectoryFor(openSearchBackupDirRules, inst.Name); def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	} else {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault("/opt/rdm_opensearch_backups"))
	}
	dirPromptOpts = append(dirPromptOpts, ui.WithIO(input, output))
	directory, err := ui.Prompt("OpenSearch backup directory (e.g. /opt/rdm_opensearch_backups)", dirPromptOpts...)
	if err != nil {
		return err
	}
	if hist.Save != nil {
		if err := hist.Save(inst.InstanceID, directory); err != nil {
			fmt.Fprintf(w, "warning: could not save backup history: %v\n", err)
		}
	}

	bucket, err := promptBackupBucketFunc(ctx, w, s3Client, newS3Client, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	bucketRegion, err := BucketRegion(ctx, s3Client, bucket)
	if err != nil {
		return err
	}
	bucketClient, err := newS3Client(ctx, bucketRegion)
	if err != nil {
		return err
	}
	if err := CheckS3BucketAccess(ctx, bucketClient, bucket); err != nil {
		return err
	}

	// Namespaces every archived snapshot by the source instance, same
	// convention as Feature 11's own upload prefix (backup_archive.go).
	// This is purely an S3 key-naming choice -- CaltechAUTHORS's Name tag
	// ("newauthors", a legacy label) is deliberately kept here even
	// though it differs from its Project tag (DECISIONS.md,
	// "CaltechAUTHORS's Name tag drives its S3 upload prefix, by
	// design").
	prefix := inst.Name
	if prefix == "" {
		prefix = inst.InstanceID
	}

	// Distinct from the S3 key prefix above: this is what OpenSearch
	// itself must match against real index names, via
	// rdmOpenSearchSnapshotIndexPatterns below. A real incident
	// (2026-08-17, CaltechAUTHORS production) found inst.Name silently
	// matching zero indices -- ignore_unavailable: true makes a wrong
	// pattern fail quietly, not loudly -- because this instance's real
	// index prefix is its Project tag ("caltechauthors"), not its Name
	// tag ("newauthors"). Same fix shape as Phase 20.52's Postgres
	// db_name/db_user defaulting (DECISIONS.md, "Default db_name/db_user
	// to the instance's Project tag, not its Name tag"): prefer
	// inst.Project, fall back to inst.Name only when Project is blank.
	indexPrefix := cmp.Or(inst.Project, prefix)

	cleanupDays, cleanupRequested, err := promptOpenSearchCleanupDays(input, output)
	if err != nil {
		return err
	}

	var toCleanup []SnapshotPrefixInfo
	if cleanupRequested {
		existing, err := ListArchivedSnapshotPrefixes(ctx, bucketClient, bucket, prefix)
		if err != nil {
			return err
		}
		candidates := FilterOlderThan(existing, cleanupDays, time.Now())
		if len(candidates) > 0 {
			displayCleanupDryRun(w, candidates)
			ok, err := ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(w, "Cancelled.")
				return nil
			}
			toCleanup = candidates
		}
	}

	if err := RegisterSnapshotRepo(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, DefaultOpenSearchContainerRepoPath, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	snapshotName := time.Now().UTC().Format("rdm-20060102-150405")
	indices := rdmOpenSearchSnapshotIndexPatterns(indexPrefix)
	if err := CreateSnapshot(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snapshotName, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if _, err := PollSnapshotUntilComplete(ctx, w, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snapshotName, DefaultSnapshotCreateTimeout, DefaultSnapshotPollInterval); err != nil {
		return err
	}

	if err := SyncOpenSearchBackupsToS3(ctx, ssmClient, inst.InstanceID, directory, bucket, prefix, snapshotName, DefaultOpenSearchSyncTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	objectCount, totalBytes, err := VerifySyncedSnapshot(ctx, bucketClient, bucket, prefix, snapshotName)
	if err != nil {
		return err
	}

	// The EBS-side delete is the tool's ordinary behavior every run,
	// unconditional -- not gated by the cleanup confirm above, which only
	// ever covers the S3-side cleanup below (DESIGN.md, "Archive
	// OpenSearch Snapshot to S3", step 9).
	if err := DeleteSnapshot(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snapshotName, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	var cleanedUp int
	if len(toCleanup) > 0 {
		if err := DeleteSnapshotPrefixes(ctx, bucketClient, bucket, prefix, toCleanup); err != nil {
			return err
		}
		cleanedUp = len(toCleanup)
	}

	fmt.Fprintf(w, "\nArchived OpenSearch snapshot %q: %d object(s), %d bytes synced to s3://%s/%s/opensearch-snapshots/%s/.\n", snapshotName, objectCount, totalBytes, bucket, prefix, snapshotName)
	if cleanedUp > 0 {
		fmt.Fprintf(w, "Removed %d old snapshot(s) older than %d days.\n", cleanedUp, cleanupDays)
	}
	return nil
}

// promptOpenSearchCleanupDays prompts for an optional cleanup threshold
// in days -- unlike promptAgeDays, a blank answer is valid and means
// "skip cleanup entirely for this run" (DESIGN.md, "Archive OpenSearch
// Snapshot to S3", step 3), so this is a new, distinct prompt function,
// not a reuse of promptAgeDays. The question text spells out exactly
// what this threshold governs -- a real user report (2026-07-29) found
// the original, terser wording ambiguous about whether it meant the
// instance's own local /opt directory or S3: this only ever deletes
// already-archived snapshot copies previously synced to S3 for this
// instance, never anything on the instance itself, and never the
// snapshot this run is about to create.
func promptOpenSearchCleanupDays(input io.Reader, output io.Writer) (days int, requested bool, err error) {
	raw, err := ui.Prompt("Delete this instance's previously-archived OpenSearch snapshots in S3 older than how many days? (blank to skip -- does not affect anything on the instance itself, or the snapshot this run is about to create)", ui.WithValidator(func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		n, convErr := strconv.Atoi(s)
		if convErr != nil || n <= 0 {
			return errors.New("must be blank (skip cleanup), or a positive integer")
		}
		return nil
	}), ui.WithIO(input, output))
	if err != nil {
		return 0, false, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, _ := strconv.Atoi(raw) // already validated above
	return n, true, nil
}
