package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/workflow"
)

func TestQuoteArg_PlainValueGetsWrappedInSingleQuotes(t *testing.T) {
	if got := quoteArg("caltechauthors-v13"); got != "'caltechauthors-v13'" {
		t.Errorf("got %q, want %q", got, "'caltechauthors-v13'")
	}
}

func TestQuoteArg_EmptyStringStaysVisiblyPresent(t *testing.T) {
	if got := quoteArg(""); got != "''" {
		t.Errorf("got %q, want %q -- an empty positional arg must not vanish from the pasted line", got, "''")
	}
}

// TestQuoteArg_EmbeddedSingleQuoteRoundTripsThroughAShell pins the one
// case that needs real escaping: shell out to `sh -c` and confirm the
// quoted form reproduces the original value exactly, for both a value
// containing a single quote and one containing a space.
func TestQuoteArg_EmbeddedSingleQuoteRoundTripsThroughAShell(t *testing.T) {
	for _, want := range []string{
		"O'Brien's directory",
		"/opt/rdm sql backups",
		"plain",
		"",
	} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+quoteArg(want)).Output()
		if err != nil {
			t.Fatalf("sh -c failed for %q: %v", want, err)
		}
		if string(out) != want {
			t.Errorf("round-trip for %q: got %q", want, string(out))
		}
	}
}

func TestPastableArchiveSQLCommand_RendersAllFourArguments(t *testing.T) {
	p := workflow.BackupArchiveParams{InstanceID: "i-0effbcc6cfb91097e", Directory: "/opt/rdm_sql_backups", Bucket: "s3://sql-backups.library.caltech.edu", AgeDays: 30, TrimRequested: true}
	got := pastableArchiveSQLCommand("clasm", p)
	want := "clasm rdm-backup-and-restore archive-sql-backups-to-s3 'i-0effbcc6cfb91097e' '/opt/rdm_sql_backups' 's3://sql-backups.library.caltech.edu' '30'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPastableArchiveSQLCommand_NoTrimRendersAnEmptyQuotedArgument(t *testing.T) {
	p := workflow.BackupArchiveParams{InstanceID: "i-1", Directory: "/opt/dir", Bucket: "my-bucket", TrimRequested: false}
	got := pastableArchiveSQLCommand("clasm", p)
	if !strings.HasSuffix(got, "''") {
		t.Errorf("got %q, want it to end with an empty quoted trim argument", got)
	}
}

func TestPastableArchiveOpenSearchCommand_RendersAllFourArguments(t *testing.T) {
	p := workflow.ArchiveOpenSearchParams{InstanceID: "i-1", Directory: "/opt/rdm_opensearch_backups", Bucket: "my-os-bucket", CleanupDays: 30, CleanupRequested: true}
	got := pastableArchiveOpenSearchCommand("clasm", p)
	want := "clasm rdm-backup-and-restore archive-opensearch-snapshot-to-s3 'i-1' '/opt/rdm_opensearch_backups' 'my-os-bucket' '30'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPastableArchiveOpenSearchCommand_NoCleanupRendersAnEmptyQuotedArgument(t *testing.T) {
	p := workflow.ArchiveOpenSearchParams{InstanceID: "i-1", Directory: "/opt/dir", Bucket: "my-bucket", CleanupRequested: false}
	got := pastableArchiveOpenSearchCommand("clasm", p)
	if !strings.HasSuffix(got, "''") {
		t.Errorf("got %q, want it to end with an empty quoted cleanup argument", got)
	}
}
