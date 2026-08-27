package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// Default timeouts for Backup Archive & Trim's remote SSM operations.
// Listing/delete/fstrim are quick; upload can legitimately take a while
// for large backup files, so it gets a much longer bound.
const (
	DefaultBackupListTimeout   = 2 * time.Minute
	DefaultBackupUploadTimeout = 30 * time.Minute
	DefaultBackupDeleteTimeout = 2 * time.Minute
	DefaultBackupFstrimTimeout = 5 * time.Minute
)

// BackupArchiveParams is the resolved parameter set for one Backup
// Archive & Trim run.
type BackupArchiveParams struct {
	InstanceID string
	Directory  string
	// AgeDays governs only which local copies may be deleted once
	// they're confirmed archived -- never what gets copied (DR-0170).
	// Meaningful only when TrimRequested is true; 0 with TrimRequested
	// means "delete every file confirmed archived", while a blank
	// answer leaves TrimRequested false and deletes nothing.
	AgeDays       int
	TrimRequested bool
	Bucket        string
}

// BackupHistory is Backup Archive & Trim's previously-recorded
// instance/directory choices, used to pre-position the instance
// picker's cursor and default the directory prompt, plus the callback
// used to persist new choices for next time. Callers (cmd/clasm/
// main.go) own the actual on-disk format (internal/state), keeping this
// package decoupled from state-file I/O (DECISIONS.md, "Recall Backup
// Archive & Trim's instance/directory choices per-instance"). The zero
// value disables all of this (no pre-selection, no default, no
// persistence) -- existing/test callers that don't pass one behave
// exactly as before this feature existed.
type BackupHistory struct {
	// LastInstanceID pre-positions the instance picker's cursor, if it
	// matches one of the instances offered.
	LastInstanceID string
	// LastDirectoryByInstance pre-fills the directory prompt's default
	// for the picked instance, if present -- takes priority over
	// backupDirRules' Name-pattern-based default, since it reflects
	// what was actually typed for this exact instance most recently
	// rather than a generic pattern match.
	LastDirectoryByInstance map[string]string
	// Save persists the chosen instance/directory for next time. Nil
	// disables persistence (every existing test, and any caller that
	// doesn't want it). A non-nil error is reported to w as a warning,
	// not fatal -- history is a convenience, not core to the backup
	// itself.
	Save func(instanceID, directory string) error
}

// BackupArchiveAndTrim runs the full Backup Archive & Trim workflow
// (DESIGN.md, Feature 11): pick an instance, immediately check
// CheckAWSCLIAvailable (see DECISIONS.md, "Preflight check: AWS CLI
// availability before Backup Archive & Trim") -- this project's most
// common real-AWS failure so far, now surfaced as one clear error
// before any prompts, instead of every subsequent upload silently
// reporting FAIL -- then prompt for the backup directory, then the S3
// bucket -- a filterable pick list of this account's buckets, plus
// "Other" to type any bucket name directly (see DECISIONS.md, "Bucket
// picker for Backup Archive & Trim") -- immediately followed by
// BucketRegion + newS3Client to build an S3 client actually scoped to
// that bucket's region (a bucket can be in any region, unrelated to the
// instance's -- see DECISIONS.md,
// "Resolve a bucket's actual region before Backup Archive & Trim's
// access check"), then CheckS3BucketAccess, aborting before the
// (potentially slow) dry-run list if the bucket doesn't exist or the
// operator's own credentials can't reach it (see DECISIONS.md,
// "Preflight check: S3 bucket access before Backup Archive & Trim's
// dry-run list") -- then the age threshold (explicit, no default;
// asked last since it's most naturally read as "of the files in that
// directory, which are old enough to move to that bucket" -- see
// DECISIONS.md, "Reorder Backup Archive & Trim's prompts"), dry-run
// list, type-to-confirm, upload, independently verify via
// s3:HeadObject, delete only the verified files via a second SSM
// command, fstrim, and report bytes freed plus any verification
// failures (left untouched).
// Takes a per-region SSM client map and resolves the one matching the
// picked instance's region. s3Client is used only to discover the
// bucket's region (BucketRegion works from a client scoped to any
// region); newS3Client then builds the client actually used for every
// other S3 call in this run, scoped to the bucket's real region.
// backupDirRules (~/.awsops' backup_directories, see DECISIONS.md,
// "Configure per-instance backup directories by Name pattern")
// pre-fills the backup directory prompt with the first matching rule's
// directory for the picked instance's Name tag, still editable -- there
// is deliberately no rule-match-skips-the-prompt mode, consistent with
// this workflow's other fields having no silent defaults. hist recalls
// (and, once both are answered, persists) the instance/directory
// actually used last time, taking priority over backupDirRules for the
// directory default (DECISIONS.md, "Recall Backup Archive & Trim's
// instance/directory choices per-instance"); its zero value disables
// all of that.
func BackupArchiveAndTrim(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance, backupDirRules []config.BackupDirectoryRule, hist BackupHistory) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstanceDefaulted(ctx, "Select an instance", "Connects to this instance via SSM to list and upload backup files.", instances, hist.LastInstanceID)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return backupArchiveAndTrim(ctx, w, ssmClients, s3Client, newS3Client, inst, backupDirRules, hist, nil, nil)
}

