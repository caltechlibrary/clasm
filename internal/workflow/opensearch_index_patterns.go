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
		fmt.Sprintf(".ds-%s-auditlog-audit-log-*", prefix),
	}
}
