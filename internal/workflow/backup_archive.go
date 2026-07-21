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
	AgeDays    int
	Bucket     string
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

	ageDays, err := promptAgeDays(w, input, output)
	if err != nil {
		return err
	}

	params := BackupArchiveParams{InstanceID: inst.InstanceID, Directory: directory, AgeDays: ageDays, Bucket: bucket}

	allFiles, err := ListBackupFiles(ctx, ssmClient, params.InstanceID, params.Directory, DefaultBackupListTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	candidates := FilterByAge(allFiles, params.AgeDays, time.Now())
	if len(candidates) == 0 {
		fmt.Fprintln(w, "No files match the age threshold. Nothing to do.")
		return nil
	}

	displayBackupDryRun(w, candidates)

	ok, err := ConfirmDestructive([]string{inst.InstanceID, inst.Name}, WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	// Namespaces every uploaded key by the source instance, so backups
	// from different systems sharing this bucket don't collide on
	// identically- or similarly-named files (see DECISIONS.md,
	// "Namespace backup uploads by instance"). Falls back to the
	// instance ID when Name is blank -- an untagged instance still
	// needs a non-empty, unique prefix.
	prefix := inst.Name
	if prefix == "" {
		prefix = inst.InstanceID
	}

	uploads, err := UploadBackupFiles(ctx, ssmClient, params.InstanceID, candidates, params.Bucket, prefix, DefaultBackupUploadTimeout, DefaultSSMPollInterval, func(p UploadProgress) {
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

	pathByKey := make(map[string]string, len(candidates))
	for _, f := range candidates {
		pathByKey[uploadKey(prefix, f.Path)] = f.Path
	}

	var toDelete []string
	var failedKeys []string
	var bytesFreed int64
	for _, v := range verified {
		if v.Verified {
			if p, ok := pathByKey[v.Key]; ok {
				toDelete = append(toDelete, p)
				bytesFreed += v.SizeBytes
			}
		} else {
			failedKeys = append(failedKeys, v.Key)
		}
	}

	if err := DeleteVerifiedFiles(ctx, ssmClient, params.InstanceID, toDelete, DefaultBackupDeleteTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	if _, status, err := RunShellCommand(ctx, ssmClient, params.InstanceID, "sudo fstrim -av", DefaultBackupFstrimTimeout, DefaultSSMPollInterval); err != nil {
		fmt.Fprintf(w, "fstrim did not complete: %v\n", err)
	} else if status != ssmtypes.CommandInvocationStatusSuccess {
		fmt.Fprintf(w, "fstrim did not complete (status: %s)\n", status)
	}

	fmt.Fprintf(w, "\nArchived and deleted %d file(s), freed %d bytes.\n", len(toDelete), bytesFreed)
	if len(failedKeys) > 0 {
		fmt.Fprintf(w, "%d file(s) failed verification and were left untouched: %s\n", len(failedKeys), strings.Join(failedKeys, ", "))
	}
	return nil
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

// promptAgeDays prompts for a positive integer age threshold, re-prompting
// on invalid input. No default -- an explicit, deliberate choice every
// time (see DESIGN.md, Feature 11).
func promptAgeDays(w io.Writer, input io.Reader, output io.Writer) (int, error) {
	var days int
	_, err := ui.Prompt("Age threshold in days", ui.WithValidator(func(s string) error {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n <= 0 {
			return errors.New("must be a positive integer")
		}
		days = n
		return nil
	}), ui.WithIO(input, output))
	if err != nil {
		return 0, err
	}
	return days, nil
}

func displayBackupDryRun(w io.Writer, files []BackupFile) {
	fmt.Fprintln(w, "\n=== DRY RUN: candidate files ===")
	var total int64
	for _, f := range files {
		ageDays := time.Since(f.ModTime).Hours() / 24
		fmt.Fprintf(w, "  %s  %d bytes  %.0f days old\n", f.Path, f.SizeBytes, ageDays)
		total += f.SizeBytes
	}
	fmt.Fprintf(w, "Total: %d file(s), %d bytes\n", len(files), total)
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
