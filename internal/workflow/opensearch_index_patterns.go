package workflow

import "fmt"

// rdmOpenSearchSnapshotIndexPatterns returns the confirmed allowlist of
// index patterns Archive OpenSearch Snapshot to S3 (PLAN.md Phase 20.49)
// scopes a snapshot to, each prefixed by prefix (the picked instance's
// Name tag -- see DESIGN.md, "Archive OpenSearch Snapshot to S3", and the
// real `_cat/indices` pull against CaltechAUTHORS production this list
// was grounded in, 2026-07-28). Deliberately excludes the raw
// `events-stats-*` indices (large, growing, confirmed unused by any
// report) -- out of scope by design, not an oversight.
//
// The audit log needs BOTH of its forms listed, which is not obvious.
// `.ds-<prefix>-auditlog-audit-log-*` is OpenSearch's data-stream
// backing-index naming convention; a plain `<prefix>-auditlog-*` index
// is what RDM creates once no data stream is registered. An instance
// restored from a snapshot ends up with one of each, because a snapshot
// taken with `include_global_state: false` carries `"data_streams": []`
// -- restoring a backing index yields a plain index with its alias but
// no stream definition, so the app starts a fresh ordinary audit-log
// index beside the restored orphan. Found live on CaltechAUTHORS
// production v13 2026-09-16: the restored
// `.ds-caltechauthors-auditlog-audit-log-v1.0.0-000001` (302,252 docs)
// was being snapshotted while the actively-written
// `caltechauthors-auditlog-audit-log-v1.0.0` (532 docs, created the day
// before cutover) matched nothing, so every audit record since go-live
// was outside the backup. Job logs were missing outright and have the
// same two forms for the same reason -- CaltechAUTHORS production had
// `.ds-<prefix>-job-logs-*` (524 docs) while v13 has the plain
// `<prefix>-job-logs`. Both kinds of log confirmed in scope by the user
// 2026-09-16.
func rdmOpenSearchSnapshotIndexPatterns(prefix string) []string {
	return []string{
		fmt.Sprintf("%s-rdmrecords-*", prefix),
		fmt.Sprintf("%s-users-*", prefix),
		fmt.Sprintf("%s-communities-*", prefix),
		fmt.Sprintf("%s-requests*", prefix),
		fmt.Sprintf("%s-requestevents-*", prefix),
		fmt.Sprintf("%s-names-*", prefix),
		fmt.Sprintf("%s-affiliations-*", prefix),
		fmt.Sprintf("%s-funders-*", prefix),
		fmt.Sprintf("%s-awards-*", prefix),
		fmt.Sprintf("%s-subjects-*", prefix),
		fmt.Sprintf("%s-vocabularies-*", prefix),
		fmt.Sprintf("%s-groups-*", prefix),
		fmt.Sprintf("%s-domains-*", prefix),
		fmt.Sprintf("%s-communitymembers-*", prefix),
		fmt.Sprintf("%s-stats-record-view-*", prefix),
		fmt.Sprintf("%s-stats-file-download-*", prefix),
		fmt.Sprintf("%s-stats-bookmarks", prefix),
		fmt.Sprintf("%s-auditlog-*", prefix),
		fmt.Sprintf(".ds-%s-auditlog-audit-log-*", prefix),
		fmt.Sprintf("%s-job-logs*", prefix),
		fmt.Sprintf(".ds-%s-job-logs-*", prefix),
	}
}
