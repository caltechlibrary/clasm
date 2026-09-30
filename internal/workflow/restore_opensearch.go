package workflow

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

// buildChownTreeCommand builds the `chown -R` that hands a directory tree to
// owner. For the OpenSearch snapshot repository it pairs with
// buildSyncFromS3Command above and must run after it, every time.
//
// `aws s3 sync` is executed by SSM, which runs as **root on the host**, so
// every file and directory it writes lands root:root. OpenSearch runs as
// DefaultOpenSearchRepoUID inside the search container and can only write
// where the `path.repo` retrofit's one-time chown reached -- the top-level
// directory alone. Without this step the repository is silently unwritable
// below its own root, and the damage surfaces not here but on the
// instance's *next* archive (DR-0175, PLAN.md Phase 20.63).
//
// The owner is the service user's own uid *and* gid (DR-0176), looked up on
// the instance. This used to be a hardcoded 1000:1000, and on the current
// images ubuntu is 1000:1001 -- gid 1000 is the docker group -- so the
// repository ended up ubuntu:docker. It worked, because the container
// writes as the owner uid, but the group was an accident that handed
// docker-group members access nobody intended.
func buildChownTreeCommand(dir string, owner ServiceOwner) string {
	return fmt.Sprintf("chown -R %d:%d %s", owner.UID, owner.GID, shellQuote(dir))
}

// NormalizeSnapshotRepoOwnership runs buildChownTreeCommand via SSM.
//
// Deliberately a separate SSM round trip rather than `&& chown ...`
// appended to the sync command (DR-0175 decision 1): this project has
// twice been bitten by compound remote commands hiding which half failed
// -- `pg_dump | gzip` masking pg_dump's own exit status, and gunzip's
// "unknown suffix" message vanishing with stderr -- and one extra round
// trip is cheap next to a restore.
func NormalizeSnapshotRepoOwnership(ctx context.Context, client awsclient.SSMAPI, instanceID, dir string, owner ServiceOwner, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildChownTreeCommand(dir, owner), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("setting ownership of the OpenSearch snapshot repository %q on %s failed", dir, instanceID), status, stdout)
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

// backingIndexRE matches the name of a data stream's backing index in
// OpenSearch: ".ds-<stream>-<six-digit generation>", e.g.
// ".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001", whose stream is
// "caltechauthors-auditlog-audit-log-v1.0.0" (the mapping OpenSearch itself
// reported in its 2026-09-30 refusal to delete that index by name).
var backingIndexRE = regexp.MustCompile(`^\.ds-(.+)-\d{6}$`)

