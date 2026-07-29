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
		".ds-caltechauthors-auditlog-audit-log-*",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d patterns, want %d:\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got[len(got)-1] != ".ds-caltechauthors-auditlog-audit-log-*" {
		t.Errorf("audit log pattern missing its .ds- data-stream backing-index prefix")
	}
}
