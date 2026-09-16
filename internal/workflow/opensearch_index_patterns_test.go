package workflow

import "testing"

func TestRDMOpenSearchSnapshotIndexPatterns(t *testing.T) {
	got := rdmOpenSearchSnapshotIndexPatterns("caltechauthors")
	want := []string{
		"caltechauthors-rdmrecords-*",
		"caltechauthors-users-*",
		"caltechauthors-communities-*",
		"caltechauthors-requests*",
		"caltechauthors-requestevents-*",
		"caltechauthors-names-*",
		"caltechauthors-affiliations-*",
		"caltechauthors-funders-*",
		"caltechauthors-awards-*",
		"caltechauthors-subjects-*",
		"caltechauthors-vocabularies-*",
		"caltechauthors-groups-*",
		"caltechauthors-domains-*",
		"caltechauthors-communitymembers-*",
		"caltechauthors-stats-record-view-*",
		"caltechauthors-stats-file-download-*",
		"caltechauthors-stats-bookmarks",
		"caltechauthors-auditlog-*",
		".ds-caltechauthors-auditlog-audit-log-*",
		"caltechauthors-job-logs*",
		".ds-caltechauthors-job-logs-*",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d patterns, want %d:\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Both audit-log forms must be present: a restored instance carries a
	// `.ds-`-prefixed data-stream backing index AND an ordinary index the
	// app creates once no data stream is registered. Listing only the
	// `.ds-` form silently omitted every post-restore audit record on
	// CaltechAUTHORS production v13 (found 2026-09-16).
	for _, required := range []string{
		"caltechauthors-auditlog-*",
		".ds-caltechauthors-auditlog-audit-log-*",
		"caltechauthors-job-logs*",
		".ds-caltechauthors-job-logs-*",
	} {
		found := false
		for _, p := range got {
			if p == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("pattern %q missing from the allowlist", required)
		}
	}
}