// dataStreamOfBackingIndex reports the data stream a backing index belongs
// to, derived from its name, and whether index is a backing index at all.
func dataStreamOfBackingIndex(index string) (string, bool) {
	m := backingIndexRE.FindStringSubmatch(index)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// buildDeleteIndicesCommand builds the curl command that deletes every
// name in indices via a single comma-joined DELETE request -- OpenSearch's
// real REST API accepts a comma-separated index list on one DELETE call,
// same shape as buildCreateSnapshotCommand's comma-joined indices value.
// Never a raw filesystem operation, matching buildDeleteSnapshotCommand's
// own precedent. Run after buildDeleteDataStreamsCommand, never before: a
// backing index that is still its stream's write index cannot be deleted by
// name.
func buildDeleteIndicesCommand(indices []string) string {
	// ignore_unavailable: a name already removed -- a data stream's backing
	// index that the stream delete just took with it -- is not an error.
	url := fmt.Sprintf("localhost:9200/%s?ignore_unavailable=true", strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X DELETE %s", shellQuote(url))
}

// buildDeleteDataStreamsCommand builds the curl command that deletes data
// streams, and with them their backing indices. A backing index cannot be
// deleted by name while it is its stream's write index -- OpenSearch answers
// HTTP 400 and, because the request is one comma-joined DELETE, refuses the
// ordinary indices in it too (found live 2026-09-30, Phase 20.65 step 4).
func buildDeleteDataStreamsCommand(streams []string) string {
	url := fmt.Sprintf("localhost:9200/_data_stream/%s", strings.Join(streams, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X DELETE %s", shellQuote(url))
}

// DeleteConflictingIndices deletes indices via SSM: first the data streams that
// own any backing index in the list, then every listed name by name, including
// the backing indices. A no-op (no SSM call at all) when indices is empty, so
// callers don't need their own empty-check before calling this.
//
// Both steps are needed. A backing index that is a stream's write index can only
// go with its stream (found live 2026-09-30: HTTP 400 on the plain delete).
// But a backing index is often *not* in a stream any more: a restore brings the
// audit log back as a plain index with no stream (DR-0173), so deleting "its
// stream" removes nothing and the next _restore fails with "an open index with
// same name already exists" (found live the same day). OpenSearch answers 200 for
// a stream that does not exist and, with ignore_unavailable, for an index already
// gone, so doing both is safe in every case.
func DeleteConflictingIndices(ctx context.Context, client awsclient.SSMAPI, instanceID string, indices []string, timeout, pollInterval time.Duration) error {
	if len(indices) == 0 {
		return nil
	}
	var streams []string
	seen := map[string]bool{}
	for _, name := range indices {
		if stream, ok := dataStreamOfBackingIndex(name); ok && !seen[stream] {
			seen[stream] = true
			streams = append(streams, stream)
		}
	}
	if len(streams) > 0 {
		stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDeleteDataStreamsCommand(streams), timeout, pollInterval)
		if err != nil {
			return err
		}
		if status != ssmtypes.CommandInvocationStatusSuccess {
			return curlFailureError(fmt.Sprintf("deleting data stream(s) %s on %s failed", strings.Join(streams, ", "), instanceID), status, stdout)
		}
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

// errNoIndicesRestored is RestoreSnapshot's answer to a restore request
// OpenSearch accepted but which matched nothing in the snapshot.
var errNoIndicesRestored = errors.New("the snapshot restored no indices")

// restoredIndexCount reads the number of indices from a `_restore` response
// ({"snapshot":{"indices":[...],...}}). known is false when the response is
// not that shape -- an unrecognised answer must not be read as "nothing".
func restoredIndexCount(stdout string) (n int, known bool) {
	var resp struct {
		Snapshot *struct {
			Indices []string `json:"indices"`
		} `json:"snapshot"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp) != nil || resp.Snapshot == nil {
		return 0, false
	}
	return len(resp.Snapshot.Indices), true
}

// RestoreSnapshot runs buildRestoreSnapshotCommand via SSM and errors on a
// non-Success SSM invocation status. Returns once the restore request has
// been accepted -- it does not wait for the restore itself to finish; see
// PollRestoreUntilComplete for that.
//
// ignore_unavailable is true, so a request matching nothing in the snapshot
// is still accepted (HTTP 200, "indices":[], 0 shards). That is reported as
// errNoIndicesRestored rather than left to fail later on a 404 from the
// recovery poll, for indices that were never there.
func RestoreSnapshot(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, snapshotName string, indices []string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildRestoreSnapshotCommand(repo, snapshotName, indices), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("restoring snapshot %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
	}
	if n, known := restoredIndexCount(stdout); known && n == 0 {
		return errNoIndicesRestored
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

// snapshotIndexList reads the index names from a `GET _snapshot/<repo>/<name>`
// response ({"snapshots":[{"indices":[...],...}]}). known is false when the
// response is not that shape, so an unrecognised answer is never read as
// "the snapshot is empty".
func snapshotIndexList(stdout string) (indices []string, known bool) {
	var resp struct {
		Snapshots []struct {
			Indices []string `json:"indices"`
		} `json:"snapshots"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp) != nil || len(resp.Snapshots) == 0 {
		return nil, false
	}
	return resp.Snapshots[0].Indices, true
}

// CheckSnapshotHoldsMatchingIndices asks the registered repository what
// snapshotName holds and stops, before anything is deleted, if none of its
// indices match patterns -- the guard against an index prefix that matches
// live indices on the target but nothing in the snapshot (a cross-instance
// restore with the wrong prefix), which would otherwise delete first and
// restore nothing. A response it cannot read does not block: the check exists
// to refuse a positively empty match, and RestoreSnapshot still stops on an
// empty result afterwards.
func CheckSnapshotHoldsMatchingIndices(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, snapshotName, indexPrefix string, patterns []string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildSnapshotStateCommand(repo, snapshotName), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("listing the indices in snapshot %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
	}
	held, known := snapshotIndexList(stdout)
	if !known {
		return nil
	}
	for _, name := range held {
		if matchesAnyPattern(name, patterns) {
			return nil
		}
	}
	sort.Strings(held)
	return fmt.Errorf("snapshot %q holds no indices matching the index prefix %q, so nothing was deleted or restored. It holds: %s. The index prefix is the name the indices carry inside the snapshot (e.g. caltechauthors), not the source instance name used as the S3 prefix (e.g. caltechauthors-v13)", snapshotName, indexPrefix, summarizeNames(held, 8))
}

// summarizeNames joins up to limit names, noting how many were left out.
func summarizeNames(names []string, limit int) string {
	if len(names) == 0 {
		return "no indices"
	}
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

// SnapshotSource is one source-instance prefix in a backup bucket that holds
// archived OpenSearch snapshots.
type SnapshotSource struct {
	Name      string
	Snapshots int
}

// maxSnapshotSourcesProbed bounds how many top-level prefixes
// ListSnapshotSources inspects (one listing each). A backup bucket holds one
// prefix per instance, so this is generous; it only stops a bucket that has
// been used for something else from making an error message slow.
const maxSnapshotSourcesProbed = 50

// ListSnapshotSources lists the source prefixes in bucket that hold at least
// one archived OpenSearch snapshot, sorted by name -- the answer to "which
// Source instance name did you mean?" when the one typed holds none.
func ListSnapshotSources(ctx context.Context, client awsclient.S3API, bucket string) ([]SnapshotSource, error) {
	var names []string
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, cp := range out.CommonPrefixes {
			names = append(names, strings.TrimSuffix(aws.ToString(cp.Prefix), "/"))
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Strings(names)
	if len(names) > maxSnapshotSourcesProbed {
		names = names[:maxSnapshotSourcesProbed]
	}
	var sources []SnapshotSource
	for _, name := range names {
		snaps, err := ListArchivedSnapshotPrefixes(ctx, client, bucket, name)
		if err != nil {
			return nil, err
		}
		if len(snaps) > 0 {
			sources = append(sources, SnapshotSource{Name: name, Snapshots: len(snaps)})
		}
	}
	return sources, nil
}

// noSnapshotsError is what Restore returns when the chosen source prefix
// holds no snapshots. It is an error rather than a quiet return because
// nothing was restored, and it says what to try, because the source-name
// prompt defaults to the *target's* own Name, which is only right when a
// target restores its own backups -- restoring another instance's (the usual
// reason for a restore onto a test box) needs that instance's name. Found
// live 2026-09-30: the default listed an empty prefix and Restore said only
// that no snapshots were found, and exited successfully.
func noSnapshotsError(ctx context.Context, client awsclient.S3API, bucket, sourceName, targetName string) error {
	msg := fmt.Sprintf("no OpenSearch snapshots found under s3://%s/%s/. The source instance name is the S3 prefix the snapshots were archived under; it defaults to this target's own name (%s), which is right only when restoring the target's own backups", bucket, openSearchSnapshotsPrefix(sourceName)+"/", targetName)
	sources, err := ListSnapshotSources(ctx, client, bucket)
	switch {
	case err != nil:
		return fmt.Errorf("%s (the bucket's other sources could not be listed: %v)", msg, err)
	case len(sources) == 0:
		return fmt.Errorf("%s. There is no source in s3://%s that holds any OpenSearch snapshots", msg, bucket)
	}
	parts := make([]string, 0, len(sources))
	for _, src := range sources {
		unit := "snapshots"
		if src.Snapshots == 1 {
			unit = "snapshot"
		}
		parts = append(parts, fmt.Sprintf("%s (%d %s)", src.Name, src.Snapshots, unit))
	}
	return fmt.Errorf("%s. Sources in s3://%s that hold snapshots: %s", msg, bucket, strings.Join(parts, ", "))
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
// needing a second live-testing round to rediscover it: *detecting*
// conflicting indices, and taking the operator's type-to-confirm, only needs
// the target's own index prefix (Project/Name tag), not any bucket/
// source-name/snapshot choice, so both run immediately after the AWS-CLI
// preflight -- before any S3 prompt, and before syncing a potentially
// multi-gigabyte snapshot down. See DECISIONS.md, "Restore OpenSearch:
// detect and resolve conflicting indices before any S3 activity, applying
// the SQL restore lesson from the start."
//
// *Deleting* them is a different matter and comes late: only once the
// snapshot is chosen, downloaded, registered and confirmed to hold matching
// indices, immediately before the restore that replaces them (DR-0175
// decision 2). The early placement was a real defect, found live 2026-09-30.
func restoreOpenSearchSnapshot(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, openSearchBackupDirRules []config.BackupDirectoryRule, input io.Reader, output io.Writer) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	// The service owner is looked up and its uid checked first of all
	// (DR-0176 decision 5), before any prompt and above all before
	// DeleteConflictingIndices below: a mismatch found after that deletion
	// would already have destroyed the current indices ahead of any verified
	// replacement, which this workflow's own ordering (DR-0175 decision 2)
	// exists to refuse. The check needs only the instance, so nothing forces
	// it later.
	owner, err := ResolveServiceOwner(ctx, ssmClient, inst.InstanceID, DefaultOwnershipTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if err := CheckOwnerMatchesOpenSearch(owner); err != nil {
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
		// Confirmed, not yet deleted: see the deletion just before
		// RestoreSnapshot below.
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
		return noSnapshotsError(ctx, bucketClient, bucket, sourceName, inst.Name)
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].CreatedAt.After(prefixes[j].CreatedAt) })
	snap, err := pickSnapshotPrefix(w, "Select an OpenSearch snapshot to restore", "Most recent first.", prefixes, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return executeOpenSearchRestore(ctx, w, ssmClient, openSearchRestoreRun{
		inst: inst, owner: owner, directory: directory, bucket: bucket, sourceName: sourceName,
		indexPrefix: indexPrefix, snap: snap, indices: indices, existing: existing,
	})
}

// openSearchRestoreRun is everything a confirmed Restore OpenSearch run needs,
// however it was collected: prompts in the interactive form, arguments and a
// read-only plan in the CLI form (DR-0180). Both call executeOpenSearchRestore,
// so the order of the destructive steps exists once.
type openSearchRestoreRun struct {
	inst        inventory.Instance
	owner       ServiceOwner
	directory   string
	bucket      string
	sourceName  string
	indexPrefix string
	snap        SnapshotPrefixInfo
	indices     []string // the index patterns to restore, from rdmOpenSearchSnapshotIndexPatterns
	existing    []string // the conflicting indices on the target, deleted just before the restore
}

// executeOpenSearchRestore runs a confirmed restore: ensure the directory, sync
// the snapshot down, hand it to the service user, register it, check it holds
// matching indices, delete the conflicting indices, restore, verify, then clean
// up. Nothing before the delete is destructive, and the delete comes after a
// verified source (DR-0175 decision 2).
func executeOpenSearchRestore(ctx context.Context, w io.Writer, ssmClient awsclient.SSMAPI, r openSearchRestoreRun) error {
	inst, owner, directory, bucket, sourceName := r.inst, r.owner, r.directory, r.bucket, r.sourceName
	indexPrefix, snap, indices, existing := r.indexPrefix, r.snap, r.indices, r.existing

	// Made or repaired for the service user immediately before the sync,
	// which needs the directory the operator just typed. Non-recursive: the
	// chown -R below, after the sync, is the only recursive step.
	if err := EnsureBackupDirectory(ctx, ssmClient, inst.InstanceID, directory, owner, openSearchRepoDirMode, DefaultOwnershipTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	// Before the sync, the directory must hold nothing that can shadow the
	// snapshot about to be synced into it: any earlier registration is dropped
	// (metadata only), and the generation files an earlier Archive or Restore
	// left behind are moved aside, never deleted. OpenSearch picks the highest
	// index-N it finds, not the one index.latest names (found live 2026-09-30).
	if err := DeregisterSnapshotRepoIfPresent(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := MoveStaleRepositoryGenerations(ctx, w, ssmClient, inst.InstanceID, directory, DefaultOwnershipTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := SyncOpenSearchBackupsFromS3(ctx, ssmClient, inst.InstanceID, bucket, sourceName, snap.Name, directory, DefaultOpenSearchSyncTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	// Strictly between the sync and the registration. After the sync
	// because that is what writes the root-owned files; before the
	// registration because registration only verifies a write into the
	// repository's top-level directory -- exactly the part the path.repo
	// retrofit already chowned -- so registering first would report
	// success against a tree OpenSearch cannot write and destroy the one
	// signal available here (DR-0175, PLAN.md Phase 20.63).
	if err := NormalizeSnapshotRepoOwnership(ctx, ssmClient, inst.InstanceID, directory, owner, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := RegisterSnapshotRepo(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, DefaultOpenSearchContainerRepoPath, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	// The conflicting indices are deleted here, and nowhere earlier: the
	// snapshot is chosen, downloaded, owned by the service user and
	// registered, and (just below) confirmed to hold something that will
	// replace them. Deleting right after the index-prefix prompt, as this
	// once did, meant a mistyped source destroyed the live indices and then
	// found nothing to restore (found live 2026-09-30 on
	// caltechauthors-test-v13; DR-0175 decision 2 -- never destroy the current
	// state before a verified replacement exists). The operator's
	// confirmation was taken earlier because it only needs the target.
	if err := CheckSnapshotHoldsMatchingIndices(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snap.Name, indexPrefix, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := DeleteConflictingIndices(ctx, ssmClient, inst.InstanceID, existing, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	if err := RestoreSnapshot(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snap.Name, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		if errors.Is(err, errNoIndicesRestored) {
			return fmt.Errorf("snapshot %q holds no indices matching the index prefix %q, so nothing was restored (the synced snapshot is still in %s): the index prefix is the name the indices carry inside the snapshot (e.g. caltechauthors), not the source instance name used as the S3 prefix (e.g. caltechauthors-v13)", snap.Name, indexPrefix, directory)
		}
		return err
	}
	if err := PollRestoreUntilComplete(ctx, w, ssmClient, inst.InstanceID, indices, DefaultSnapshotCreateTimeout, DefaultSnapshotPollInterval); err != nil {
		return err
	}

	restored, err := VerifyRestoredIndices(ctx, ssmClient, inst.InstanceID, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	// Everything below this point is destructive, and nothing above it is
	// -- that ordering is the point, not an accident of where the code
	// happened to land (DR-0175 decision 2: never destroy the current
	// state before a verified replacement exists). A restore that fails
	// or cannot be verified returns above with the synced snapshot still
	// on disk, which is evidence.
	//
	// The mirror of Archive's own step 9: the snapshot goes out through
	// the OpenSearch API rather than a filesystem delete (DR-0131 -- and
	// because cleanup happens only here, the repository still knows the
	// snapshot, so the API can reach it and no carve-out is needed), then
	// the repository is deregistered. What is left is an empty directory
	// owned by uid 1000, which is what Archive needs to find.
	if err := DeleteSnapshot(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, snap.Name, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	if err := DeregisterSnapshotRepo(ctx, ssmClient, inst.InstanceID, DefaultOpenSearchRepoName, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}
	fmt.Fprintf(w, "Cleaned up the local snapshot repository on %s (snapshot %q deleted, repository deregistered).\n", inst.InstanceID, snap.Name)

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

// staleRepoDirPrefix is where the generation files already in a repository
// directory are moved before a Restore: outside the repository, so the next
// Archive's `aws s3 sync` of the directory does not upload them, and never
// deleted, so the move is reversible.
const staleRepoDirPrefix = "/var/tmp/clasm-stale-repo-"

// buildMoveStaleGenerationsCommand builds the command that moves every index-N
// file and index.latest in dir into staleDir, and prints one line,
// "clasm-stale-moved <count> <staleDir>" ("clasm-stale-moved 0" when there was
// nothing to move, in which case staleDir is not created).
//
// Why: every Archive and Restore leaves the repository's metadata behind with N
// one higher each time, and OpenSearch uses the highest index-N it finds, not
// the one index.latest names. A leftover index-31 (an empty repository) therefore
// shadowed a synced snapshot's own index-24 and the repository reported no
// snapshots (found live 2026-09-30, caltechauthors-test-v13). Only generation
// files move; snapshot and index data, and anything else in the directory, stay.
// It moves, never deletes: DR-0131 and DR-0175 avoid raw deletes in a repository.
func buildMoveStaleGenerationsCommand(dir, staleDir string) string {
	d, s := shellQuote(dir), shellQuote(staleDir)
	return fmt.Sprintf(`set -e; d=%s; s=%s; n=0; for f in "$d"/index-* "$d"/index.latest; do [ -e "$f" ] || continue; [ "$n" -eq 0 ] && mkdir -p "$s"; mv "$f" "$s"/; n=$((n+1)); done; if [ "$n" -gt 0 ]; then echo "clasm-stale-moved $n $s"; else echo "clasm-stale-moved 0"; fi`, d, s)
}

// parseStaleMove reads buildMoveStaleGenerationsCommand's output. ok is false
// when no well-formed line is present.
func parseStaleMove(stdout string) (n int, where string, ok bool) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "clasm-stale-moved" {
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count < 0 {
			return 0, "", false
		}
		if count == 0 {
			return 0, "", true
		}
		if len(fields) < 3 {
			return 0, "", false
		}
		return count, fields[2], true
	}
	return 0, "", false
}

// MoveStaleRepositoryGenerations moves the generation files already in dir aside
// (buildMoveStaleGenerationsCommand) and tells the operator how many and where.
// It says nothing when there was nothing to move.
func MoveStaleRepositoryGenerations(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID, dir string, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	staleDir := staleRepoDirPrefix + time.Now().Format("20060102T150405")
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildMoveStaleGenerationsCommand(dir, staleDir), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("moving the stale repository files in %q aside on %s failed", dir, instanceID), status, stdout)
	}
	n, where, ok := parseStaleMove(stdout)
	if !ok {
		return fmt.Errorf("moving the stale repository files in %q aside on %s gave no result to read: %q", dir, instanceID, strings.TrimSpace(stdout))
	}
	if n > 0 {
		noun := "files"
		if n == 1 {
			noun = "file"
		}
		fmt.Fprintf(w, "Moved %d stale repository %s (index-N, index.latest) from %s aside to %s on %s: leftovers of an earlier run, which would have shadowed the snapshot being restored. Nothing was deleted.\n", n, noun, dir, where, instanceID)
	}
	return nil
}

// buildDeregisterIfPresentCommand is buildDeregisterRepoCommand without
// --fail-with-body: it prints the HTTP status instead of failing on a 404, so a
// repository that is not registered is not an error.
func buildDeregisterIfPresentCommand(repo string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s", repo)
	return fmt.Sprintf("curl -sS -X DELETE -o /dev/null -w '%%{http_code}' %s", shellQuote(url))
}

// DeregisterSnapshotRepoIfPresent removes repo's registration if there is one,
// tolerating "not registered" (HTTP 404). Metadata only: no file in the
// directory is touched (DR-0175). A Restore does this before it syncs, so the
// repository is registered fresh against the files it is about to hold.
func DeregisterSnapshotRepoIfPresent(ctx context.Context, client awsclient.SSMAPI, instanceID, repo string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDeregisterIfPresentCommand(repo), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("deregistering snapshot repository %q on %s failed", repo, instanceID), status, stdout)
	}
	switch code := strings.TrimSpace(stdout); code {
	case "200", "404":
		return nil
	default:
		return fmt.Errorf("deregistering snapshot repository %q on %s returned HTTP status %q, want 200 or 404", repo, instanceID, code)
	}
}
