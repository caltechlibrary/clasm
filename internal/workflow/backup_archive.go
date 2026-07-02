package workflow

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/config"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
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
// (DESIGN.md, Feature 11): pick an instance, prompt for the backup
// directory and age threshold (both explicit, no default), dry-run list,
// type-to-confirm, upload, independently verify via s3:HeadObject,
// delete only the verified files via a second SSM command, fstrim, and
// report bytes freed plus any verification failures (left untouched).
// Takes a per-region SSM client map (the S3 client stays a single
// client -- a bucket's home region is unrelated to the instance's
// region) and resolves the SSM client matching the picked instance's
// region. backupDirRules (~/.awsops' backup_directories, see
// DECISIONS.md, "Configure per-instance backup directories by Name
// pattern") pre-fills the backup directory prompt with the first
// matching rule's directory for the picked instance's Name tag, still
// editable -- there is deliberately no rule-match-skips-the-prompt
// mode, consistent with this workflow's other fields having no silent
// defaults.
func BackupArchiveAndTrim(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, instances []inventory.Instance, backupDirRules []config.BackupDirectoryRule) error {
	if len(instances) == 0 {
		t.Println("No instances found.")
		t.Refresh()
		return nil
	}

	inst, err := ui.PickList(t, le, instances, instanceLabel, "Select an instance")
	if err != nil {
		return cancelledIsNil(t, err)
	}
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
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

	stopUploadTicker := startProgressTicker(t, 30*time.Second, "uploading backup files to S3")
	uploads, err := UploadBackupFiles(ctx, ssmClient, params.InstanceID, candidates, params.Bucket, DefaultBackupUploadTimeout, DefaultSSMPollInterval)
	stopUploadTicker()
	if err != nil {
		return err
	}

	stopVerifyTicker := startProgressTicker(t, 30*time.Second, "verifying uploads via s3:HeadObject")
	verified := VerifyUploads(ctx, s3Client, params.Bucket, uploads)
	stopVerifyTicker()

	pathByKey := make(map[string]string, len(candidates))
	for _, f := range candidates {
		pathByKey[path.Base(f.Path)] = f.Path
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
