package workflow

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// buildSyncFromS3Command builds the `aws s3 sync` command that downloads
// an already-archived OpenSearch snapshot's own S3 sub-prefix down into
// localDir on the target instance -- the opposite direction from
// SyncOpenSearchBackupsToS3 (opensearch_sync.go), same command-building
// shape (source and destination swapped).
func buildSyncFromS3Command(bucket, prefix, snapshotName, localDir string) string {
	src := fmt.Sprintf("s3://%s/%s/%s/", bucket, openSearchSnapshotsPrefix(prefix), snapshotName)
	return fmt.Sprintf("aws s3 sync --only-show-errors %s %s", shellQuote(src), shellQuote(localDir))
}

// SyncOpenSearchBackupsFromS3 runs buildSyncFromS3Command via SSM.
func SyncOpenSearchBackupsFromS3(ctx context.Context, client awsclient.SSMAPI, instanceID, bucket, prefix, snapshotName, localDir string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildSyncFromS3Command(bucket, prefix, snapshotName, localDir), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("syncing OpenSearch snapshot %q from s3://%s/%s on %s failed", snapshotName, bucket, openSearchSnapshotsPrefix(prefix), instanceID), status, stdout)
	}
	return nil
}

// buildListIndicesCommand builds the curl command that lists every index
// name on the target matching prefix's own two wildcard shapes --
// "<prefix>-*" (every ordinary curated pattern in
// rdmOpenSearchSnapshotIndexPatterns, including the one non-wildcard
// "-stats-bookmarks" entry, which is itself a match of this broader
// wildcard) and ".ds-<prefix>-*" (the audit-log data-stream pattern,
// prefixed differently). Both are wildcards, so OpenSearch's `_cat`
// endpoints degrade to an empty (not an error) result if nothing matches
// -- deliberately not the full 18-pattern curated list verbatim, since
// one of those patterns ("<prefix>-stats-bookmarks") is a bare exact
// name, and _cat/indices can 404 when a comma-joined list mixes an exact
// missing name with wildcards. The precise curated-pattern match happens
// client-side afterward (matchesAnyPattern), so this is purely a scoping
// optimization (avoid listing every index in the whole cluster), not a
// precision mechanism.
func buildListIndicesCommand(prefix string) string {
	url := fmt.Sprintf("localhost:9200/_cat/indices/%s-*,.ds-%s-*?h=index", prefix, prefix)
	return fmt.Sprintf("curl --fail-with-body -sS -X GET %s", shellQuote(url))
}

// parseListedIndices splits a plain-text `_cat/indices?h=index` response
// (one index name per line, confirmed live 2026-08-19 against
// CaltechAUTHORS production) into a slice, skipping blank lines.
func parseListedIndices(stdout string) []string {
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

// matchesAnyPattern reports whether name matches any of patterns, each a
// path.Match-style glob (rdmOpenSearchSnapshotIndexPatterns' own "*"
// wildcards are the only special character used, so path.Match is
// sufficient -- no need for a dedicated glob package).
func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// detectExistingOpenSearchIndices lists every index on instanceID
// matching indexPrefix's own two broad wildcards (buildListIndicesCommand),
// then filters client-side to exactly the curated patterns -- reports
// which of a restore's own target indices already exist, so the caller
// can gate a destructive delete-before-restore behind an explicit
// confirmation.
func detectExistingOpenSearchIndices(ctx context.Context, client awsclient.SSMAPI, instanceID, indexPrefix string, patterns []string, timeout, pollInterval time.Duration) ([]string, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildListIndicesCommand(indexPrefix), timeout, pollInterval)
	if err != nil {
		return nil, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, curlFailureError(fmt.Sprintf("listing existing indices on %s failed", instanceID), status, stdout)
	}
	var matched []string
	for _, name := range parseListedIndices(stdout) {
		if matchesAnyPattern(name, patterns) {
			matched = append(matched, name)
		}
	}
	sort.Strings(matched)
	return matched, nil
}

// buildDeleteIndicesCommand builds the curl command that deletes every
// name in indices via a single comma-joined DELETE request -- OpenSearch's
// real REST API accepts a comma-separated index list on one DELETE call,
// same shape as buildCreateSnapshotCommand's comma-joined indices value.
// Never a raw filesystem operation, matching buildDeleteSnapshotCommand's
// own precedent.
func buildDeleteIndicesCommand(indices []string) string {
	url := fmt.Sprintf("localhost:9200/%s", strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X DELETE %s", shellQuote(url))
}

// DeleteConflictingIndices runs buildDeleteIndicesCommand via SSM for a
// non-empty indices list -- a no-op (no SSM call at all) when indices is
// empty, so callers don't need their own empty-check before calling this.
func DeleteConflictingIndices(ctx context.Context, client awsclient.SSMAPI, instanceID string, indices []string, timeout, pollInterval time.Duration) error {
	if len(indices) == 0 {
		return nil
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDeleteIndicesCommand(indices), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("deleting %d conflicting index/indices on %s failed", len(indices), instanceID), status, stdout)
	}
	return nil
}