// backupArchiveAndTrim is BackupArchiveAndTrim's testable core, once an
// instance is resolved -- instance selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as
// terminateEC2Instance (terminate_instance.go). input/output are nil in
// production and supplied by tests to drive every prompt/confirm in this
// function through its accessible-mode pipe path instead.
func backupArchiveAndTrim(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, backupDirRules []config.BackupDirectoryRule, hist BackupHistory, input io.Reader, output io.Writer) error {
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
	} else if def := config.BackupDirectoryFor(backupDirRules, inst.Name); def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	}
	dirPromptOpts = append(dirPromptOpts, ui.WithIO(input, output))
	directory, err := ui.Prompt("Backup directory (e.g. /opt/rdm_sql_backups)", dirPromptOpts...)
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

	trimDays, trimRequested, err := promptLocalTrimDays(input, output)
	if err != nil {
		return err
	}

	params := BackupArchiveParams{InstanceID: inst.InstanceID, Directory: directory, AgeDays: trimDays, TrimRequested: trimRequested, Bucket: bucket}

	allFiles, err := ListBackupFiles(ctx, ssmClient, params.InstanceID, params.Directory, DefaultBackupListTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if len(allFiles) == 0 {
		fmt.Fprintf(w, "No files in %s on %s. Nothing to do.\n", params.Directory, params.InstanceID)
		return nil
	}

	// Namespaces every uploaded key by the source instance, so backups
	// from different systems sharing this bucket don't collide on
	// identically- or similarly-named files (see DECISIONS.md,
	// "Namespace backup uploads by instance"). Falls back to the
	// instance ID when Name is blank -- an untagged instance still
	// needs a non-empty, unique prefix. Resolved before the dry run
	// because the already-archived pre-pass needs the destination keys.
	prefix := inst.Name
	if prefix == "" {
		prefix = inst.InstanceID
	}

	// Every file in the directory is a copy candidate (DR-0170,
	// decision 1); the pre-pass then drops the ones already safely in
	// the bucket, so a steady-state run re-sends nothing over SSM.
	stopPrePassTicker := startProgressTicker(w, "checking which backups are already archived")
	already := CheckAlreadyArchived(ctx, bucketClient, params.Bucket, prefix, allFiles)
	stopPrePassTicker()

	archivedKeys := make(map[string]bool, len(already))
	for _, a := range already {
		if a.Verified {
			archivedKeys[a.Key] = true
		}
	}

	toUpload := make([]BackupFile, 0, len(allFiles))
	for _, f := range allFiles {
		if !archivedKeys[uploadKey(prefix, f.Path)] {
			toUpload = append(toUpload, f)
		}
	}

	// The age threshold now governs only which *local* copies may be
	// removed once they're confirmed safe in S3 -- never what gets
	// copied. A blank answer means nothing is deleted at all.
	var aged []BackupFile
	if params.TrimRequested {
		aged = FilterByAge(allFiles, params.AgeDays, time.Now())
	}

	displayBackupDryRun(w, toUpload, len(allFiles)-len(toUpload), aged, prefix, archivedKeys)

	// Friction tracks the actual risk, not the menu entry: a run that
	// deletes nothing is a pure copy and gets a plain yes/no, while one
	// that removes files still demands the instance name typed out
	// (DR-0170, decision 6).
	var ok bool
	if len(aged) > 0 {
		ok, err = ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
	} else {
		ok, err = Confirm(fmt.Sprintf("Copy %d file(s) to s3://%s/%s/ ? Nothing will be deleted.", len(toUpload), params.Bucket, prefix), WithConfirmIO(input, output))
	}
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	uploads, err := UploadBackupFiles(ctx, ssmClient, params.InstanceID, toUpload, params.Bucket, prefix, DefaultBackupUploadTimeout, DefaultSSMPollInterval, func(p UploadProgress) {
		status := "OK"
		if !p.Result.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  ... uploading %d/%d (%s of %s) - %s %s\n", p.Done, p.Total, formatBytes(p.BytesDone), formatBytes(p.BytesTotal), status, p.Result.Key)
	})
	if err != nil {
		return err
	}

	stopVerifyTicker := startProgressTicker(w, "verifying uploads via s3:HeadObject")
	verified := VerifyUploads(ctx, bucketClient, params.Bucket, uploads)
	stopVerifyTicker()

	// One merged evidence set: an object this run uploaded and verified
	// and an object an earlier run left behind are the same proof, so
	// the delete gate below asks one question and takes one path
	// (DR-0170, decision 3).
	verifiedKeys := make(map[string]bool, len(already)+len(verified))
	for key := range archivedKeys {
		verifiedKeys[key] = true
	}
	var copied int
	var bytesCopied int64
	var failedKeys []string
	for _, v := range verified {
		if v.Verified {
			verifiedKeys[v.Key] = true
			copied++
			bytesCopied += v.SizeBytes
		} else {
			failedKeys = append(failedKeys, v.Key)
		}
	}

	var toDelete []string
	var bytesFreed int64
	for _, f := range aged {
		if verifiedKeys[uploadKey(prefix, f.Path)] {
			toDelete = append(toDelete, f.Path)
			bytesFreed += f.SizeBytes
		}
	}

	if len(toDelete) > 0 {
		if err := DeleteVerifiedFiles(ctx, ssmClient, params.InstanceID, toDelete, DefaultBackupDeleteTimeout, DefaultSSMPollInterval); err != nil {
			return err
		}
		// Only worth the SSM round trip when something was actually
		// removed -- a copy-only run frees nothing to trim.
		if _, status, err := RunShellCommand(ctx, ssmClient, params.InstanceID, "sudo fstrim -av", DefaultBackupFstrimTimeout, DefaultSSMPollInterval); err != nil {
			fmt.Fprintf(w, "fstrim did not complete: %v\n", err)
		} else if status != ssmtypes.CommandInvocationStatusSuccess {
			fmt.Fprintf(w, "fstrim did not complete (status: %s)\n", status)
		}
	}

	fmt.Fprintf(w, "\nCopied %d file(s) (%d bytes); %d already in S3; deleted %d local file(s), freed %d bytes.\n",
		copied, bytesCopied, len(archivedKeys), len(toDelete), bytesFreed)
	if len(failedKeys) > 0 {
		fmt.Fprintf(w, "%d file(s) failed to copy and are still only on %s: %s\n", len(failedKeys), params.InstanceID, strings.Join(failedKeys, ", "))
		// The dangerous case: old enough to have been trimmed, but the
		// copy didn't land, so it was correctly kept -- and must not be
		// kept silently.
		if agedFailures := intersectKeys(aged, prefix, failedKeys); len(agedFailures) > 0 {
			fmt.Fprintf(w, "  %d of those were old enough to trim and were deliberately left in place: %s\n", len(agedFailures), strings.Join(agedFailures, ", "))
		}
	}
	return nil
}

