package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rsdoiel/termlib"

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

// BackupArchiveAndTrim runs the full Backup Archive & Trim workflow
// (DESIGN.md, Feature 11): pick an instance, immediately check
// CheckAWSCLIAvailable (see DECISIONS.md, "Preflight check: AWS CLI
// availability before Backup Archive & Trim") -- this project's most
// common real-AWS failure so far, now surfaced as one clear error
// before any prompts, instead of every subsequent upload silently
// reporting FAIL -- then prompt for the backup directory and age
// threshold (both explicit, no default) and the S3
// bucket -- immediately followed by BucketRegion + newS3Client to build
// an S3 client actually scoped to that bucket's region (a bucket can be
// in any region, unrelated to the instance's -- see DECISIONS.md,
// "Resolve a bucket's actual region before Backup Archive & Trim's
// access check"), then CheckS3BucketAccess, aborting before the
// (potentially slow) dry-run list if the bucket doesn't exist or the
// operator's own credentials can't reach it (see DECISIONS.md,
// "Preflight check: S3 bucket access before Backup Archive & Trim's
// dry-run list") -- then dry-run list, type-to-confirm, upload,
// independently verify via s3:HeadObject, delete only the verified files
// via a second SSM command, fstrim, and report bytes freed plus any
// verification failures (left untouched).
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
// this workflow's other fields having no silent defaults.
func BackupArchiveAndTrim(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance, backupDirRules []config.BackupDirectoryRule) error {
	if len(instances) == 0 {
		t.Println("No instances found.")
		t.Refresh()
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance", instances)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	return backupArchiveAndTrim(ctx, t, le, ssmClients, s3Client, newS3Client, inst, backupDirRules)
}

// backupArchiveAndTrim is BackupArchiveAndTrim's testable core, once an
// instance is resolved -- instance selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as
// terminateEC2Instance (terminate_instance.go).
func backupArchiveAndTrim(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, backupDirRules []config.BackupDirectoryRule) error {
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	if err := CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, DefaultBackupListTimeout, DefaultSSMPollInterval); err != nil {
		return err
	}

	dirPromptOpts := []ui.PromptOption{ui.WithValidator(requireNonEmpty)}
	if def := config.BackupDirectoryFor(backupDirRules, inst.Name); def != "" {
		dirPromptOpts = append(dirPromptOpts, ui.WithDefault(def))
	}
	directory, err := ui.Prompt(t, le, "Backup directory (e.g. /opt/rdm_sql_backups)", dirPromptOpts...)
	if err != nil {
		return err
	}

	ageDays, err := promptAgeDays(t, le)
	if err != nil {
		return err
	}

	bucket, err := ui.Prompt(t, le, "S3 bucket", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return err
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

	params := BackupArchiveParams{InstanceID: inst.InstanceID, Directory: directory, AgeDays: ageDays, Bucket: bucket}

	allFiles, err := ListBackupFiles(ctx, ssmClient, params.InstanceID, params.Directory, DefaultBackupListTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	candidates := FilterByAge(allFiles, params.AgeDays, time.Now())
	if len(candidates) == 0 {
		t.Println("No files match the age threshold. Nothing to do.")
		t.Refresh()
		return nil
	}

	displayBackupDryRun(t, candidates)

	ok, err := ConfirmDestructive(t, le, inst.InstanceID, inst.Name)
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
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
		t.Printf("  ... uploading %d/%d (%s of %s) - %s %s\n", p.Done, p.Total, formatBytes(p.BytesDone), formatBytes(p.BytesTotal), status, p.Result.Key)
		t.Refresh()
	})
	if err != nil {
		return err
	}

	stopVerifyTicker := startProgressTicker(t, 30*time.Second, "verifying uploads via s3:HeadObject")
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
		t.Printf("fstrim did not complete: %v\n", err)
		t.Refresh()
	} else if status != ssmtypes.CommandInvocationStatusSuccess {
		t.Printf("fstrim did not complete (status: %s)\n", status)
		t.Refresh()
	}

	t.Printf("\nArchived and deleted %d file(s), freed %d bytes.\n", len(toDelete), bytesFreed)
	if len(failedKeys) > 0 {
		t.Printf("%d file(s) failed verification and were left untouched: %s\n", len(failedKeys), strings.Join(failedKeys, ", "))
	}
	t.Refresh()
	return nil
}

// promptAgeDays prompts for a positive integer age threshold, re-prompting
// on invalid input. No default -- an explicit, deliberate choice every
// time (see DESIGN.md, Feature 11).
func promptAgeDays(t *termlib.Terminal, le *termlib.LineEditor) (int, error) {
	var days int
	_, err := ui.Prompt(t, le, "Age threshold in days", ui.WithValidator(func(s string) error {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n <= 0 {
			return errors.New("must be a positive integer")
		}
		days = n
		return nil
	}))
	if err != nil {
		return 0, err
	}
	return days, nil
}

func displayBackupDryRun(t *termlib.Terminal, files []BackupFile) {
	t.Println("\n=== DRY RUN: candidate files ===")
	var total int64
	for _, f := range files {
		ageDays := time.Since(f.ModTime).Hours() / 24
		t.Printf("  %s  %d bytes  %.0f days old\n", f.Path, f.SizeBytes, ageDays)
		total += f.SizeBytes
	}
	t.Printf("Total: %d file(s), %d bytes\n", len(files), total)
	t.Refresh()
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