// buildRestoreSnapshotCommand builds the curl command that triggers
// OpenSearch's own `_restore` API for snapshotName in repo, scoped to
// indices (comma-joined, same shape as buildCreateSnapshotCommand).
// ignore_unavailable/include_global_state match the create side for the
// same reasons. No wait_for_completion -- completion is polled
// separately via PollRestoreUntilComplete, matching the create side's own
// "don't block a single SSM command on a long operation" precedent.
func buildRestoreSnapshotCommand(repo, snapshotName string, indices []string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s/%s/_restore", repo, snapshotName)
	body := fmt.Sprintf(`{"indices":%q,"ignore_unavailable":true,"include_global_state":false}`, strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X POST %s -H 'Content-Type: application/json' -d %s",
		shellQuote(url), shellQuote(body))
}

// RestoreSnapshot runs buildRestoreSnapshotCommand via SSM and errors on a
// non-Success SSM invocation status. Returns once the restore request has
// been accepted -- it does not wait for the restore itself to finish; see
// PollRestoreUntilComplete for that.
func RestoreSnapshot(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, snapshotName string, indices []string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildRestoreSnapshotCommand(repo, snapshotName, indices), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("restoring snapshot %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
	}
	return nil
}

// buildRestoreRecoveryCommand builds the curl command that fetches
// OpenSearch's own recovery status for indices -- restores aren't tracked
// as a named object the way snapshots are (there is no `_restore/<name>`
// status endpoint), so `_cat/recovery` is the real, documented mechanism
// for monitoring an in-progress restore (PLAN.md Phase 20.60's own design
// note: "OpenSearch's restore-status API (_cat/recovery ...) gives it a
// real per-shard signal").
func buildRestoreRecoveryCommand(indices []string) string {
	url := fmt.Sprintf("localhost:9200/_cat/recovery/%s?h=index,type,stage", strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X GET %s", shellQuote(url))
}

// parseRestoreRecovery parses a plain-text `_cat/recovery?h=index,type,stage`
// response (one row per shard) and reports whether every "snapshot"-type
// recovery row (the kind a just-triggered restore creates -- as opposed to
// "peer"/"store", ordinary replica/primary recovery unrelated to this
// restore) has reached the "done" stage. A response with zero snapshot-
// type rows is reported as not-done, not an error -- recovery rows can
// take a moment to register after the `_restore` call returns, and
// "nothing to report yet" must mean "keep polling," not "already
// finished" (the same false-positive-avoidance shape as
// parseSnapshotState requiring a real snapshots entry to exist at all).
func parseRestoreRecovery(stdout string) (done bool, err error) {
	var sawSnapshotRow bool
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return false, fmt.Errorf("unexpected _cat/recovery row %q", line)
		}
		recoveryType, stage := fields[1], fields[2]
		if recoveryType != "snapshot" {
			continue
		}
		sawSnapshotRow = true
		if stage != "done" {
			return false, nil
		}
	}
	return sawSnapshotRow, nil
}

// PollRestoreUntilComplete polls buildRestoreRecoveryCommand once every
// pollInterval, via a fresh SSM round trip each time, until every
// snapshot-type recovery row reaches "done" or the overall timeout
// elapses (an error, matching PollSnapshotUntilComplete's own "a restore
// that never finishes is a real problem" precedent). Progress is printed
// to w throughout via pollWithProgress (PLAN.md Phase 20.53/20.60) --
// built with progress reporting from the start, rather than shipping a
// bare blocking wait and retrofitting the lesson a second time (see
// DECISIONS.md, "Restore load steps should report real progress...").
func PollRestoreUntilComplete(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID string, indices []string, timeout, pollInterval time.Duration) error {
	command := buildRestoreRecoveryCommand(indices)
	label := fmt.Sprintf("OpenSearch restore of %d index pattern(s) on %s", len(indices), instanceID)

	return pollWithProgress(ctx, w, label, timeout, pollInterval, func(ctx context.Context) (bool, error) {
		stdout, status, err := RunShellCommand(ctx, client, instanceID, command, DefaultSnapshotStateCheckTimeout, DefaultSSMPollInterval)
		if err != nil {
			return false, err
		}
		if status != ssmtypes.CommandInvocationStatusSuccess {
			return false, curlFailureError(fmt.Sprintf("restore recovery check on %s failed", instanceID), status, stdout)
		}
		return parseRestoreRecovery(stdout)
	})
}