// intersectKeys returns the subset of failedKeys whose files are also in
// aged -- the copy failures that would otherwise have been deleted this
// run.
func intersectKeys(aged []BackupFile, prefix string, failedKeys []string) []string {
	failed := make(map[string]bool, len(failedKeys))
	for _, k := range failedKeys {
		failed[k] = true
	}
	var out []string
	for _, f := range aged {
		if key := uploadKey(prefix, f.Path); failed[key] {
			out = append(out, key)
		}
	}
	return out
}

// bucketChoice is one entry in promptBackupBucket's pick list: either an
// already-known bucket, or "Other" to type any bucket name directly.
type bucketChoice struct {
	label string
	name  string
	other bool
}

// promptBackupBucketFunc indirects backupArchiveAndTrim's call to
// promptBackupBucket through a package-level var, so a test can
// substitute a fake that returns huh.ErrUserAborted directly --
// promptBackupBucket's own huh.Select Quit keybinding (q/ctrl+c) can't
// be driven from accessible mode the way every other prompt in this
// function's pipe-testable sequence can (accessible mode has no
// keyboard to interrupt, only a plain io.Reader/io.Writer pair -- see
// domain_menu.go's mapMenuPickerErr doc for the same limitation), so
// this is the only seam that can exercise backupArchiveAndTrim's own
// handling of a cancelled bucket pick.
var promptBackupBucketFunc = promptBackupBucket

