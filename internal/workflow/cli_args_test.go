package workflow

import (
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

var cliArgsTestFleet = []inventory.Instance{
	{InstanceID: "i-1", Name: "caltechauthors-v13", Region: "us-west-2"},
	{InstanceID: "i-2", Name: "caltechdata", Region: "us-west-2"},
	{InstanceID: "i-3", Name: "caltechauthors-v13", Region: "us-east-1"}, // same Name, different region -- a real possibility in this fleet
}

func TestResolveInstanceArg_UniqueNameMatch(t *testing.T) {
	got, err := resolveInstanceArg("caltechdata", cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.InstanceID != "i-2" {
		t.Errorf("resolved InstanceID = %q, want i-2", got.InstanceID)
	}
}

func TestResolveInstanceArg_UniqueInstanceIDMatch(t *testing.T) {
	got, err := resolveInstanceArg("i-2", cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "caltechdata" {
		t.Errorf("resolved Name = %q, want caltechdata", got.Name)
	}
}

func TestResolveInstanceArg_NoMatch(t *testing.T) {
	_, err := resolveInstanceArg("does-not-exist", cliArgsTestFleet)
	if err == nil {
		t.Fatal("expected an error for an unmatched instance argument")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the unmatched argument, got: %v", err)
	}
}

// TestResolveInstanceArg_AmbiguousNameAcrossRegions pins that a same-Name
// collision across regions must be reported, never silently resolved to
// one of the matches (PLAN.md Phase 20.64 item 3).
func TestResolveInstanceArg_AmbiguousNameAcrossRegions(t *testing.T) {
	_, err := resolveInstanceArg("caltechauthors-v13", cliArgsTestFleet)
	if err == nil {
		t.Fatal("expected an error for a Name matching more than one instance")
	}
	if !strings.Contains(err.Error(), "i-1") || !strings.Contains(err.Error(), "i-3") {
		t.Errorf("error should name every ambiguous match, got: %v", err)
	}
}

func TestParseBackupArchiveArgs_HappyPath(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", "30"}
	inst, params, err := ParseBackupArchiveArgs(args, cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.InstanceID != "i-2" {
		t.Errorf("inst.InstanceID = %q, want i-2", inst.InstanceID)
	}
	want := BackupArchiveParams{InstanceID: "i-2", Directory: "/opt/rdm_sql_backups", Bucket: "s3://sql-backups.library.caltech.edu", AgeDays: 30, TrimRequested: true}
	if params != want {
		t.Errorf("params = %+v, want %+v", params, want)
	}
}

func TestParseBackupArchiveArgs_BlankTrimMeansNoTrim(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", ""}
	_, params, err := ParseBackupArchiveArgs(args, cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.TrimRequested || params.AgeDays != 0 {
		t.Errorf("got AgeDays=%d TrimRequested=%t, want (0, false)", params.AgeDays, params.TrimRequested)
	}
}

func TestParseBackupArchiveArgs_ZeroTrimMeansTrimEverythingVerified(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", "0"}
	_, params, err := ParseBackupArchiveArgs(args, cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !params.TrimRequested || params.AgeDays != 0 {
		t.Errorf("got AgeDays=%d TrimRequested=%t, want (0, true)", params.AgeDays, params.TrimRequested)
	}
}

func TestParseBackupArchiveArgs_InvalidTrimIsAUsageError(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", "abc"}
	if _, _, err := ParseBackupArchiveArgs(args, cliArgsTestFleet); err == nil {
		t.Fatal("expected an error for a non-numeric, non-blank trim argument")
	}
}

func TestParseBackupArchiveArgs_WrongArityIsAUsageErrorNamingTheLeaf(t *testing.T) {
	for _, args := range [][]string{
		{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu"},
		{"caltechdata", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", "0", "extra"},
		{},
	} {
		_, _, err := ParseBackupArchiveArgs(args, cliArgsTestFleet)
		if err == nil {
			t.Fatalf("expected a usage error for %d args, got none", len(args))
		}
		if !strings.Contains(err.Error(), "archive-sql-backups-to-s3") {
			t.Errorf("error should name the leaf, got: %v", err)
		}
	}
}

func TestParseBackupArchiveArgs_UnknownInstanceIsAUsageError(t *testing.T) {
	args := []string{"does-not-exist", "/opt/rdm_sql_backups", "s3://sql-backups.library.caltech.edu", ""}
	if _, _, err := ParseBackupArchiveArgs(args, cliArgsTestFleet); err == nil {
		t.Fatal("expected an error for an unmatched instance argument")
	}
}

func TestParseOpenSearchArchiveArgs_HappyPath(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_opensearch_backups", "s3://sql-backups.library.caltech.edu", "30"}
	inst, directory, bucket, cleanupDays, cleanupRequested, err := ParseOpenSearchArchiveArgs(args, cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.InstanceID != "i-2" || directory != "/opt/rdm_opensearch_backups" || bucket != "s3://sql-backups.library.caltech.edu" {
		t.Errorf("got inst=%q directory=%q bucket=%q, want i-2/opt.../bucket", inst.InstanceID, directory, bucket)
	}
	if !cleanupRequested || cleanupDays != 30 {
		t.Errorf("got cleanupDays=%d cleanupRequested=%t, want (30, true)", cleanupDays, cleanupRequested)
	}
}

func TestParseOpenSearchArchiveArgs_BlankCleanupMeansSkip(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_opensearch_backups", "s3://sql-backups.library.caltech.edu", ""}
	_, _, _, cleanupDays, cleanupRequested, err := ParseOpenSearchArchiveArgs(args, cliArgsTestFleet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanupRequested || cleanupDays != 0 {
		t.Errorf("got cleanupDays=%d cleanupRequested=%t, want (0, false)", cleanupDays, cleanupRequested)
	}
}

// TestParseOpenSearchArchiveArgs_ZeroCleanupIsRejected pins the one
// place SQL trim and OpenSearch cleanup disagree: unlike trim, "0" is
// not a valid cleanup threshold (promptOpenSearchCleanupDays' own rule)
// -- there's no "clean up everything regardless of age" reading for
// S3-side snapshot cleanup.
func TestParseOpenSearchArchiveArgs_ZeroCleanupIsRejected(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_opensearch_backups", "s3://sql-backups.library.caltech.edu", "0"}
	if _, _, _, _, _, err := ParseOpenSearchArchiveArgs(args, cliArgsTestFleet); err == nil {
		t.Fatal("expected an error -- \"0\" is not a valid cleanup threshold")
	}
}

func TestParseOpenSearchArchiveArgs_WrongArityIsAUsageErrorNamingTheLeaf(t *testing.T) {
	args := []string{"caltechdata", "/opt/rdm_opensearch_backups", "s3://sql-backups.library.caltech.edu"}
	_, _, _, _, _, err := ParseOpenSearchArchiveArgs(args, cliArgsTestFleet)
	if err == nil {
		t.Fatal("expected a usage error for 3 args")
	}
	if !strings.Contains(err.Error(), "archive-opensearch-snapshot-to-s3") {
		t.Errorf("error should name the leaf, got: %v", err)
	}
}