// buildVerifyRestoredIndicesCommand builds the curl command
// VerifyRestoredIndices uses to report each restored index's actual
// post-restore state.
func buildVerifyRestoredIndicesCommand(indices []string) string {
	url := fmt.Sprintf("localhost:9200/_cat/indices/%s?h=index,health,status,docs.count", strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X GET %s", shellQuote(url))
}

// restoredIndexInfo is one restored index's real, observed post-restore
// state, as reported by VerifyRestoredIndices.
type restoredIndexInfo struct {
	Index     string
	Health    string
	Status    string
	DocsCount int
}

// parseRestoredIndices parses a `_cat/indices?h=index,health,status,docs.count`
// response (space-padded columns, confirmed live 2026-08-19 against
// CaltechAUTHORS production) into one restoredIndexInfo per row.
func parseRestoredIndices(stdout string) ([]restoredIndexInfo, error) {
	var out []restoredIndexInfo
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("unexpected _cat/indices row %q", line)
		}
		n, convErr := strconv.Atoi(fields[3])
		if convErr != nil {
			return nil, fmt.Errorf("parsing docs.count in row %q: %w", line, convErr)
		}
		out = append(out, restoredIndexInfo{Index: fields[0], Health: fields[1], Status: fields[2], DocsCount: n})
	}
	return out, nil
}

// VerifyRestoredIndices runs buildVerifyRestoredIndicesCommand via SSM and
// reports each restored index's real, observed health/status/doc count --
// deliberately not a comparison against the snapshot's own internal
// `_status` metadata (PLAN.md Phase 20.51's original sketch), since that
// endpoint's exact per-index doc-count JSON shape hasn't been confirmed
// against a real OpenSearch response (unlike this function's own
// `_cat/indices` shape, checked live 2026-08-19). Reporting the actually-
// observed state directly is simpler, avoids guessing at an unverified
// nested field path, and still catches the failure modes that matter
// (an index missing entirely from the response, or reporting red health)
// -- see DECISIONS.md, "Restore OpenSearch: verify against observed
// _cat/indices state, not the snapshot's own internal _status metadata."
func VerifyRestoredIndices(ctx context.Context, client awsclient.SSMAPI, instanceID string, indices []string, timeout, pollInterval time.Duration) ([]restoredIndexInfo, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildVerifyRestoredIndicesCommand(indices), timeout, pollInterval)
	if err != nil {
		return nil, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return nil, curlFailureError(fmt.Sprintf("verifying restored indices on %s failed", instanceID), status, stdout)
	}
	return parseRestoredIndices(stdout)
}

// pickSnapshotPrefix lets the operator pick one of prefixes (already
// sorted most-recent-first by the caller) -- same shape as pickS3Object.
func pickSnapshotPrefix(w io.Writer, title, description string, prefixes []SnapshotPrefixInfo, input io.Reader, output io.Writer) (SnapshotPrefixInfo, error) {
	return pickComparable(w, title, description, hintCancel, prefixes, snapshotPrefixLabel, input, output)
}

// snapshotPrefixLabel formats one SnapshotPrefixInfo for pickSnapshotPrefix's list.
func snapshotPrefixLabel(p SnapshotPrefixInfo) string {
	return fmt.Sprintf("%s (created %s)", p.Name, p.CreatedAt.Format("2006-01-02 15:04:05"))
}

// RestoreOpenSearchSnapshot runs the full Restore OpenSearch Snapshot from
// S3 workflow (DESIGN.md, "RDM Backup & Restore Domain" -> "Restore
// OpenSearch Snapshot from S3"; PLAN.md Phase 20.51): pick a target
// instance, then delegate to the testable core. No recall/default-cursor
// history, matching Restore SQL Backup's own precedent (Phase 20.50) --
// restoring is a rare, deliberate action, not a routine one worth
// pre-positioning.
func RestoreOpenSearchSnapshot(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance, openSearchBackupDirRules []config.BackupDirectoryRule) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select the target instance to restore into", "Connects to this instance via SSM to restore OpenSearch indices from an archived snapshot. This deletes any conflicting indices already on the target before restoring.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return restoreOpenSearchSnapshot(ctx, w, ssmClients, s3Client, newS3Client, inst, openSearchBackupDirRules, nil, nil)
}