// promptBackupBucket lists this account's S3 buckets and lets the
// operator pick one via a filterable Menu-tier huh.Select ('/' to
// filter by name, matching every other filterable screen in this app),
// plus "Other" to type any bucket name directly -- e.g. one outside
// this account's own listing, or not yet reflected in it (DECISIONS.md,
// "Bucket picker for Backup Archive & Trim"). Falls back entirely to
// the original free-text prompt if the listing can't be fetched or
// comes back empty, matching promptKeyPairNameOrCreate's own precedent
// for the same reason: there's nothing more reliable (or, for an empty
// account, nothing useful) to offer instead. Deliberately a huh.Select
// (accessible-mode pipe-testable), not a tui.RunPicker -- unlike every
// other bucket-selection call site, this one needs to stay embedded
// inside backupArchiveAndTrim's own pipe-testable prompt sequence
// (directory, then bucket, then age threshold), and a real bubbletea
// Program can't be driven by a test's pipe input the way pickBucket's
// callers already accept.
func promptBackupBucket(ctx context.Context, w io.Writer, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), input io.Reader, output io.Writer) (string, error) {
	buckets, err := inventory.ListBuckets(ctx, s3Client, newS3Client)
	if err != nil || len(buckets) == 0 {
		return ui.Prompt("S3 bucket", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	}

	choices := make([]bucketChoice, 0, len(buckets)+1)
	for _, b := range buckets {
		choices = append(choices, bucketChoice{label: bucketLabel(b), name: b.Name})
	}
	choices = append(choices, bucketChoice{label: "Other (type a bucket name)", other: true})

	picked, err := pickComparable(w, "Select a bucket", "Type / to filter by name, or choose Other to type any bucket name directly.", hintCancel, choices, func(c bucketChoice) string { return c.label }, input, output)
	if err != nil {
		return "", err
	}
	if picked.other {
		return ui.Prompt("S3 bucket", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	}
	return picked.name, nil
}

// promptLocalTrimDays prompts for the optional local-retention
// threshold. Three answers, all meaningful (DR-0170, decision 4): blank
// keeps every local copy, "0" deletes every file confirmed archived,
// and a positive N deletes the confirmed ones older than N days. Blank
// and 0 are both falsy as a bare int and mean opposite things, hence
// the separate requested flag -- the same shape, and the same reason,
// as promptOpenSearchCleanupDays. Replaces promptAgeDays, which
// rejected both of the new answers.
//
// The question names the instance's EBS volume explicitly rather than
// mirroring the OpenSearch cleanup prompt's wording: that one deletes
// previously-archived snapshots *in S3*, this one deletes local files
// *on the instance*, and a real user report (2026-07-29, DR-0151) found
// terser wording genuinely ambiguous about which.
func promptLocalTrimDays(input io.Reader, output io.Writer) (days int, requested bool, err error) {
	raw, err := ui.Prompt("Delete local backup files on the instance's EBS volume older than how many days? (blank to keep all local copies; 0 to delete every file successfully archived)", ui.WithValidator(func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		n, convErr := strconv.Atoi(s)
		if convErr != nil || n < 0 {
			return errors.New("must be blank (keep all local copies), 0, or a positive integer")
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

// displayBackupDryRun shows the two sets this workflow now derives, in
// their own labelled sections (DR-0172): what will be copied, and what
// will be deleted locally once confirmed archived. They answer different
// questions, and only the second determines which confirmation tier the
// caller uses.
//
// Already-archived files are a count in the copy section -- there is no
// decision to make about them -- but one that is *also* in the delete
// set stays listed and marked, because it will be removed on the
// strength of an object an earlier run uploaded, without being copied
// now. That is the case worth seeing before confirming.
func displayBackupDryRun(w io.Writer, toCopy []BackupFile, alreadyArchived int, toDelete []BackupFile, prefix string, archivedKeys map[string]bool) {
	fmt.Fprintln(w, "\n=== DRY RUN: to copy to S3 ===")
	var copyTotal int64
	for _, f := range toCopy {
		ageDays := time.Since(f.ModTime).Hours() / 24
		fmt.Fprintf(w, "  %s  %d bytes  %.0f days old\n", f.Path, f.SizeBytes, ageDays)
		copyTotal += f.SizeBytes
	}
	fmt.Fprintf(w, "Total: %d file(s), %d bytes\n", len(toCopy), copyTotal)
	if alreadyArchived > 0 {
		fmt.Fprintf(w, "%d file(s) already in S3, skipped\n", alreadyArchived)
	}

	fmt.Fprintln(w, "\n=== DRY RUN: to delete locally after verification ===")
	if len(toDelete) == 0 {
		fmt.Fprintln(w, "  (none -- every local copy is kept)")
		return
	}
	var deleteTotal int64
	for _, f := range toDelete {
		ageDays := time.Since(f.ModTime).Hours() / 24
		note := ""
		if archivedKeys[uploadKey(prefix, f.Path)] {
			note = "  (already in S3)"
		}
		fmt.Fprintf(w, "  %s  %d bytes  %.0f days old%s\n", f.Path, f.SizeBytes, ageDays, note)
		deleteTotal += f.SizeBytes
	}
	fmt.Fprintf(w, "Total: %d file(s), %d bytes\n", len(toDelete), deleteTotal)
}

// formatBytes renders n as a human-scaled size (e.g. "1.2 GiB") for the
// upload progress line -- raw byte counts for multi-gigabyte backups
// are hard to track at a glance.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
