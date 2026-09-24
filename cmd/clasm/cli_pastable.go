package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caltechlibrary/clasm/internal/workflow"
)

// quoteArg shell-quotes s for safe inclusion in a pasted command line
// (PLAN.md Phase 20.64 item 5): wraps in single quotes -- the one
// POSIX-portable form needing no escaping except for an embedded single
// quote itself (closed, an escaped quote via a separate double-quoted
// segment, reopened). Every argument gets this treatment unconditionally,
// not just ones that happen to need it today -- a directory or bucket
// name with no space now is exactly the kind of value that eventually
// grows one, and the printed line must stay pastable without a
// hand-edit when it does. An empty string becomes `”` rather than
// vanishing, since a blank trim/cleanup argument still has to appear as
// its own positional argument.
func quoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pastableArchiveSQLCommand renders archive-sql-backups-to-s3's exact
// non-interactive equivalent of an interactive run that just used p
// (PLAN.md Phase 20.64 item 5) -- appName so the printed line matches
// however this binary was actually invoked, not a hardcoded "clasm".
func pastableArchiveSQLCommand(appName string, p workflow.BackupArchiveParams) string {
	trim := ""
	if p.TrimRequested {
		trim = strconv.Itoa(p.AgeDays)
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s",
		appName, workflow.RDMBackupRestoreDomainCLISlug, workflow.ArchiveSQLBackupsCLISlug,
		quoteArg(p.InstanceID), quoteArg(p.Directory), quoteArg(p.Bucket), quoteArg(trim))
}

// pastableArchiveOpenSearchCommand is pastableArchiveSQLCommand's
// OpenSearch-side analog.
func pastableArchiveOpenSearchCommand(appName string, p workflow.ArchiveOpenSearchParams) string {
	cleanup := ""
	if p.CleanupRequested {
		cleanup = strconv.Itoa(p.CleanupDays)
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s",
		appName, workflow.RDMBackupRestoreDomainCLISlug, workflow.ArchiveOpenSearchSnapshotCLISlug,
		quoteArg(p.InstanceID), quoteArg(p.Directory), quoteArg(p.Bucket), quoteArg(cleanup))
}