// restoreOpenSearchSnapshot is RestoreOpenSearchSnapshot's testable core,
// once a target instance is resolved -- input/output are nil in
// production and supplied by tests to drive every prompt/confirm in this
// function through its accessible-mode pipe path instead.
//
// Step order applies Restore SQL Backup's own step-order lesson (PLAN.md
// Phase 20.50, DECISIONS.md, "Restore SQL Backup: resolve the Postgres
// target before any S3 prompt, not after") from the start, rather than
// needing a second live-testing round to rediscover it: detecting
// conflicting indices only needs the target's own index prefix
// (Project/Name tag), not any bucket/source-name/snapshot choice, so it
// runs immediately after the AWS-CLI preflight -- before any S3 prompt,
// and before syncing a potentially multi-gigabyte snapshot down. See
// DECISIONS.md, "Restore OpenSearch: detect and resolve conflicting
// indices before any S3 activity, applying the SQL restore lesson from
// the start."
func restoreOpenSearchSnapshot(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, openSearchBackupDirRules []config.BackupDirectoryRule, input io.Reader, output io.Writer) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	// indexPrefix defaults to the target's own Project tag (falling back
	// to Name), same reasoning as Archive OpenSearch Snapshot's own fix
	// (DECISIONS.md, "Real bug: Archive OpenSearch Snapshot's
	// index-match patterns used the Name tag, not the Project tag") --
	// but stays editable, unlike Archive's own computed-and-used-as-is
	// value. A restore only ever connects to the *target* instance, not
	// whichever instance the archived snapshot actually came from, so
	// the target's own tags are just a convenient default for the
	// common self-restore-after-disaster case (source and target are
	// the same instance) -- a cross-instance restore (e.g. restoring a
	// production instance's snapshot onto an unrelated dev/test box)
	// needs this to be the snapshot's own real index prefix instead,
	// which can differ arbitrarily from the target's tags. Silently
	// using the wrong value wouldn't error -- ignore_unavailable:true
	// just restores zero indices -- so this must be confirmable/
	// overridable, not computed-and-trusted (DECISIONS.md, "Restore
	// OpenSearch Snapshot from S3: a fifth correction...").
	indexPrefix, err := ui.Prompt("OpenSearch index prefix in the archived snapshot to restore (e.g. caltechdata) -- may differ from the target's own tags when restoring a different instance's backup onto this one",
		ui.WithDefault(cmp.Or(inst.Project, inst.Name)), ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}
	indices := rdmOpenSearchSnapshotIndexPatterns(indexPrefix)

	existing, err := detectExistingOpenSearchIndices(ctx, ssmClient, inst.InstanceID, indexPrefix, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		ok, err := ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Cancelled.")
			return nil
		}
		if err := DeleteConflictingIndices(ctx, ssmClient, inst.InstanceID, existing, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
			return err
		}
	}

	dirPromptOpts := []ui.PromptOption{ui.WithValidator(requireNonEmpty)}
	if def := config.BackupDirectoryFor(openSearchBackupDirRules, inst.Name); def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	} else {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault("/opt/rdm_opensearch_backups"))
	}
	dirPromptOpts = append(dirPromptOpts, ui.WithIO(input, output))
	directory, err := ui.Prompt("OpenSearch backup directory (e.g. /opt/rdm_opensearch_backups)", dirPromptOpts...)
	if err != nil {
		return err
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

	// Defaults to the target's own Name -- the common case is restoring
	// an instance's own most recent snapshot -- but stays editable, same
	// rationale as Restore SQL Backup's own source-name prompt.
	sourceName, err := ui.Prompt("Source instance name (the S3 prefix to restore from)", ui.WithDefault(inst.Name), ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}

	prefixes, err := ListArchivedSnapshotPrefixes(ctx, bucketClient, bucket, sourceName)
	if err != nil {
		return err
	}
	if len(prefixes) == 0 {
		fmt.Fprintf(w, "No OpenSearch snapshots found under s3://%s/%s/opensearch-snapshots/.\n", bucket, sourceName)
		return nil
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].CreatedAt.After(prefixes[j].CreatedAt) })
	snap, err := pickSnapshotPrefix(w, "Select an OpenSearch snapshot to restore", "Most recent first.", prefixes, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	if err := SyncOpenSearchBackupsFromS3(ctx, ssmClient, inst.InstanceID, bucket, sourceName, snap.Name, directory, DefaultOpenSearchSyncTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := RegisterSnapshotRepo(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, DefaultOpenSearchContainerRepoPath, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	if err := RestoreSnapshot(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snap.Name, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := PollRestoreUntilComplete(ctx, w, ssmClient, inst.InstanceID, indices, DefaultSnapshotCreateTimeout, DefaultSnapshotPollInterval); err != nil {
		return err
	}

	restored, err := VerifyRestoredIndices(ctx, ssmClient, inst.InstanceID, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\nRestored OpenSearch snapshot %q from s3://%s/%s onto %s:\n", snap.Name, bucket, openSearchSnapshotsPrefix(sourceName), inst.InstanceID)
	var redCount int
	for _, r := range restored {
		marker := ""
		if r.Health == "red" {
			marker = "  *** RED ***"
			redCount++
		}
		fmt.Fprintf(w, "  %-55s %-6s %-6s %8d docs%s\n", r.Index, r.Health, r.Status, r.DocsCount, marker)
	}
	if redCount > 0 {
		fmt.Fprintf(w, "\nWARNING: %d restored index/indices reported red health -- investigate before trusting this restore.\n", redCount)
	}
	return nil
}
